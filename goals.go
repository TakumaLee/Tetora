package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// --- P24.6: Goal Planning & Autonomy ---

// Goal represents a long-term goal with milestone decomposition.
type Goal struct {
	ID          string       `json:"id"`
	UserID      string       `json:"user_id"`
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Category    string       `json:"category,omitempty"`
	TargetDate  string       `json:"target_date,omitempty"`
	Status      string       `json:"status"`
	Progress    int          `json:"progress"`
	Milestones  []Milestone  `json:"milestones,omitempty"`
	ReviewNotes []ReviewNote `json:"review_notes,omitempty"`
	CreatedAt   string       `json:"created_at"`
	UpdatedAt   string       `json:"updated_at"`
}

// Milestone represents a sub-step within a goal.
type Milestone struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Done    bool   `json:"done"`
	DueDate string `json:"due_date,omitempty"`
}

// ReviewNote records a periodic review observation on a goal.
type ReviewNote struct {
	Date string `json:"date"`
	Note string `json:"note"`
}

// GoalsService provides goal planning and tracking operations.
type GoalsService struct {
	dbPath string
	cfg    *Config
}

// globalGoalsService is the singleton goals service.
var globalGoalsService *GoalsService

// --- DB Initialization ---

// initGoalsDB creates the goals table and indexes.
func initGoalsDB(dbPath string) error {
	ddl := `
CREATE TABLE IF NOT EXISTS goals (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL DEFAULT 'default',
    title TEXT NOT NULL,
    description TEXT DEFAULT '',
    category TEXT DEFAULT '',
    target_date TEXT DEFAULT '',
    status TEXT DEFAULT 'active',
    progress INTEGER DEFAULT 0,
    milestones TEXT DEFAULT '[]',
    review_notes TEXT DEFAULT '[]',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_goals_user ON goals(user_id);
CREATE INDEX IF NOT EXISTS idx_goals_status ON goals(status);
`
	cmd := exec.Command("sqlite3", dbPath, ddl)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init goals tables: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// newGoalsService creates a new GoalsService.
func newGoalsService(cfg *Config) *GoalsService {
	return &GoalsService{
		dbPath: cfg.HistoryDB,
		cfg:    cfg,
	}
}

// --- Goal CRUD ---

// CreateGoal creates a new goal with auto-generated milestones.
func (svc *GoalsService) CreateGoal(userID, title, description, category, targetDate string) (*Goal, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("goal title is required")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	id := newUUID()

	if userID == "" {
		userID = "default"
	}

	milestones := parseMilestonesFromDescription(description)

	milestonesJSON, err := json.Marshal(milestones)
	if err != nil {
		return nil, fmt.Errorf("marshal milestones: %w", err)
	}

	reviewNotes := []ReviewNote{}
	reviewNotesJSON, _ := json.Marshal(reviewNotes)

	sql := fmt.Sprintf(`INSERT INTO goals (id, user_id, title, description, category, target_date, status, progress, milestones, review_notes, created_at, updated_at)
VALUES ('%s','%s','%s','%s','%s','%s','active',0,'%s','%s','%s','%s');`,
		escapeSQLite(id),
		escapeSQLite(userID),
		escapeSQLite(title),
		escapeSQLite(description),
		escapeSQLite(category),
		escapeSQLite(targetDate),
		escapeSQLite(string(milestonesJSON)),
		escapeSQLite(string(reviewNotesJSON)),
		escapeSQLite(now),
		escapeSQLite(now),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("create goal: %s: %w", strings.TrimSpace(string(out)), err)
	}

	goal := &Goal{
		ID:          id,
		UserID:      userID,
		Title:       title,
		Description: description,
		Category:    category,
		TargetDate:  targetDate,
		Status:      "active",
		Progress:    0,
		Milestones:  milestones,
		ReviewNotes: reviewNotes,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	logInfo("goal created", "id", id, "title", title, "user", userID, "milestones", len(milestones))
	return goal, nil
}

// ListGoals returns goals for a user filtered by status.
func (svc *GoalsService) ListGoals(userID, status string, limit int) ([]Goal, error) {
	if userID == "" {
		userID = "default"
	}
	if limit <= 0 {
		limit = 20
	}

	conditions := []string{fmt.Sprintf("user_id = '%s'", escapeSQLite(userID))}
	if status != "" {
		conditions = append(conditions, fmt.Sprintf("status = '%s'", escapeSQLite(status)))
	}

	sql := fmt.Sprintf(`SELECT * FROM goals WHERE %s ORDER BY created_at DESC LIMIT %d;`,
		strings.Join(conditions, " AND "), limit)
	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}

	goals := make([]Goal, 0, len(rows))
	for _, row := range rows {
		goals = append(goals, goalFromRow(row))
	}
	return goals, nil
}

// GetGoal retrieves a single goal by ID with parsed milestones.
func (svc *GoalsService) GetGoal(id string) (*Goal, error) {
	sql := fmt.Sprintf(`SELECT * FROM goals WHERE id = '%s';`, escapeSQLite(id))
	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("get goal: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("goal not found: %s", id)
	}
	goal := goalFromRow(rows[0])
	return &goal, nil
}

// UpdateGoal updates specific fields of a goal.
func (svc *GoalsService) UpdateGoal(id string, fields map[string]any) (*Goal, error) {
	if len(fields) == 0 {
		return svc.GetGoal(id)
	}

	var setClauses []string
	for key, val := range fields {
		col := goalFieldToColumn(key)
		if col == "" {
			continue
		}
		switch v := val.(type) {
		case string:
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", col, escapeSQLite(v)))
		case float64:
			setClauses = append(setClauses, fmt.Sprintf("%s = %d", col, int(v)))
		case int:
			setClauses = append(setClauses, fmt.Sprintf("%s = %d", col, v))
		default:
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", col, escapeSQLite(fmt.Sprintf("%v", v))))
		}
	}
	if len(setClauses) == 0 {
		return svc.GetGoal(id)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	setClauses = append(setClauses, fmt.Sprintf("updated_at = '%s'", escapeSQLite(now)))

	sql := fmt.Sprintf(`UPDATE goals SET %s WHERE id = '%s';`,
		strings.Join(setClauses, ", "), escapeSQLite(id))
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("update goal: %s: %w", strings.TrimSpace(string(out)), err)
	}

	logInfo("goal updated", "id", id)
	return svc.GetGoal(id)
}

// CompleteMilestone marks a milestone as done and auto-updates progress.
func (svc *GoalsService) CompleteMilestone(goalID, milestoneID string) error {
	goal, err := svc.GetGoal(goalID)
	if err != nil {
		return err
	}

	found := false
	for i, m := range goal.Milestones {
		if m.ID == milestoneID {
			goal.Milestones[i].Done = true
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("milestone not found: %s", milestoneID)
	}

	// Calculate new progress.
	progress := calculateMilestoneProgress(goal.Milestones)

	milestonesJSON, err := json.Marshal(goal.Milestones)
	if err != nil {
		return fmt.Errorf("marshal milestones: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(`UPDATE goals SET milestones = '%s', progress = %d, updated_at = '%s' WHERE id = '%s';`,
		escapeSQLite(string(milestonesJSON)),
		progress,
		escapeSQLite(now),
		escapeSQLite(goalID),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("complete milestone: %s: %w", strings.TrimSpace(string(out)), err)
	}

	logInfo("milestone completed", "goal_id", goalID, "milestone_id", milestoneID, "progress", progress)
	return nil
}

// AddMilestone adds a new milestone to an existing goal.
func (svc *GoalsService) AddMilestone(goalID, title, dueDate string) (*Goal, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("milestone title is required")
	}

	goal, err := svc.GetGoal(goalID)
	if err != nil {
		return nil, err
	}

	newMilestone := Milestone{
		ID:      newUUID(),
		Title:   title,
		Done:    false,
		DueDate: dueDate,
	}
	goal.Milestones = append(goal.Milestones, newMilestone)

	// Recalculate progress.
	progress := calculateMilestoneProgress(goal.Milestones)

	milestonesJSON, err := json.Marshal(goal.Milestones)
	if err != nil {
		return nil, fmt.Errorf("marshal milestones: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(`UPDATE goals SET milestones = '%s', progress = %d, updated_at = '%s' WHERE id = '%s';`,
		escapeSQLite(string(milestonesJSON)),
		progress,
		escapeSQLite(now),
		escapeSQLite(goalID),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("add milestone: %s: %w", strings.TrimSpace(string(out)), err)
	}

	logInfo("milestone added", "goal_id", goalID, "title", title)
	return svc.GetGoal(goalID)
}

// ReviewGoal adds a review note with the current date.
func (svc *GoalsService) ReviewGoal(goalID, note string) error {
	if strings.TrimSpace(note) == "" {
		return fmt.Errorf("review note is required")
	}

	goal, err := svc.GetGoal(goalID)
	if err != nil {
		return err
	}

	today := time.Now().UTC().Format("2006-01-02")
	reviewNote := ReviewNote{
		Date: today,
		Note: note,
	}
	goal.ReviewNotes = append(goal.ReviewNotes, reviewNote)

	reviewNotesJSON, err := json.Marshal(goal.ReviewNotes)
	if err != nil {
		return fmt.Errorf("marshal review notes: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(`UPDATE goals SET review_notes = '%s', updated_at = '%s' WHERE id = '%s';`,
		escapeSQLite(string(reviewNotesJSON)),
		escapeSQLite(now),
		escapeSQLite(goalID),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("review goal: %s: %w", strings.TrimSpace(string(out)), err)
	}

	logInfo("goal reviewed", "goal_id", goalID)
	return nil
}

// GetStaleGoals returns active goals with no update in the given number of days.
func (svc *GoalsService) GetStaleGoals(userID string, staleDays int) ([]Goal, error) {
	if userID == "" {
		userID = "default"
	}
	if staleDays <= 0 {
		staleDays = 14
	}

	cutoff := time.Now().UTC().Add(-time.Duration(staleDays) * 24 * time.Hour).Format(time.RFC3339)

	sql := fmt.Sprintf(`SELECT * FROM goals WHERE user_id = '%s' AND status = 'active' AND updated_at < '%s' ORDER BY updated_at ASC;`,
		escapeSQLite(userID), escapeSQLite(cutoff))
	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("get stale goals: %w", err)
	}

	goals := make([]Goal, 0, len(rows))
	for _, row := range rows {
		goals = append(goals, goalFromRow(row))
	}
	return goals, nil
}

// GoalSummary returns an overview of goals for a user.
func (svc *GoalsService) GoalSummary(userID string) (map[string]any, error) {
	if userID == "" {
		userID = "default"
	}

	summary := map[string]any{}

	// Total active goals.
	sql := fmt.Sprintf(`SELECT COUNT(*) as cnt FROM goals WHERE user_id = '%s' AND status = 'active';`,
		escapeSQLite(userID))
	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("summary active: %w", err)
	}
	if len(rows) > 0 {
		summary["active_count"] = jsonInt(rows[0]["cnt"])
	}

	// Completed this month.
	monthStart := time.Now().UTC().Format("2006-01") + "-01T00:00:00Z"
	sql = fmt.Sprintf(`SELECT COUNT(*) as cnt FROM goals WHERE user_id = '%s' AND status = 'completed' AND updated_at >= '%s';`,
		escapeSQLite(userID), escapeSQLite(monthStart))
	rows, err = queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("summary completed: %w", err)
	}
	if len(rows) > 0 {
		summary["completed_this_month"] = jsonInt(rows[0]["cnt"])
	}

	// Overdue (past target_date, still active).
	today := time.Now().UTC().Format("2006-01-02")
	sql = fmt.Sprintf(`SELECT COUNT(*) as cnt FROM goals WHERE user_id = '%s' AND status = 'active' AND target_date != '' AND target_date < '%s';`,
		escapeSQLite(userID), escapeSQLite(today))
	rows, err = queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("summary overdue: %w", err)
	}
	if len(rows) > 0 {
		summary["overdue"] = jsonInt(rows[0]["cnt"])
	}

	// By category.
	sql = fmt.Sprintf(`SELECT category, COUNT(*) as cnt FROM goals WHERE user_id = '%s' AND status = 'active' AND category != '' GROUP BY category ORDER BY cnt DESC;`,
		escapeSQLite(userID))
	rows, err = queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("summary by category: %w", err)
	}
	byCategory := map[string]int{}
	for _, row := range rows {
		cat := jsonStr(row["category"])
		if cat != "" {
			byCategory[cat] = jsonInt(row["cnt"])
		}
	}
	summary["by_category"] = byCategory

	// Average progress of active goals.
	sql = fmt.Sprintf(`SELECT AVG(progress) as avg_prog FROM goals WHERE user_id = '%s' AND status = 'active';`,
		escapeSQLite(userID))
	rows, err = queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("summary avg progress: %w", err)
	}
	if len(rows) > 0 {
		summary["average_progress"] = jsonInt(rows[0]["avg_prog"])
	}

	// Total goals count.
	sql = fmt.Sprintf(`SELECT COUNT(*) as cnt FROM goals WHERE user_id = '%s';`,
		escapeSQLite(userID))
	rows, err = queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("summary total: %w", err)
	}
	if len(rows) > 0 {
		summary["total_count"] = jsonInt(rows[0]["cnt"])
	}

	return summary, nil
}

// --- Tool Handlers ---

// toolGoalCreate handles the goal_create tool.
func toolGoalCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalGoalsService == nil {
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

	goal, err := globalGoalsService.CreateGoal(args.UserID, args.Title, args.Description, args.Category, args.TargetDate)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(goal, "", "  ")
	result := string(out)

	// P29.0: Suggest habits for the new goal.
	if globalLifecycleEngine != nil && cfg.Lifecycle.AutoHabitSuggest {
		suggestions := globalLifecycleEngine.SuggestHabitForGoal(args.Title, args.Category)
		if len(suggestions) > 0 {
			result += "\n\nSuggested habits: " + strings.Join(suggestions, ", ")
		}
	}

	return result, nil
}

// toolGoalList handles the goal_list tool.
func toolGoalList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalGoalsService == nil {
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

	goals, err := globalGoalsService.ListGoals(args.UserID, args.Status, args.Limit)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(goals, "", "  ")
	return string(out), nil
}

// toolGoalUpdate handles the goal_update tool.
func toolGoalUpdate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalGoalsService == nil {
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
		if err := globalGoalsService.CompleteMilestone(args.ID, args.MilestoneID); err != nil {
			return "", err
		}
		goal, err := globalGoalsService.GetGoal(args.ID)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(goal, "", "  ")
		return fmt.Sprintf("Milestone completed. Progress: %d%%\n%s", goal.Progress, string(out)), nil

	case "add_milestone":
		if args.Title == "" {
			return "", fmt.Errorf("title is required for add_milestone")
		}
		goal, err := globalGoalsService.AddMilestone(args.ID, args.Title, args.DueDate)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(goal, "", "  ")
		return fmt.Sprintf("Milestone added.\n%s", string(out)), nil

	case "review":
		if args.Note == "" {
			return "", fmt.Errorf("note is required for review")
		}
		if err := globalGoalsService.ReviewGoal(args.ID, args.Note); err != nil {
			return "", err
		}
		goal, err := globalGoalsService.GetGoal(args.ID)
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
		goal, err := globalGoalsService.UpdateGoal(args.ID, fields)
		if err != nil {
			return "", err
		}

		// P29.0: Trigger celebration on goal completion.
		if args.Status == "completed" && globalLifecycleEngine != nil {
			if err := globalLifecycleEngine.OnGoalCompleted(args.ID); err != nil {
				logWarn("lifecycle: goal completion hook failed", "error", err)
			}
		}

		out, _ := json.MarshalIndent(goal, "", "  ")
		return fmt.Sprintf("Goal updated.\n%s", string(out)), nil
	}
}

// toolGoalReview handles the goal_review tool (weekly review summary).
func toolGoalReview(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalGoalsService == nil {
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

	staleGoals, err := globalGoalsService.GetStaleGoals(args.UserID, args.StaleDays)
	if err != nil {
		return "", err
	}

	summary, err := globalGoalsService.GoalSummary(args.UserID)
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

// --- Helpers ---

// goalFromRow converts a queryDB row to a Goal.
func goalFromRow(row map[string]any) Goal {
	g := Goal{
		ID:          jsonStr(row["id"]),
		UserID:      jsonStr(row["user_id"]),
		Title:       jsonStr(row["title"]),
		Description: jsonStr(row["description"]),
		Category:    jsonStr(row["category"]),
		TargetDate:  jsonStr(row["target_date"]),
		Status:      jsonStr(row["status"]),
		Progress:    jsonInt(row["progress"]),
		CreatedAt:   jsonStr(row["created_at"]),
		UpdatedAt:   jsonStr(row["updated_at"]),
	}

	// Parse milestones from JSON.
	msStr := jsonStr(row["milestones"])
	if msStr != "" {
		var milestones []Milestone
		if json.Unmarshal([]byte(msStr), &milestones) == nil {
			g.Milestones = milestones
		}
	}
	if g.Milestones == nil {
		g.Milestones = []Milestone{}
	}

	// Parse review notes from JSON.
	rnStr := jsonStr(row["review_notes"])
	if rnStr != "" {
		var notes []ReviewNote
		if json.Unmarshal([]byte(rnStr), &notes) == nil {
			g.ReviewNotes = notes
		}
	}
	if g.ReviewNotes == nil {
		g.ReviewNotes = []ReviewNote{}
	}

	return g
}

// goalFieldToColumn maps JSON field names to DB column names.
func goalFieldToColumn(field string) string {
	switch field {
	case "title":
		return "title"
	case "description":
		return "description"
	case "category":
		return "category"
	case "target_date":
		return "target_date"
	case "status":
		return "status"
	case "progress":
		return "progress"
	default:
		return ""
	}
}

// parseMilestonesFromDescription extracts milestones from description text.
// Looks for numbered items ("1. ...", "2. ...") or bullet points ("- ...").
// If none found, creates 3 default milestones: Plan, Execute, Review.
func parseMilestonesFromDescription(description string) []Milestone {
	if strings.TrimSpace(description) == "" {
		return defaultMilestones()
	}

	var milestones []Milestone

	// Try numbered items: "1. ...", "2. ..."
	numberedRe := regexp.MustCompile(`(?m)^\s*\d+[\.\)]\s+(.+)$`)
	matches := numberedRe.FindAllStringSubmatch(description, -1)
	if len(matches) >= 2 {
		for _, match := range matches {
			milestones = append(milestones, Milestone{
				ID:    newUUID(),
				Title: strings.TrimSpace(match[1]),
				Done:  false,
			})
		}
		return milestones
	}

	// Try bullet points: "- ..."
	bulletRe := regexp.MustCompile(`(?m)^\s*[-*]\s+(.+)$`)
	matches = bulletRe.FindAllStringSubmatch(description, -1)
	if len(matches) >= 2 {
		for _, match := range matches {
			milestones = append(milestones, Milestone{
				ID:    newUUID(),
				Title: strings.TrimSpace(match[1]),
				Done:  false,
			})
		}
		return milestones
	}

	return defaultMilestones()
}

// defaultMilestones returns 3 default milestones: Plan, Execute, Review.
func defaultMilestones() []Milestone {
	return []Milestone{
		{ID: newUUID(), Title: "Plan", Done: false},
		{ID: newUUID(), Title: "Execute", Done: false},
		{ID: newUUID(), Title: "Review", Done: false},
	}
}

// calculateMilestoneProgress returns progress as % of done milestones.
func calculateMilestoneProgress(milestones []Milestone) int {
	if len(milestones) == 0 {
		return 0
	}
	done := 0
	for _, m := range milestones {
		if m.Done {
			done++
		}
	}
	return (done * 100) / len(milestones)
}
