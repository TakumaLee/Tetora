package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// --- P24.6: Goal Planning & Autonomy ---
// Service struct, types, and method implementations are in internal/life/goals/.
// This file keeps tool handlers and the global singleton.

// globalGoalsService is the singleton goals service.
var globalGoalsService *GoalsService

// --- Tool Handlers ---

// toolGoalCreate handles the goal_create tool.
func toolGoalCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	var args struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Category    string `json:"category"`
		TargetDate  string `json:"target_date"`
		UserID      string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Title == "" {
		return "", fmt.Errorf("title is required")
	}
	if args.UserID == "" {
		args.UserID = "default"
	}

	id := newUUID()
	goal, err := app.Goals.CreateGoal(id, args.UserID, args.Title, args.Description, args.Category, args.TargetDate, newUUID)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(goal, "", "  ")
	result := string(out)

	// P29.0: Suggest habits for the new goal.
	if app.Lifecycle != nil && cfg.Lifecycle.AutoHabitSuggest {
		suggestions := app.Lifecycle.SuggestHabitForGoal(args.Title, args.Category)
		if len(suggestions) > 0 {
			result += "\n\nSuggested habits: " + strings.Join(suggestions, ", ")
		}
	}

	return result, nil
}

// toolGoalList handles the goal_list tool.
func toolGoalList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	var args struct {
		UserID string `json:"user_id"`
		Status string `json:"status"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}

	goals, err := app.Goals.ListGoals(args.UserID, args.Status, args.Limit)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(goals, "", "  ")
	return string(out), nil
}

// toolGoalUpdate handles the goal_update tool.
func toolGoalUpdate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	var args struct {
		ID          string `json:"id"`
		Action      string `json:"action"`
		MilestoneID string `json:"milestone_id"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Category    string `json:"category"`
		TargetDate  string `json:"target_date"`
		Status      string `json:"status"`
		Progress    *int   `json:"progress"`
		Note        string `json:"note"`
		DueDate     string `json:"due_date"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.ID == "" {
		return "", fmt.Errorf("id is required")
	}
	if args.Action == "" {
		args.Action = "update"
	}

	switch args.Action {
	case "complete_milestone":
		if args.MilestoneID == "" {
			return "", fmt.Errorf("milestone_id is required for complete_milestone")
		}
		if err := app.Goals.CompleteMilestone(args.ID, args.MilestoneID); err != nil {
			return "", err
		}
		goal, err := app.Goals.GetGoal(args.ID)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(goal, "", "  ")
		return fmt.Sprintf("Milestone completed. Progress: %d%%\n%s", goal.Progress, string(out)), nil

	case "add_milestone":
		if args.Title == "" {
			return "", fmt.Errorf("title is required for add_milestone")
		}
		milestoneID := newUUID()
		goal, err := app.Goals.AddMilestone(args.ID, milestoneID, args.Title, args.DueDate)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(goal, "", "  ")
		return fmt.Sprintf("Milestone added.\n%s", string(out)), nil

	case "review":
		if args.Note == "" {
			return "", fmt.Errorf("note is required for review")
		}
		if err := app.Goals.ReviewGoal(args.ID, args.Note); err != nil {
			return "", err
		}
		goal, err := app.Goals.GetGoal(args.ID)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(goal, "", "  ")
		return fmt.Sprintf("Review added.\n%s", string(out)), nil

	default: // "update"
		fields := map[string]any{}
		if args.Title != "" {
			fields["title"] = args.Title
		}
		if args.Description != "" {
			fields["description"] = args.Description
		}
		if args.Category != "" {
			fields["category"] = args.Category
		}
		if args.TargetDate != "" {
			fields["target_date"] = args.TargetDate
		}
		if args.Status != "" {
			fields["status"] = args.Status
		}
		if args.Progress != nil {
			fields["progress"] = *args.Progress
		}
		goal, err := app.Goals.UpdateGoal(args.ID, fields)
		if err != nil {
			return "", err
		}

		// P29.0: Trigger celebration on goal completion.
		if args.Status == "completed" && app.Lifecycle != nil {
			if err := app.Lifecycle.OnGoalCompleted(args.ID); err != nil {
				logWarn("lifecycle: goal completion hook failed", "error", err)
			}
		}

		out, _ := json.MarshalIndent(goal, "", "  ")
		return fmt.Sprintf("Goal updated.\n%s", string(out)), nil
	}
}

// toolGoalReview handles the goal_review tool (weekly review summary).
func toolGoalReview(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	var args struct {
		UserID    string `json:"user_id"`
		StaleDays int    `json:"stale_days"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}
	if args.StaleDays <= 0 {
		args.StaleDays = 14
	}

	staleGoals, err := app.Goals.GetStaleGoals(args.UserID, args.StaleDays)
	if err != nil {
		return "", err
	}

	summary, err := app.Goals.GoalSummary(args.UserID)
	if err != nil {
		return "", err
	}

	result := map[string]any{
		"summary":     summary,
		"stale_goals": staleGoals,
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	return string(out), nil
}
