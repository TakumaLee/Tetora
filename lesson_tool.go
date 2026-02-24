package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// toolStoreLesson handles the store_lesson tool.
// It stores a lesson learned into the notes vault and optionally into tasks/lessons.md.
func toolStoreLesson(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Category string   `json:"category"`
		Lesson   string   `json:"lesson"`
		Source   string   `json:"source"`
		Tags     []string `json:"tags"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Category == "" {
		return "", fmt.Errorf("category is required")
	}
	if args.Lesson == "" {
		return "", fmt.Errorf("lesson is required")
	}

	category := sanitizeLessonCategory(args.Category)
	noteName := "lessons/" + category

	// 1. Write to vault via NotesService (triggers AutoEmbed + index rebuild).
	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	now := time.Now().Format("2006-01-02 15:04")
	var entry strings.Builder
	entry.WriteString(fmt.Sprintf("\n## %s\n", now))
	entry.WriteString(fmt.Sprintf("- %s\n", args.Lesson))
	if args.Source != "" {
		entry.WriteString(fmt.Sprintf("- Source: %s\n", args.Source))
	}
	if len(args.Tags) > 0 {
		entry.WriteString(fmt.Sprintf("- Tags: %s\n", strings.Join(args.Tags, ", ")))
	}

	if err := svc.AppendNote(noteName, entry.String()); err != nil {
		return "", fmt.Errorf("append to vault: %w", err)
	}

	// 2. Append to tasks/lessons.md under matching section (best-effort).
	lessonsFile := "tasks/lessons.md"
	if _, err := os.Stat(lessonsFile); err == nil {
		sectionHeader := "## " + args.Category
		line := fmt.Sprintf("- %s", args.Lesson)
		if err := appendToLessonSection(lessonsFile, sectionHeader, line); err != nil {
			logWarn("append to lessons.md failed", "error", err)
		}
	}

	// 3. Record analytics event.
	if cfg.HistoryDB != "" {
		recordSkillEvent(cfg.HistoryDB, category, "lesson", args.Lesson, args.Source)
	}

	logInfoCtx(ctx, "lesson stored", "category", category, "tags", args.Tags)

	result := map[string]any{
		"status":   "stored",
		"category": category,
		"vault":    noteName,
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// sanitizeLessonCategory normalizes a category name to alphanumeric + hyphens.
func sanitizeLessonCategory(cat string) string {
	cat = strings.ToLower(strings.TrimSpace(cat))
	re := regexp.MustCompile(`[^a-z0-9-]+`)
	cat = re.ReplaceAllString(cat, "-")
	cat = strings.Trim(cat, "-")
	if cat == "" {
		cat = "general"
	}
	return cat
}

// appendToLessonSection appends a line under a markdown ## section in a file.
// If the section doesn't exist, it appends a new section at the end.
func appendToLessonSection(filePath, sectionHeader, content string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	var result []string
	inserted := false

	for i, line := range lines {
		result = append(result, line)
		if strings.TrimSpace(line) == sectionHeader {
			// Find the end of this section's content (next ## or EOF).
			j := i + 1
			for j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
				j++
			}
			// Insert before the next section or at the last non-empty line.
			insertIdx := j
			for insertIdx > i+1 && strings.TrimSpace(lines[insertIdx-1]) == "" {
				insertIdx--
			}
			// We've already appended line i; now append lines i+1..insertIdx-1,
			// then insert content, then continue with the rest.
			for k := i + 1; k < insertIdx; k++ {
				result = append(result, lines[k])
			}
			result = append(result, content)
			// Append remaining lines from insertIdx onward.
			for k := insertIdx; k < len(lines); k++ {
				result = append(result, lines[k])
			}
			inserted = true
			break
		}
	}

	if !inserted {
		// Section doesn't exist — append new section at end.
		result = append(result, "", sectionHeader, content)
	}

	return os.WriteFile(filePath, []byte(strings.Join(result, "\n")), 0o644)
}
