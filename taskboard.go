package main

// taskboard.go — thin shim over internal/taskboard.
//
// All types are aliases to internal/taskboard so callers see no type change.
// All engine methods are inherited automatically via type alias.
// Only root-specific wiring (constructors, cmdTask CLI) lives here.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"tetora/internal/config"
	"tetora/internal/discord"
	dtypes "tetora/internal/dispatch"
	"tetora/internal/log"
	"tetora/internal/taskboard"
	"tetora/internal/worktree"
)

// --- Type aliases (zero-cost, no wrapper overhead) ---

type TaskBoardEngine = taskboard.Engine
type TaskBoardDispatcher = taskboard.Dispatcher
type TaskBoard = taskboard.TaskBoard
type TaskComment = taskboard.TaskComment
type TaskListResult = taskboard.TaskListResult
type Pagination = taskboard.Pagination
type BoardView = taskboard.BoardView
type BoardStats = taskboard.BoardStats
type BoardFilter = taskboard.BoardFilter
type ProjectStats = taskboard.ProjectStats

// --- Constructor shims ---

func newTaskBoardEngine(dbPath string, cfg TaskBoardConfig, webhooks []WebhookConfig) *TaskBoardEngine {
	return taskboard.NewEngine(dbPath, cfg, webhooks)
}

// hasBlockingDeps returns true if any dependency of the task is not yet done.
func hasBlockingDeps(tb *TaskBoardEngine, t TaskBoard) bool {
	return taskboard.HasBlockingDeps(tb, t)
}

// normalizeTaskID adds "task-" prefix if the ID looks like a bare number.
func normalizeTaskID(id string) string {
	return taskboard.NormalizeTaskID(id)
}

func generateID(prefix string) string {
	return taskboard.GenerateID(prefix)
}

func detectDefaultBranch(workdir string) string {
	return taskboard.DetectDefaultBranch(workdir)
}

// --- initTaskBoardSchema is an alias for InitSchema ---
// (The method name changed between root and internal; keep backward-compat for callers.)
// Note: *TaskBoardEngine has InitSchema() directly via alias, but callers still use
// the old lowercase name in tests and tool_taskboard.go. Provide a package-level shim.

// --- cmdTask CLI ---

func cmdTask(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora task <list|create|show|update|move|assign|comment|thread>")
		fmt.Println("\nCommands:")
		fmt.Println("  list [--status=STATUS] [--assignee=AGENT] [--project=PROJECT]")
		fmt.Println("  create --title=TITLE [--description=DESC] [--priority=PRIORITY] [--assignee=AGENT] [--type=TYPE] [--depends-on=ID]...")
		fmt.Println("  show TASK_ID [--full]")
		fmt.Println("  update TASK_ID [--title=TITLE] [--description=DESC] [--priority=PRIORITY]")
		fmt.Println("  move TASK_ID --status=STATUS")
		fmt.Println("  assign TASK_ID --assignee=AGENT")
		fmt.Println("  comment TASK_ID --author=AUTHOR --content=CONTENT [--type=TYPE]")
		fmt.Println("  thread TASK_ID")
		os.Exit(0)
	}

	cfg := loadConfig("")
	if !cfg.TaskBoard.Enabled {
		fmt.Println("Error: task board not enabled in config")
		os.Exit(1)
	}

	tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)
	if err := tb.InitSchema(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	cmd := args[0]
	args = args[1:]

	switch cmd {
	case "list":
		var status, assignee, project string
		for _, arg := range args {
			if strings.HasPrefix(arg, "--status=") {
				status = strings.TrimPrefix(arg, "--status=")
			} else if strings.HasPrefix(arg, "--assignee=") {
				assignee = strings.TrimPrefix(arg, "--assignee=")
			} else if strings.HasPrefix(arg, "--project=") {
				project = strings.TrimPrefix(arg, "--project=")
			}
		}

		tasks, err := tb.ListTasks(status, assignee, project)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if len(tasks) == 0 {
			fmt.Println("No tasks found")
			return
		}

		fmt.Printf("Found %d tasks:\n\n", len(tasks))
		for _, t := range tasks {
			fmt.Printf("ID: %s\n", t.ID)
			fmt.Printf("Title: %s\n", t.Title)
			fmt.Printf("Status: %s | Priority: %s | Assignee: %s\n", t.Status, t.Priority, t.Assignee)
			if t.Description != "" {
				fmt.Printf("Description: %s\n", t.Description)
			}
			fmt.Printf("Created: %s | Updated: %s\n", t.CreatedAt, t.UpdatedAt)
			fmt.Println()
		}

	case "create":
		var title, description, priority, assignee, taskType string
		var dependsOn []string
		for _, arg := range args {
			if strings.HasPrefix(arg, "--title=") {
				title = strings.TrimPrefix(arg, "--title=")
			} else if strings.HasPrefix(arg, "--description=") {
				description = strings.TrimPrefix(arg, "--description=")
			} else if strings.HasPrefix(arg, "--priority=") {
				priority = strings.TrimPrefix(arg, "--priority=")
			} else if strings.HasPrefix(arg, "--assignee=") {
				assignee = strings.TrimPrefix(arg, "--assignee=")
			} else if strings.HasPrefix(arg, "--type=") {
				taskType = strings.TrimPrefix(arg, "--type=")
			} else if strings.HasPrefix(arg, "--depends-on=") {
				depID := strings.TrimPrefix(arg, "--depends-on=")
				if depID != "" {
					dependsOn = append(dependsOn, depID)
				}
			}
		}

		if title == "" {
			fmt.Println("Error: --title is required")
			os.Exit(1)
		}

		task, err := tb.CreateTask(TaskBoard{
			Title:       title,
			Description: description,
			Priority:    priority,
			Assignee:    assignee,
			Type:        taskType,
			DependsOn:   dependsOn,
		})
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Created task: %s\n", task.ID)
		fmt.Printf("Title: %s\n", task.Title)
		fmt.Printf("Status: %s\n", task.Status)

	case "show":
		if len(args) < 1 {
			fmt.Println("Usage: tetora task show TASK_ID [--full]")
			os.Exit(1)
		}

		taskID := args[0]
		full := false
		for _, arg := range args[1:] {
			if arg == "--full" {
				full = true
			}
		}

		task, err := tb.GetTask(taskID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			normalizedID := normalizeTaskID(taskID)
			if candidates := tb.SuggestTasks(normalizedID); len(candidates) > 0 {
				fmt.Fprintln(os.Stderr, "Did you mean:")
				for _, c := range candidates {
					fmt.Fprintf(os.Stderr, "  %s  %s  (%s)\n", c.ID, c.Title, c.Status)
				}
			}
			os.Exit(1)
		}

		fmt.Printf("# %s\n\n", task.Title)
		fmt.Printf("- **ID**: %s\n", task.ID)
		fmt.Printf("- **Status**: %s\n", task.Status)
		fmt.Printf("- **Priority**: %s\n", task.Priority)
		fmt.Printf("- **Assignee**: %s\n", task.Assignee)
		fmt.Printf("- **Project**: %s\n", task.Project)
		if task.ParentID != "" {
			fmt.Printf("- **Parent**: %s\n", task.ParentID)
		}
		if len(task.DependsOn) > 0 {
			fmt.Printf("- **Depends On**: %s\n", strings.Join(task.DependsOn, ", "))
		}
		fmt.Printf("- **Created**: %s\n", task.CreatedAt)
		fmt.Printf("- **Updated**: %s\n", task.UpdatedAt)
		if task.CompletedAt != "" {
			fmt.Printf("- **Completed**: %s\n", task.CompletedAt)
		}
		if task.Description != "" {
			fmt.Printf("\n## Description\n\n%s\n", task.Description)
		}

		if full {
			comments, err := tb.GetThread(task.ID)
			if err != nil {
				fmt.Printf("\nError loading comments: %v\n", err)
			} else if len(comments) > 0 {
				fmt.Printf("\n## Comments (%d)\n\n", len(comments))
				for _, c := range comments {
					fmt.Printf("### [%s] %s (type: %s)\n\n%s\n\n", c.CreatedAt, c.Author, c.Type, c.Content)
				}
			}
		}

	case "update":
		if len(args) < 2 {
			fmt.Println("Usage: tetora task update TASK_ID [--title=TITLE] [--description=DESC] [--priority=PRIORITY]")
			os.Exit(1)
		}

		taskID := args[0]
		updates := make(map[string]any)
		var dependsOn []string
		hasDeps := false
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--title=") {
				updates["title"] = strings.TrimPrefix(arg, "--title=")
			} else if strings.HasPrefix(arg, "--description=") {
				updates["description"] = strings.TrimPrefix(arg, "--description=")
			} else if strings.HasPrefix(arg, "--priority=") {
				updates["priority"] = strings.TrimPrefix(arg, "--priority=")
			} else if strings.HasPrefix(arg, "--depends-on=") {
				depID := strings.TrimPrefix(arg, "--depends-on=")
				if depID != "" {
					dependsOn = append(dependsOn, depID)
				}
				hasDeps = true
			}
		}
		if hasDeps {
			updates["dependsOn"] = dependsOn
		}

		if len(updates) == 0 {
			fmt.Println("Error: at least one update field is required")
			os.Exit(1)
		}

		task, err := tb.UpdateTask(taskID, updates)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Updated task %s\n", task.ID)
		fmt.Printf("Title: %s\n", task.Title)
		fmt.Printf("Priority: %s\n", task.Priority)

	case "move":
		if len(args) < 2 {
			fmt.Println("Usage: tetora task move TASK_ID --status=STATUS")
			os.Exit(1)
		}

		taskID := args[0]
		var newStatus string
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--status=") {
				newStatus = strings.TrimPrefix(arg, "--status=")
			}
		}

		if newStatus == "" {
			fmt.Println("Error: --status is required")
			os.Exit(1)
		}

		task, err := tb.MoveTask(taskID, newStatus)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Moved task %s to %s\n", task.ID, task.Status)

	case "assign":
		if len(args) < 2 {
			fmt.Println("Usage: tetora task assign TASK_ID --assignee=AGENT")
			os.Exit(1)
		}

		taskID := args[0]
		var assignee string
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--assignee=") {
				assignee = strings.TrimPrefix(arg, "--assignee=")
			}
		}

		if assignee == "" {
			fmt.Println("Error: --assignee is required")
			os.Exit(1)
		}

		task, err := tb.AssignTask(taskID, assignee)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Assigned task %s to %s\n", task.ID, task.Assignee)

	case "comment":
		if len(args) < 3 {
			fmt.Println("Usage: tetora task comment TASK_ID --author=AUTHOR --content=CONTENT [--type=TYPE]")
			os.Exit(1)
		}

		taskID := args[0]
		var author, content, commentType string
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--author=") {
				author = strings.TrimPrefix(arg, "--author=")
			} else if strings.HasPrefix(arg, "--content=") {
				content = strings.TrimPrefix(arg, "--content=")
			} else if strings.HasPrefix(arg, "--type=") {
				commentType = strings.TrimPrefix(arg, "--type=")
			}
		}

		if author == "" || content == "" {
			fmt.Println("Error: --author and --content are required")
			os.Exit(1)
		}

		comment, err := tb.AddComment(taskID, author, content, commentType)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Added comment %s (type: %s) to task %s\n", comment.ID, comment.Type, taskID)

	case "thread":
		if len(args) < 1 {
			fmt.Println("Usage: tetora task thread TASK_ID")
			os.Exit(1)
		}

		taskID := args[0]
		comments, err := tb.GetThread(taskID)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if len(comments) == 0 {
			fmt.Println("No comments found")
			return
		}

		fmt.Printf("Thread for task %s (%d comments):\n\n", taskID, len(comments))
		for _, c := range comments {
			fmt.Printf("[%s] %s (type: %s):\n%s\n\n", c.CreatedAt, c.Author, c.Type, c.Content)
		}

	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		fmt.Println("Use 'tetora task' to see available commands")
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// worktree.go — thin shim over internal/worktree.
//
// Types are aliases so callers (including package main tests) see no type change.
// Unexported helper shims (buildBranchName, slugifyBranch, isGitRepo, detectDefaultBranch)
// are kept here because worktree_test.go (package main) references them directly.
// ---------------------------------------------------------------------------

// --- Type aliases ---

type WorktreeManager = worktree.WorktreeManager
type WorktreeInfo = worktree.WorktreeInfo

// --- Constructor shim ---

func NewWorktreeManager(baseDir string) *WorktreeManager {
	return worktree.NewWorktreeManager(baseDir)
}

// --- Unexported shims for package main tests and internal callers ---

func buildBranchName(cfg config.GitWorkflowConfig, t taskboard.TaskBoard) string {
	return worktree.BuildBranchName(cfg, t)
}

func slugify(s string) string {
	return worktree.Slugify(s)
}

func slugifyBranch(s string) string {
	return worktree.SlugifyBranch(s)
}

func isGitRepo(dir string) bool {
	return worktree.IsGitRepo(dir)
}

// ============================================================
// Merged from taskboard_processing.go
// ============================================================

// taskboard_processing.go — standalone functions that need root-package access
// (runSingleTask, fillDefaults, etc.). All methods on *TaskBoardDispatcher are
// now in internal/taskboard/processing.go on *Dispatcher (inherited via type alias).

// estimateTimeoutSem is a dedicated semaphore for timeout estimation LLM calls.
var estimateTimeoutSem = make(chan struct{}, 3)

// estimateTimeoutLLM uses a lightweight LLM call to estimate appropriate timeout
// for a taskboard task. Returns a duration string (e.g. "45m", "2h") or empty
// string on failure (caller should fall back to keyword-based estimation).
func estimateTimeoutLLM(ctx context.Context, cfg *Config, prompt string) string {
	estPrompt := fmt.Sprintf(`Estimate how long an AI coding agent will need to complete this task. Consider the complexity, number of files likely involved, and whether it requires research/analysis.

Task:
%s

Reply with ONLY a single integer: the estimated minutes needed. Examples:
- Simple bug fix or config change: 15
- Moderate feature or multi-file fix: 45
- Large feature, refactor, or codebase analysis: 120
- Major rewrite or multi-project task: 180

Minutes:`, truncateStr(prompt, 2000))

	task := Task{
		ID:             newUUID(),
		Name:           "timeout-estimate",
		Prompt:         estPrompt,
		Model:          "haiku",
		Budget:         0.02,
		Timeout:        "15s",
		PermissionMode: "plan",
		Source:         "timeout-estimate",
	}
	fillDefaults(cfg, &task)
	task.Model = "haiku"
	task.Budget = 0.02

	result := runSingleTask(ctx, cfg, task, estimateTimeoutSem, nil, "")
	if result.Status != "success" || result.Output == "" {
		return ""
	}

	// Parse the integer from output.
	cleaned := strings.TrimSpace(result.Output)
	// Extract first number found.
	var numStr string
	for _, ch := range cleaned {
		if ch >= '0' && ch <= '9' {
			numStr += string(ch)
		} else if numStr != "" {
			break
		}
	}
	minutes, err := strconv.Atoi(numStr)
	if err != nil || minutes < 5 || minutes > 480 {
		return ""
	}

	// Apply 1.5x buffer to avoid premature timeout.
	buffered := int(float64(minutes) * 1.5)
	if buffered < 15 {
		buffered = 15
	}

	if buffered >= 60 {
		hours := buffered / 60
		rem := buffered % 60
		if rem == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh%dm", hours, rem)
	}
	return fmt.Sprintf("%dm", buffered)
}

// =============================================================================
// Section: Backlog Triage
// =============================================================================

// triageBacklog analyzes backlog tasks and decides whether to assign, decompose, or clarify.
// Called as a special cron job (like daily_notes).
func triageBacklog(ctx context.Context, cfg *Config, sem, childSem chan struct{}) {
	if !cfg.TaskBoard.Enabled {
		return
	}

	tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)
	if err := tb.InitSchema(); err != nil {
		log.Error("triage: init schema failed", "error", err)
		return
	}

	tasks, err := tb.ListTasks("backlog", "", "")
	if err != nil {
		log.Error("triage: list backlog failed", "error", err)
		return
	}

	if len(tasks) == 0 {
		log.Debug("triage: no backlog tasks")
		return
	}

	roster := buildAgentRoster(cfg)
	if roster == "" {
		log.Warn("triage: no agents configured, skipping")
		return
	}

	// Build valid agent name set for validation.
	validAgents := make(map[string]bool, len(cfg.Agents))
	for name := range cfg.Agents {
		validAgents[name] = true
	}

	// Fast-path: promote assigned tasks with no blocking deps directly to todo.
	fastPromoted := 0
	for _, t := range tasks {
		if t.Assignee != "" && !hasBlockingDeps(tb, t) {
			if _, err := tb.MoveTask(t.ID, "todo"); err == nil {
				log.Info("triage: fast-path promote", "taskId", t.ID, "assignee", t.Assignee, "priority", t.Priority)
				tb.AddComment(t.ID, "triage", "[triage] Fast-path: already assigned, no blocking deps → todo")
				fastPromoted++
			}
		}
	}
	if fastPromoted > 0 {
		log.Info("triage: fast-path promoted tasks", "count", fastPromoted)
		// Re-fetch remaining backlog for LLM triage.
		tasks, err = tb.ListTasks("backlog", "", "")
		if err != nil {
			log.Error("triage: re-list backlog failed", "error", err)
			return
		}
		if len(tasks) == 0 {
			log.Debug("triage: all backlog tasks promoted via fast-path")
			return
		}
	}

	log.Info("triage: processing backlog", "count", len(tasks))

	for _, t := range tasks {
		if ctx.Err() != nil {
			return
		}

		comments, err := tb.GetThread(t.ID)
		if err != nil {
			log.Warn("triage: failed to get thread", "taskId", t.ID, "error", err)
			continue
		}
		if shouldSkipTriage(comments) {
			log.Debug("triage: skipping (already triaged, no new replies)", "taskId", t.ID)
			continue
		}

		result := triageOneTask(ctx, cfg, sem, childSem, tb, t, comments, roster)
		if result == nil {
			continue
		}

		applyTriageResult(tb, t, result, validAgents)
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
func triageOneTask(ctx context.Context, cfg *Config, sem, childSem chan struct{}, tb *TaskBoardEngine, t TaskBoard, comments []TaskComment, roster string) *triageResult {
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
		Model:   "sonnet",
		Budget:  0.2,
		Timeout: "30s",
		Source:  "triage",
	}
	fillDefaults(cfg, &task)
	task.Model = "sonnet" // triage needs better judgement than haiku

	result := runSingleTask(ctx, cfg, task, sem, childSem, "")
	if result.Status != "success" {
		log.Warn("triage: LLM call failed", "taskId", t.ID, "error", result.Error)
		return nil
	}

	// Parse JSON response — extract JSON object from LLM output.
	output := strings.TrimSpace(result.Output)
	output = extractJSON(output)

	var tr triageResult
	if err := json.Unmarshal([]byte(output), &tr); err != nil {
		log.Warn("triage: failed to parse LLM response", "taskId", t.ID, "output", truncate(output, 200), "error", err)
		return nil
	}

	if tr.Action != "ready" && tr.Action != "decompose" && tr.Action != "clarify" {
		log.Warn("triage: unknown action", "taskId", t.ID, "action", tr.Action)
		return nil
	}

	return &tr
}

// applyTriageResult executes the triage decision on a task.
func applyTriageResult(tb *TaskBoardEngine, t TaskBoard, tr *triageResult, validAgents map[string]bool) {
	switch tr.Action {
	case "ready":
		if tr.Assignee == "" {
			log.Warn("triage: ready but no assignee", "taskId", t.ID)
			return
		}
		if !validAgents[tr.Assignee] {
			log.Warn("triage: assignee not a configured agent", "taskId", t.ID, "assignee", tr.Assignee)
			// Add as clarify instead.
			comment := fmt.Sprintf("[triage] Could not assign: agent %q not found. Reason: %s", tr.Assignee, tr.Comment)
			if _, err := tb.AddComment(t.ID, "triage", comment); err != nil {
				log.Warn("triage: add comment failed", "taskId", t.ID, "error", err)
			}
			return
		}
		if _, err := tb.AssignTask(t.ID, tr.Assignee); err != nil {
			log.Warn("triage: assign failed", "taskId", t.ID, "error", err)
			return
		}
		if _, err := tb.MoveTask(t.ID, "todo"); err != nil {
			log.Warn("triage: move to todo failed", "taskId", t.ID, "error", err)
			return
		}
		comment := fmt.Sprintf("[triage] Assigned to %s. Reason: %s", tr.Assignee, tr.Comment)
		if _, err := tb.AddComment(t.ID, "triage", comment); err != nil {
			log.Warn("triage: add comment failed", "taskId", t.ID, "error", err)
		}
		log.Info("triage: task ready", "taskId", t.ID, "assignee", tr.Assignee)

	case "decompose":
		if len(tr.Subtasks) == 0 {
			log.Warn("triage: decompose but no subtasks", "taskId", t.ID)
			return
		}
		var created []string
		for _, sub := range tr.Subtasks {
			if sub.Title == "" {
				log.Warn("triage: skipping subtask with empty title", "taskId", t.ID)
				continue
			}
			assignee := sub.Assignee
			if !validAgents[assignee] {
				log.Warn("triage: subtask assignee not found, leaving unassigned", "taskId", t.ID, "assignee", assignee)
				assignee = ""
			}
			newTask, err := tb.CreateTask(TaskBoard{
				Title:    sub.Title,
				Status:   "todo",
				Assignee: assignee,
				Priority: t.Priority,
				Project:  t.Project,
				ParentID: t.ID,
			})
			if err != nil {
				log.Warn("triage: create subtask failed", "taskId", t.ID, "title", sub.Title, "error", err)
				continue
			}
			created = append(created, fmt.Sprintf("- %s → %s (%s)", newTask.ID, sub.Title, assignee))
		}
		// Only move parent to done if at least one subtask was created.
		if len(created) == 0 {
			log.Warn("triage: all subtasks failed to create, keeping in backlog", "taskId", t.ID)
			if _, err := tb.AddComment(t.ID, "triage", "[triage] Decompose attempted but all subtasks failed to create."); err != nil {
				log.Warn("triage: add comment failed", "taskId", t.ID, "error", err)
			}
			return
		}
		comment := fmt.Sprintf("[triage] Decomposed into %d subtasks:\n%s\n\nReason: %s",
			len(created), strings.Join(created, "\n"), tr.Comment)
		if _, err := tb.AddComment(t.ID, "triage", comment); err != nil {
			log.Warn("triage: add comment failed", "taskId", t.ID, "error", err)
		}
		if _, err := tb.MoveTask(t.ID, "todo"); err != nil {
			log.Warn("triage: move decomposed task to todo failed", "taskId", t.ID, "error", err)
		}
		log.Info("triage: task decomposed", "taskId", t.ID, "subtasks", len(created))

	case "clarify":
		if tr.Comment == "" {
			log.Warn("triage: clarify but no comment", "taskId", t.ID)
			return
		}
		comment := fmt.Sprintf("[triage] Need clarification: %s", tr.Comment)
		if _, err := tb.AddComment(t.ID, "triage", comment); err != nil {
			log.Warn("triage: add comment failed", "taskId", t.ID, "error", err)
		}
		log.Info("triage: asked for clarification", "taskId", t.ID)
	}
}

// buildAgentRoster generates a deterministic summary of available agents for the triage prompt.
func buildAgentRoster(cfg *Config) string {
	if len(cfg.Agents) == 0 {
		return ""
	}
	// Sort agent names for deterministic prompt ordering.
	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)

	var lines []string
	for _, name := range names {
		ac := cfg.Agents[name]
		line := fmt.Sprintf("- %s: %s", name, ac.Description)
		if len(ac.Keywords) > 0 {
			line += fmt.Sprintf(" (keywords: %s)", strings.Join(ac.Keywords, ", "))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// shouldSkipTriage returns true if triage has already commented and no human
// has replied since — prevents re-triaging the same task repeatedly.
func shouldSkipTriage(comments []TaskComment) bool {
	if len(comments) == 0 {
		return false // first triage
	}
	lastTriageIdx := -1
	for i := len(comments) - 1; i >= 0; i-- {
		if comments[i].Author == "triage" {
			lastTriageIdx = i
			break
		}
	}
	if lastTriageIdx == -1 {
		return false // no triage comment yet
	}
	for i := lastTriageIdx + 1; i < len(comments); i++ {
		if comments[i].Author != "triage" {
			return false // human replied after triage — re-triage
		}
	}
	return true // triage has the last word, skip
}

// --- Taskboard Dispatcher Wiring (merged from taskboard_dispatch.go) ---

func newTaskBoardDispatcher(engine *TaskBoardEngine, cfg *Config, sem, childSem chan struct{}, state *dispatchState) *TaskBoardDispatcher {
	wtBaseDir := filepath.Join(cfg.RuntimeDir, "worktrees")
	wtMgr := NewWorktreeManager(wtBaseDir)

	deps := taskboard.DispatcherDeps{
		Executor: dtypes.TaskExecutorFunc(func(ctx context.Context, task dtypes.Task, agentName string) dtypes.TaskResult {
			// Acquire semaphore slot for this task.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return dtypes.TaskResult{Status: "cancelled", Error: ctx.Err().Error()}
			}
			// Convert internal dtypes.Task to root Task and run.
			rootTask := Task(task)
			result := runSingleTask(ctx, cfg, rootTask, sem, childSem, agentName)
			return dtypes.TaskResult(result)
		}),
		ChildExecutor: dtypes.TaskExecutorFunc(func(ctx context.Context, task dtypes.Task, agentName string) dtypes.TaskResult {
			select {
			case childSem <- struct{}{}:
				defer func() { <-childSem }()
			case <-ctx.Done():
				return dtypes.TaskResult{Status: "cancelled", Error: ctx.Err().Error()}
			}
			rootTask := Task(task)
			result := runSingleTask(ctx, cfg, rootTask, childSem, nil, agentName)
			return dtypes.TaskResult(result)
		}),

		FillDefaults: func(c *config.Config, t *dtypes.Task) {
			rootTask := Task(*t)
			fillDefaults(c, &rootTask)
			*t = dtypes.Task(rootTask)
		},

		RecordHistory: func(dbPath, jobID, name, source, role string, task dtypes.Task, result dtypes.TaskResult, startedAt, finishedAt, outputFile string) {
			recordHistory(dbPath, jobID, name, source, role, Task(task), TaskResult(result), startedAt, finishedAt, outputFile)
		},

		LoadAgentPrompt: func(c *config.Config, agentName string) (string, error) {
			return loadAgentPrompt(c, agentName)
		},

		Workflows: &rootWorkflowRunner{cfg: cfg, state: state, sem: sem, childSem: childSem},

		GetProject: func(historyDB, id string) *taskboard.ProjectInfo {
			p, err := getProject(historyDB, id)
			if err != nil || p == nil {
				return nil
			}
			return &taskboard.ProjectInfo{Name: p.Name, Workdir: p.Workdir}
		},

		Skills: &rootSkillsProvider{cfg: cfg},

		Worktrees: wtMgr,

		Delegations: &rootDelegationProcessor{cfg: cfg, state: state, sem: sem, childSem: childSem},

		BuildBranch: func(gitCfg config.GitWorkflowConfig, t taskboard.TaskBoard) string {
			return buildBranchName(gitCfg, t)
		},

		NewID: newUUID,

		Truncate: func(s string, maxLen int) string {
			return truncate(s, maxLen)
		},

		TruncateToChars: func(s string, maxChars int) string {
			return truncateToChars(s, maxChars)
		},

		ExtractJSON: func(s string) string {
			return extractJSON(s)
		},

		Discord:                discordSender(state),
		DiscordNotifyChannelID: cfg.Discord.NotifyChannelID,
	}

	return taskboard.NewDispatcher(engine, cfg, deps)
}

// --- rootWorkflowRunner implements taskboard.WorkflowRunner ---

type rootWorkflowRunner struct {
	cfg      *Config
	state    *dispatchState
	sem      chan struct{}
	childSem chan struct{}
}

func (r *rootWorkflowRunner) Execute(ctx context.Context, workflowName string, vars map[string]string) (taskboard.WorkflowRunResult, error) {
	w, err := loadWorkflowByName(r.cfg, workflowName)
	if err != nil {
		return taskboard.WorkflowRunResult{}, err
	}
	run := executeWorkflow(ctx, r.cfg, w, vars, r.state, r.sem, r.childSem)
	return convertWorkflowRun(run, w), nil
}

func (r *rootWorkflowRunner) Resume(ctx context.Context, runID string) (taskboard.WorkflowRunResult, error) {
	run, err := resumeWorkflow(ctx, r.cfg, runID, r.state, r.sem, r.childSem)
	if err != nil {
		return taskboard.WorkflowRunResult{}, err
	}
	// Load the workflow definition to get step order.
	prevRun, qErr := queryWorkflowRunByID(r.cfg.HistoryDB, runID)
	if qErr != nil || prevRun == nil {
		return convertWorkflowRunNoSteps(run), nil
	}
	w, wErr := loadWorkflowByName(r.cfg, prevRun.WorkflowName)
	if wErr != nil {
		return convertWorkflowRunNoSteps(run), nil
	}
	return convertWorkflowRun(run, w), nil
}

func (r *rootWorkflowRunner) QueryRun(dbPath, id string) (taskboard.WorkflowRunInfo, error) {
	run, err := queryWorkflowRunByID(dbPath, id)
	if err != nil || run == nil {
		return taskboard.WorkflowRunInfo{}, err
	}
	return taskboard.WorkflowRunInfo{
		ID:           run.ID,
		WorkflowName: run.WorkflowName,
		Status:       run.Status,
	}, nil
}

// convertWorkflowRun maps a root WorkflowRun to the internal WorkflowRunResult.
func convertWorkflowRun(run *WorkflowRun, w *Workflow) taskboard.WorkflowRunResult {
	r := taskboard.WorkflowRunResult{
		ID:          run.ID,
		Status:      run.Status,
		TotalCost:   run.TotalCost,
		DurationMs:  run.DurationMs,
		Error:       run.Error,
		StepOutputs: make(map[string]string),
		StepErrors:  make(map[string]string),
		StepSessions: make(map[string]string),
	}
	// Build ordered step list from Workflow definition.
	for _, step := range w.Steps {
		r.StepOrder = append(r.StepOrder, step.ID)
		if sr, ok := run.StepResults[step.ID]; ok {
			if sr.Output != "" {
				r.StepOutputs[step.ID] = sr.Output
			}
			if sr.Error != "" {
				r.StepErrors[step.ID] = sr.Error
			}
			if sr.SessionID != "" {
				r.StepSessions[step.ID] = sr.SessionID
			}
		}
	}
	return r
}

// convertWorkflowRunNoSteps maps a WorkflowRun without workflow step info.
func convertWorkflowRunNoSteps(run *WorkflowRun) taskboard.WorkflowRunResult {
	r := taskboard.WorkflowRunResult{
		ID:           run.ID,
		Status:       run.Status,
		TotalCost:    run.TotalCost,
		DurationMs:   run.DurationMs,
		Error:        run.Error,
		StepOutputs:  make(map[string]string),
		StepErrors:   make(map[string]string),
		StepSessions: make(map[string]string),
	}
	for id, sr := range run.StepResults {
		r.StepOrder = append(r.StepOrder, id)
		if sr.Output != "" {
			r.StepOutputs[id] = sr.Output
		}
		if sr.Error != "" {
			r.StepErrors[id] = sr.Error
		}
		if sr.SessionID != "" {
			r.StepSessions[id] = sr.SessionID
		}
	}
	return r
}

// --- rootSkillsProvider implements taskboard.SkillsProvider ---

type rootSkillsProvider struct {
	cfg *Config
}

func (s *rootSkillsProvider) SelectSkills(task dtypes.Task) []config.SkillConfig {
	rootTask := Task(task)
	skills := selectSkills(s.cfg, rootTask)
	out := make([]config.SkillConfig, len(skills))
	for i, sk := range skills {
		out[i] = config.SkillConfig(sk)
	}
	return out
}

func (s *rootSkillsProvider) LoadFailuresByName(skillName string) string {
	return loadSkillFailuresByName(s.cfg, skillName)
}

func (s *rootSkillsProvider) AppendFailure(skillName, taskTitle, agentName, errMsg string) {
	appendSkillFailure(s.cfg, skillName, taskTitle, agentName, errMsg)
}

func (s *rootSkillsProvider) MaxInject() int {
	return skillFailuresMaxInject
}

// --- rootDelegationProcessor implements taskboard.DelegationProcessor ---

type rootDelegationProcessor struct {
	cfg      *Config
	state    *dispatchState
	sem      chan struct{}
	childSem chan struct{}
}

func (p *rootDelegationProcessor) Parse(output string) []taskboard.AutoDelegation {
	raw := parseAutoDelegate(output)
	out := make([]taskboard.AutoDelegation, len(raw))
	for i, d := range raw {
		out[i] = taskboard.AutoDelegation{Agent: d.Agent, Task: d.Task, Reason: d.Reason}
	}
	return out
}

func (p *rootDelegationProcessor) Process(ctx context.Context, delegations []taskboard.AutoDelegation, output, fromAgent string) {
	raw := make([]AutoDelegation, len(delegations))
	for i, d := range delegations {
		raw[i] = AutoDelegation{Agent: d.Agent, Task: d.Task, Reason: d.Reason}
	}
	processAutoDelegations(ctx, p.cfg, raw, output, "", fromAgent, "", p.state, p.sem, p.childSem, nil)
}

// --- discordSender extracts the DiscordEmbedSender from dispatchState ---

// discordBotAdapter wraps *DiscordBot to satisfy taskboard.DiscordEmbedSender
// (exported method SendEmbed delegates to unexported sendEmbed).
type discordBotAdapter struct {
	bot *DiscordBot
}

func (a *discordBotAdapter) SendEmbed(channelID string, embed discord.Embed) {
	a.bot.sendEmbed(channelID, embed)
}

func discordSender(state *dispatchState) taskboard.DiscordEmbedSender {
	if state == nil || state.discordBot == nil {
		return nil
	}
	return &discordBotAdapter{bot: state.discordBot}
}
