package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"tetora/internal/life/goals"
)

// LifecycleHook provides optional lifecycle integration for goal operations.
type LifecycleHook interface {
	SuggestHabitForGoal(title, category string) []string
	OnGoalCompleted(goalID string) error
}

// GoalCreate handles the goal_create tool.
func GoalCreate(svc *goals.Service, uuidFn func() string, lifecycle LifecycleHook, autoHabitSuggest bool, input json.RawMessage) (string, error) {
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

	id := uuidFn()
	goal, err := svc.CreateGoal(id, args.UserID, args.Title, args.Description, args.Category, args.TargetDate, uuidFn)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(goal, "", "  ")
	result := string(out)

	// P29.0: Suggest habits for the new goal.
	if lifecycle != nil && autoHabitSuggest {
		suggestions := lifecycle.SuggestHabitForGoal(args.Title, args.Category)
		if len(suggestions) > 0 {
			result += "\n\nSuggested habits: " + strings.Join(suggestions, ", ")
		}
	}

	return result, nil
}

// GoalList handles the goal_list tool.
func GoalList(svc *goals.Service, input json.RawMessage) (string, error) {
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

	goals, err := svc.ListGoals(args.UserID, args.Status, args.Limit)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(goals, "", "  ")
	return string(out), nil
}

// GoalUpdate handles the goal_update tool.
func GoalUpdate(svc *goals.Service, uuidFn func() string, lifecycle LifecycleHook, logFn func(string, ...any), input json.RawMessage) (string, error) {
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
		if err := svc.CompleteMilestone(args.ID, args.MilestoneID); err != nil {
			return "", err
		}
		goal, err := svc.GetGoal(args.ID)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(goal, "", "  ")
		return fmt.Sprintf("Milestone completed. Progress: %d%%\n%s", goal.Progress, string(out)), nil

	case "add_milestone":
		if args.Title == "" {
			return "", fmt.Errorf("title is required for add_milestone")
		}
		milestoneID := uuidFn()
		goal, err := svc.AddMilestone(args.ID, milestoneID, args.Title, args.DueDate)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(goal, "", "  ")
		return fmt.Sprintf("Milestone added.\n%s", string(out)), nil

	case "review":
		if args.Note == "" {
			return "", fmt.Errorf("note is required for review")
		}
		if err := svc.ReviewGoal(args.ID, args.Note); err != nil {
			return "", err
		}
		goal, err := svc.GetGoal(args.ID)
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
		goal, err := svc.UpdateGoal(args.ID, fields)
		if err != nil {
			return "", err
		}

		// P29.0: Trigger celebration on goal completion.
		if args.Status == "completed" && lifecycle != nil {
			if err := lifecycle.OnGoalCompleted(args.ID); err != nil {
				logFn("lifecycle: goal completion hook failed", "error", err)
			}
		}

		out, _ := json.MarshalIndent(goal, "", "  ")
		return fmt.Sprintf("Goal updated.\n%s", string(out)), nil
	}
}

// GoalReview handles the goal_review tool (weekly review summary).
func GoalReview(svc *goals.Service, input json.RawMessage) (string, error) {
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

	staleGoals, err := svc.GetStaleGoals(args.UserID, args.StaleDays)
	if err != nil {
		return "", err
	}

	summary, err := svc.GoalSummary(args.UserID)
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
