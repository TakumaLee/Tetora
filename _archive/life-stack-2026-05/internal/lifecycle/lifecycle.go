package lifecycle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/automation/insights"
	"tetora/internal/life/contacts"
	"tetora/internal/life/goals"
	"tetora/internal/life/reminder"
	"tetora/internal/log"
)

// Config holds the configuration subset needed by the lifecycle engine.
type Config struct {
	NotesEnabled bool
	VaultPath    string // already resolved
	Lifecycle    LifecycleConfig
}

// LifecycleConfig mirrors config.LifecycleConfig.
type LifecycleConfig struct {
	AutoHabitSuggest   bool
	AutoInsightAction  bool
	AutoBirthdayRemind bool
}

// Engine connects modules into closed-loop automation.
type Engine struct {
	cfg      Config
	insights *insights.Engine
	contacts *contacts.Service
	goals    *goals.Service
	reminder *reminder.Engine
}

// New creates a new Engine with the given dependencies. Any dependency may be nil.
func New(cfg Config, ins *insights.Engine, cs *contacts.Service, gs *goals.Service, re *reminder.Engine) *Engine {
	return &Engine{
		cfg:      cfg,
		insights: ins,
		contacts: cs,
		goals:    gs,
		reminder: re,
	}
}

// habitSuggestions maps goal category keywords to habit recommendations.
var habitSuggestions = map[string][]string{
	"fitness":      {"Exercise 30 min daily", "Track calories", "Stretch every morning"},
	"health":       {"Drink 8 glasses of water", "Sleep 7+ hours", "Meditate 10 min"},
	"learning":     {"Read 30 min daily", "Practice flashcards", "Write summary notes"},
	"finance":      {"Review expenses weekly", "Save 10% of income", "Check investments monthly"},
	"career":       {"Network weekly", "Learn new skill monthly", "Update portfolio quarterly"},
	"writing":      {"Write 500 words daily", "Journal before bed", "Read in your genre"},
	"coding":       {"Solve one problem daily", "Review code weekly", "Read technical articles"},
	"social":       {"Reach out to one friend weekly", "Plan monthly gatherings", "Send gratitude messages"},
	"mindfulness":  {"Meditate daily", "Practice gratitude", "Digital detox 1hr/day"},
}

// SuggestHabitForGoal returns habit suggestions based on goal title and category.
func (e *Engine) SuggestHabitForGoal(title, category string) []string {
	lower := strings.ToLower(title + " " + category)

	var suggestions []string
	for keyword, habits := range habitSuggestions {
		if strings.Contains(lower, keyword) {
			suggestions = append(suggestions, habits...)
		}
	}

	if len(suggestions) == 0 {
		suggestions = []string{
			"Review progress weekly",
			"Set daily micro-goals",
			"Reflect on blockers",
		}
	}

	if len(suggestions) > 3 {
		suggestions = suggestions[:3]
	}
	return suggestions
}

// RunInsightActions detects anomalies and creates reminders/notifications.
func (e *Engine) RunInsightActions() ([]string, error) {
	var actions []string

	if e.insights != nil {
		insightList, err := e.insights.DetectAnomalies(7)
		if err != nil {
			log.Warn("lifecycle: detect anomalies failed", "error", err)
		} else {
			for _, insight := range insightList {
				if insight.Severity == "high" || insight.Severity == "critical" {
					if e.reminder != nil {
						due := time.Now().Add(24 * time.Hour)
						text := fmt.Sprintf("[Insight] %s: %s", insight.Title, insight.Description)
						_, err := e.reminder.Add(text, due, "", "", "default")
						if err != nil {
							log.Warn("lifecycle: create insight reminder failed", "error", err)
						} else {
							actions = append(actions, fmt.Sprintf("Reminder created for insight: %s", insight.Title))
						}
					}
				}
			}
		}
	}

	if e.contacts != nil {
		inactive, err := e.contacts.GetInactiveContacts(30)
		if err != nil {
			log.Warn("lifecycle: get inactive contacts failed", "error", err)
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

	log.Info("lifecycle: insight actions completed", "actions", len(actions))
	return actions, nil
}

// SyncBirthdayReminders creates annual reminders for contact birthdays.
func (e *Engine) SyncBirthdayReminders() (int, error) {
	if e.contacts == nil {
		return 0, fmt.Errorf("contacts service not initialized")
	}
	if e.reminder == nil {
		return 0, fmt.Errorf("reminder engine not initialized")
	}

	events, err := e.contacts.GetUpcomingEvents(365)
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

		if daysUntil > 7 {
			continue
		}

		reminderText := fmt.Sprintf("🎂 %s's birthday is in %d day(s) (%s)", contactName, daysUntil, dateStr)
		due := time.Now().Add(time.Duration(daysUntil-1) * 24 * time.Hour)
		if daysUntil <= 1 {
			due = time.Now().Add(1 * time.Hour)
		}

		_, err := e.reminder.Add(reminderText, due, "", "", "default")
		if err != nil {
			log.Debug("lifecycle: birthday reminder skipped", "contact", contactName, "error", err)
			continue
		}
		created++
	}

	log.Info("lifecycle: birthday reminders synced", "created", created, "events", len(events))
	return created, nil
}

// OnGoalCompleted logs a celebration note when a goal is completed.
func (e *Engine) OnGoalCompleted(goalID string) error {
	if e.goals == nil {
		return fmt.Errorf("goals service not initialized")
	}

	goal, err := e.goals.GetGoal(goalID)
	if err != nil {
		return fmt.Errorf("get goal: %w", err)
	}

	if e.cfg.NotesEnabled && e.cfg.VaultPath != "" {
		os.MkdirAll(e.cfg.VaultPath, 0o755)
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

		notePath := filepath.Join(e.cfg.VaultPath, filename)
		if err := os.WriteFile(notePath, []byte(content), 0o644); err != nil {
			log.Warn("lifecycle: write celebration note failed", "error", err)
		} else {
			log.Info("lifecycle: celebration note written", "path", notePath)
		}
	}

	return nil
}

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	default:
		return fmt.Sprintf("%v", v)
	}
}

func jsonInt(v any) int {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}
