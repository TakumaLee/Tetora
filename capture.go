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

// --- P29.1: Quick Capture ---

// classifyCapture determines the category of a free-form text input.
func classifyCapture(input string) string {
	lower := strings.ToLower(input)

	// Check expense keywords
	if strings.Contains(lower, "$") || strings.Contains(lower, "spent") ||
		strings.Contains(lower, "paid") || strings.Contains(lower, "bought") ||
		strings.Contains(lower, "cost") || strings.Contains(lower, "元") ||
		strings.Contains(lower, "円") {
		return "expense"
	}
	// Check reminder keywords
	if strings.Contains(lower, "remind") || strings.Contains(lower, "deadline") ||
		strings.Contains(lower, "don't forget") || strings.Contains(lower, "dont forget") {
		return "reminder"
	}
	// Check contact keywords
	if strings.Contains(lower, "phone") || strings.Contains(lower, "email") ||
		strings.Contains(lower, "birthday") || strings.Contains(input, "@") {
		return "contact"
	}
	// Check task keywords
	if strings.Contains(lower, "todo") || strings.Contains(lower, "need to") ||
		strings.Contains(lower, "must") || strings.Contains(lower, "should") ||
		strings.Contains(lower, "fix") {
		return "task"
	}
	// Check idea keywords
	if strings.HasPrefix(lower, "idea:") || strings.Contains(lower, "what if") {
		return "idea"
	}
	return "note"
}

// executeCapture routes captured text to the appropriate service.
func executeCapture(ctx context.Context, cfg *Config, category, text string) (string, error) {
	switch category {
	case "task":
		if globalTaskManager == nil {
			return "", fmt.Errorf("task manager not initialized")
		}
		task, err := globalTaskManager.CreateTask(UserTask{
			Title:  text,
			Status: "todo",
		})
		if err != nil {
			return "", fmt.Errorf("create task: %w", err)
		}
		return fmt.Sprintf("Task created: %s (id=%s)", task.Title, task.ID), nil

	case "expense":
		// Reuse the expense tool handler which does NL parsing.
		input, _ := json.Marshal(map[string]string{"text": text})
		return toolExpenseAdd(ctx, cfg, input)

	case "reminder":
		if globalReminderEngine == nil {
			return "", fmt.Errorf("reminder engine not initialized")
		}
		due := time.Now().Add(24 * time.Hour)
		r, err := globalReminderEngine.addReminder(text, due, "", "", "default")
		if err != nil {
			return "", fmt.Errorf("add reminder: %w", err)
		}
		return fmt.Sprintf("Reminder set: %s (due=%s)", r.Text, r.DueAt), nil

	case "contact":
		if globalContactsService == nil {
			return "", fmt.Errorf("contacts service not initialized")
		}
		c, err := globalContactsService.AddContact(text, nil)
		if err != nil {
			return "", fmt.Errorf("add contact: %w", err)
		}
		return fmt.Sprintf("Contact added: %s (id=%s)", c.Name, c.ID), nil

	case "note", "idea":
		// Write to notes vault.
		if !cfg.Notes.Enabled {
			return "", fmt.Errorf("notes not enabled in config")
		}
		vaultPath := cfg.Notes.vaultPathResolved(cfg.baseDir)
		prefix := "note"
		if category == "idea" {
			prefix = "idea"
		}
		filename := fmt.Sprintf("%s-%s.md", prefix, time.Now().Format("20060102-150405"))
		notePath := filepath.Join(vaultPath, filename)
		os.MkdirAll(vaultPath, 0o755)
		if err := os.WriteFile(notePath, []byte(text+"\n"), 0o644); err != nil {
			return "", fmt.Errorf("write note: %w", err)
		}
		return fmt.Sprintf("Note saved: %s", notePath), nil

	default:
		return "", fmt.Errorf("unknown capture category: %s", category)
	}
}

// toolQuickCapture is the tool handler for quick_capture.
func toolQuickCapture(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Text     string `json:"text"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	category := args.Category
	if category == "" {
		category = classifyCapture(args.Text)
	}

	result, err := executeCapture(ctx, cfg, category, args.Text)
	if err != nil {
		return "", err
	}

	out := map[string]string{
		"category": category,
		"result":   result,
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}
