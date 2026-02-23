package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// --- P29.0: Closed-Loop Automation (Lifecycle Engine) ---

// LifecycleConfig holds configuration for cross-module automation.
type LifecycleConfig struct {
	Enabled            bool `json:"enabled"`
	AutoHabitSuggest   bool `json:"autoHabitSuggest,omitempty"`
	AutoInsightAction  bool `json:"autoInsightAction,omitempty"`
	AutoBirthdayRemind bool `json:"autoBirthdayRemind,omitempty"`
}

// LifecycleEngine connects modules into closed-loop automation.
type LifecycleEngine struct {
	cfg *Config
}

// globalLifecycleEngine is the singleton lifecycle engine.
var globalLifecycleEngine *LifecycleEngine

// newLifecycleEngine creates a new LifecycleEngine.
func newLifecycleEngine(cfg *Config) *LifecycleEngine {
	return &LifecycleEngine{cfg: cfg}
}

// --- Habit Suggestions for Goals ---

// habitSuggestions maps goal category keywords to habit recommendations.
var habitSuggestions = map[string][]string{
	"fitness":    {"Exercise 30 min daily", "Track calories", "Stretch every morning"},
	"health":     {"Drink 8 glasses of water", "Sleep 7+ hours", "Meditate 10 min"},
	"learning":   {"Read 30 min daily", "Practice flashcards", "Write summary notes"},
	"finance":    {"Review expenses weekly", "Save 10% of income", "Check investments monthly"},
	"career":     {"Network weekly", "Learn new skill monthly", "Update portfolio quarterly"},
	"writing":    {"Write 500 words daily", "Journal before bed", "Read in your genre"},
	"coding":     {"Solve one problem daily", "Review code weekly", "Read technical articles"},
	"social":     {"Reach out to one friend weekly", "Plan monthly gatherings", "Send gratitude messages"},
	"mindfulness": {"Meditate daily", "Practice gratitude", "Digital detox 1hr/day"},
}

// SuggestHabitForGoal returns habit suggestions based on goal title and category.
func (le *LifecycleEngine) SuggestHabitForGoal(title, category string) []string {
	lower := strings.ToLower(title + " " + category)

	var suggestions []string
	for keyword, habits := range habitSuggestions {
		if strings.Contains(lower, keyword) {
			suggestions = append(suggestions, habits...)
		}
	}

	// Generic suggestions if nothing matched.
	if len(suggestions) == 0 {
		suggestions = []string{
			"Review progress weekly",
			"Set daily micro-goals",
			"Reflect on blockers",
		}
	}

	// Limit to 3 suggestions.
	if len(suggestions) > 3 {
		suggestions = suggestions[:3]
	}
	return suggestions
}

// --- Insight-Driven Actions ---

// RunInsightActions detects anomalies and creates reminders/notifications.
func (le *LifecycleEngine) RunInsightActions() ([]string, error) {
	var actions []string

	// Detect anomalies via insights engine.
	if globalInsightsEngine != nil {
		insights, err := globalInsightsEngine.DetectAnomalies(7)
		if err != nil {
			logWarn("lifecycle: detect anomalies failed", "error", err)
		} else {
			for _, insight := range insights {
				if insight.Severity == "high" || insight.Severity == "critical" {
					// Create a reminder for high-severity insights.
					if globalReminderEngine != nil {
						due := time.Now().Add(24 * time.Hour)
						text := fmt.Sprintf("[Insight] %s: %s", insight.Title, insight.Description)
						_, err := globalReminderEngine.addReminder(text, due, "", "", "default")
						if err != nil {
							logWarn("lifecycle: create insight reminder failed", "error", err)
						} else {
							actions = append(actions, fmt.Sprintf("Reminder created for insight: %s", insight.Title))
						}
					}
				}
			}
		}
	}

	// Check for inactive contacts.
	if globalContactsService != nil {
		inactive, err := globalContactsService.GetInactiveContacts(30)
		if err != nil {
			logWarn("lifecycle: get inactive contacts failed", "error", err)
		} else if len(inactive) > 0 {
			names := make([]string, 0, 3)
			for i, c := range inactive {
				if i >= 3 {
					break
				}
				names = append(names, c.Name)
			}
			actions = append(actions, fmt.Sprintf("Inactive contacts (%d): %s", len(inactive), strings.Join(names, ", ")))
		}
	}

	logInfo("lifecycle: insight actions completed", "actions", len(actions))
	return actions, nil
}

// --- Birthday Reminder Sync ---

// SyncBirthdayReminders creates annual reminders for contact birthdays.
func (le *LifecycleEngine) SyncBirthdayReminders() (int, error) {
	if globalContactsService == nil {
		return 0, fmt.Errorf("contacts service not initialized")
	}
	if globalReminderEngine == nil {
		return 0, fmt.Errorf("reminder engine not initialized")
	}

	events, err := globalContactsService.GetUpcomingEvents(365)
	if err != nil {
		return 0, fmt.Errorf("get upcoming events: %w", err)
	}

	created := 0
	for _, event := range events {
		eventType := jsonStr(event["event_type"])
		if eventType != "birthday" {
			continue
		}

		contactName := jsonStr(event["contact_name"])
		dateStr := jsonStr(event["date"])
		daysUntil := jsonInt(event["days_until"])

		// Skip if birthday is more than 7 days away (we'll re-sync later).
		if daysUntil > 7 {
			continue
		}

		reminderText := fmt.Sprintf("🎂 %s's birthday is in %d day(s) (%s)", contactName, daysUntil, dateStr)
		due := time.Now().Add(time.Duration(daysUntil-1) * 24 * time.Hour)
		if daysUntil <= 1 {
			due = time.Now().Add(1 * time.Hour)
		}

		_, err := globalReminderEngine.addReminder(reminderText, due, "", "", "default")
		if err != nil {
			// Likely hit per-user limit; skip silently.
			logDebug("lifecycle: birthday reminder skipped", "contact", contactName, "error", err)
			continue
		}
		created++
	}

	logInfo("lifecycle: birthday reminders synced", "created", created, "events", len(events))
	return created, nil
}

// --- Goal Completion Celebration ---

// OnGoalCompleted logs a celebration note when a goal is completed.
func (le *LifecycleEngine) OnGoalCompleted(goalID string) error {
	if globalGoalsService == nil {
		return fmt.Errorf("goals service not initialized")
	}

	goal, err := globalGoalsService.GetGoal(goalID)
	if err != nil {
		return fmt.Errorf("get goal: %w", err)
	}

	// Write celebration note to vault.
	if le.cfg.Notes.Enabled {
		vaultPath := le.cfg.Notes.vaultPathResolved(le.cfg.baseDir)
		os.MkdirAll(vaultPath, 0o755)
		filename := fmt.Sprintf("goal-completed-%s.md", time.Now().Format("20060102-150405"))
		content := fmt.Sprintf("# 🎉 Goal Completed: %s\n\n"+
			"**Category**: %s\n"+
			"**Target Date**: %s\n"+
			"**Completed**: %s\n\n"+
			"## Milestones\n",
			goal.Title, goal.Category, goal.TargetDate, time.Now().Format("2006-01-02"))

		for _, m := range goal.Milestones {
			check := "[ ]"
			if m.Done {
				check = "[x]"
			}
			content += fmt.Sprintf("- %s %s\n", check, m.Title)
		}

		notePath := filepath.Join(vaultPath, filename)
		if err := os.WriteFile(notePath, []byte(content), 0o644); err != nil {
			logWarn("lifecycle: write celebration note failed", "error", err)
		} else {
			logInfo("lifecycle: celebration note written", "path", notePath)
		}
	}

	return nil
}

// --- Tool Handlers ---

// toolLifecycleSync handles the lifecycle_sync tool.
func toolLifecycleSync(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalLifecycleEngine == nil {
		return "", fmt.Errorf("lifecycle engine not initialized")
	}

	var args struct {
		Action string `json:"action"` // "birthdays", "insights", "all"
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Action == "" {
		args.Action = "all"
	}

	result := map[string]any{}

	switch args.Action {
	case "birthdays":
		n, err := globalLifecycleEngine.SyncBirthdayReminders()
		if err != nil {
			return "", err
		}
		result["birthdays_synced"] = n

	case "insights":
		actions, err := globalLifecycleEngine.RunInsightActions()
		if err != nil {
			return "", err
		}
		result["insight_actions"] = actions

	case "all":
		if cfg.Lifecycle.AutoBirthdayRemind {
			n, err := globalLifecycleEngine.SyncBirthdayReminders()
			if err != nil {
				result["birthday_error"] = err.Error()
			} else {
				result["birthdays_synced"] = n
			}
		}
		if cfg.Lifecycle.AutoInsightAction {
			actions, err := globalLifecycleEngine.RunInsightActions()
			if err != nil {
				result["insight_error"] = err.Error()
			} else {
				result["insight_actions"] = actions
			}
		}

	default:
		return "", fmt.Errorf("unknown action: %s (use birthdays, insights, or all)", args.Action)
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	return string(out), nil
}

// toolLifecycleSuggest handles the lifecycle_suggest tool.
func toolLifecycleSuggest(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalLifecycleEngine == nil {
		return "", fmt.Errorf("lifecycle engine not initialized")
	}

	var args struct {
		GoalTitle    string `json:"goal_title"`
		GoalCategory string `json:"goal_category"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.GoalTitle == "" {
		return "", fmt.Errorf("goal_title is required")
	}

	suggestions := globalLifecycleEngine.SuggestHabitForGoal(args.GoalTitle, args.GoalCategory)
	result := map[string]any{
		"goal_title":  args.GoalTitle,
		"suggestions": suggestions,
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	return string(out), nil
}
