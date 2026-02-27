package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// triageBacklog analyzes backlog tasks and decides whether to assign, decompose, or clarify.
// Called as a special cron job (like daily_notes).
func triageBacklog(ctx context.Context, cfg *Config, sem chan struct{}) {
	if !cfg.TaskBoard.Enabled {
		return
	}

	tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)
	if err := tb.initTaskBoardSchema(); err != nil {
		logError("triage: init schema failed", "error", err)
		return
	}

	tasks, err := tb.ListTasks("backlog", "", "")
	if err != nil {
		logError("triage: list backlog failed", "error", err)
		return
	}

	if len(tasks) == 0 {
		logDebug("triage: no backlog tasks")
		return
	}

	roster := buildAgentRoster(cfg)
	if roster == "" {
		logWarn("triage: no agents configured, skipping")
		return
	}

	logInfo("triage: processing backlog", "count", len(tasks))

	for _, t := range tasks {
		if ctx.Err() != nil {
			return
		}

		comments, _ := tb.GetThread(t.ID)
		if shouldSkipTriage(comments) {
			logDebug("triage: skipping (already triaged, no new replies)", "taskId", t.ID)
			continue
		}

		result := triageOneTask(ctx, cfg, sem, tb, t, comments, roster)
		if result == nil {
			continue
		}

		applyTriageResult(tb, t, result)
	}
}

// triageResult is the structured LLM response for triage decisions.
type triageResult struct {
	Action   string          `json:"action"`   // ready, decompose, clarify
	Assignee string          `json:"assignee"` // agent name (for ready)
	Subtasks []triageSubtask `json:"subtasks"` // (for decompose)
	Comment  string          `json:"comment"`  // reason or question
}

type triageSubtask struct {
	Title    string `json:"title"`
	Assignee string `json:"assignee"`
}

// triageOneTask sends a single backlog task to LLM for triage analysis.
func triageOneTask(ctx context.Context, cfg *Config, sem chan struct{}, tb *TaskBoardEngine, t TaskBoard, comments []TaskComment, roster string) *triageResult {
	// Build conversation thread.
	threadText := "(no comments)"
	if len(comments) > 0 {
		var lines []string
		for _, c := range comments {
			lines = append(lines, fmt.Sprintf("[%s] %s: %s", c.CreatedAt, c.Author, c.Content))
		}
		threadText = strings.Join(lines, "\n")
	}

	prompt := fmt.Sprintf(`You are a task triage agent for the Tetora AI team.

Analyze the backlog task below and decide how to handle it.

## Available Agents
%s

## Task
- ID: %s
- Title: %s
- Description: %s
- Priority: %s
- Project: %s

## Conversation
%s

## Rules
1. If the task is clear and actionable as-is, respond "ready" and pick the best agent
2. If the task is complex and should be split into 2-5 subtasks, respond "decompose"
3. If critical information is missing, respond "clarify" and ask a specific question
4. Match agents by their expertise (description + keywords)
5. Each subtask must have a clear title and assigned agent

Respond with ONLY valid JSON (no markdown fences):
{"action":"ready|decompose|clarify","assignee":"agent_name","subtasks":[{"title":"...","assignee":"agent_name"}],"comment":"reason or question"}`,
		roster, t.ID, t.Title, t.Description, t.Priority, t.Project, threadText)

	task := Task{
		Name:    "triage:" + t.ID,
		Prompt:  prompt,
		Model:   "haiku",
		Budget:  0.2,
		Timeout: "30s",
		Source:  "triage",
	}
	fillDefaults(cfg, &task)
	task.Model = "haiku" // force haiku regardless of defaults

	result := runSingleTask(ctx, cfg, task, sem, "")
	if result.Status != "success" {
		logWarn("triage: LLM call failed", "taskId", t.ID, "error", result.Error)
		return nil
	}

	// Parse JSON response.
	output := strings.TrimSpace(result.Output)
	// Strip markdown code fences if present.
	output = strings.TrimPrefix(output, "```json")
	output = strings.TrimPrefix(output, "```")
	output = strings.TrimSuffix(output, "```")
	output = strings.TrimSpace(output)

	var tr triageResult
	if err := json.Unmarshal([]byte(output), &tr); err != nil {
		logWarn("triage: failed to parse LLM response", "taskId", t.ID, "output", truncate(output, 200), "error", err)
		return nil
	}

	if tr.Action != "ready" && tr.Action != "decompose" && tr.Action != "clarify" {
		logWarn("triage: unknown action", "taskId", t.ID, "action", tr.Action)
		return nil
	}

	return &tr
}

// applyTriageResult executes the triage decision on a task.
func applyTriageResult(tb *TaskBoardEngine, t TaskBoard, tr *triageResult) {
	switch tr.Action {
	case "ready":
		if tr.Assignee == "" {
			logWarn("triage: ready but no assignee", "taskId", t.ID)
			return
		}
		if _, err := tb.AssignTask(t.ID, tr.Assignee); err != nil {
			logWarn("triage: assign failed", "taskId", t.ID, "error", err)
			return
		}
		if _, err := tb.MoveTask(t.ID, "todo"); err != nil {
			logWarn("triage: move to todo failed", "taskId", t.ID, "error", err)
			return
		}
		comment := fmt.Sprintf("[triage] Assigned to %s. Reason: %s", tr.Assignee, tr.Comment)
		tb.AddComment(t.ID, "triage", comment)
		logInfo("triage: task ready", "taskId", t.ID, "assignee", tr.Assignee)

	case "decompose":
		if len(tr.Subtasks) == 0 {
			logWarn("triage: decompose but no subtasks", "taskId", t.ID)
			return
		}
		var created []string
		for _, sub := range tr.Subtasks {
			newTask, err := tb.CreateTask(TaskBoard{
				Title:    sub.Title,
				Status:   "todo",
				Assignee: sub.Assignee,
				Priority: t.Priority,
				Project:  t.Project,
			})
			if err != nil {
				logWarn("triage: create subtask failed", "taskId", t.ID, "title", sub.Title, "error", err)
				continue
			}
			created = append(created, fmt.Sprintf("- %s → %s (%s)", newTask.ID, sub.Title, sub.Assignee))
		}
		comment := fmt.Sprintf("[triage] Decomposed into %d subtasks:\n%s\n\nReason: %s",
			len(created), strings.Join(created, "\n"), tr.Comment)
		tb.AddComment(t.ID, "triage", comment)
		tb.MoveTask(t.ID, "done")
		logInfo("triage: task decomposed", "taskId", t.ID, "subtasks", len(created))

	case "clarify":
		if tr.Comment == "" {
			logWarn("triage: clarify but no comment", "taskId", t.ID)
			return
		}
		comment := fmt.Sprintf("[triage] Need clarification: %s", tr.Comment)
		tb.AddComment(t.ID, "triage", comment)
		logInfo("triage: asked for clarification", "taskId", t.ID)
	}
}

// buildAgentRoster generates a summary of available agents for the triage prompt.
func buildAgentRoster(cfg *Config) string {
	if len(cfg.Agents) == 0 {
		return ""
	}
	var lines []string
	for name, ac := range cfg.Agents {
		line := fmt.Sprintf("- %s: %s", name, ac.Description)
		if len(ac.Keywords) > 0 {
			line += fmt.Sprintf(" (keywords: %s)", strings.Join(ac.Keywords, ", "))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// shouldSkipTriage returns true if the last comment is from "triage" and no human
// has replied since — prevents re-triaging the same task repeatedly.
func shouldSkipTriage(comments []TaskComment) bool {
	if len(comments) == 0 {
		return false // first triage
	}
	// Walk backwards: if the most recent comment is from triage and no non-triage
	// comment exists after it, skip.
	last := comments[len(comments)-1]
	if last.Author != "triage" {
		return false // human replied, re-triage
	}
	// Check if there's a recent non-triage comment after the last triage comment's time.
	lastTriageTime, _ := time.Parse(time.RFC3339, last.CreatedAt)
	for i := len(comments) - 2; i >= 0; i-- {
		c := comments[i]
		cTime, _ := time.Parse(time.RFC3339, c.CreatedAt)
		if c.Author != "triage" && cTime.After(lastTriageTime) {
			return false // someone responded after triage
		}
	}
	return true // last word is triage's, skip
}
