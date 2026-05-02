// Package goals implements goal planning and milestone tracking.
package goals

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"tetora/internal/life/lifedb"
)

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

// Service provides goal planning and tracking operations.
type Service struct {
	db     lifedb.DB
	dbPath string
}

// New creates a new goals Service.
func New(dbPath string, db lifedb.DB) *Service {
	return &Service{
		db:     db,
		dbPath: dbPath,
	}
}

// InitDB creates the goals table and indexes.
func InitDB(dbPath string) error {
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

// CreateGoal creates a new goal with auto-generated milestones.
// newUUID is a callback to generate unique IDs.
func (svc *Service) CreateGoal(id, userID, title, description, category, targetDate string, newUUID func() string) (*Goal, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("goal title is required")
	}

	now := time.Now().UTC().Format(time.RFC3339)

	if userID == "" {
		userID = "default"
	}

	milestones := ParseMilestonesFromDescription(description, newUUID)

	milestonesJSON, err := json.Marshal(milestones)
	if err != nil {
		return nil, fmt.Errorf("marshal milestones: %w", err)
	}

	reviewNotes := []ReviewNote{}
	reviewNotesJSON, _ := json.Marshal(reviewNotes)

	sql := fmt.Sprintf(`INSERT INTO goals (id, user_id, title, description, category, target_date, status, progress, milestones, review_notes, created_at, updated_at)
VALUES ('%s','%s','%s','%s','%s','%s','active',0,'%s','%s','%s','%s');`,
		svc.db.Escape(id),
		svc.db.Escape(userID),
		svc.db.Escape(title),
		svc.db.Escape(description),
		svc.db.Escape(category),
		svc.db.Escape(targetDate),
		svc.db.Escape(string(milestonesJSON)),
		svc.db.Escape(string(reviewNotesJSON)),
		svc.db.Escape(now),
		svc.db.Escape(now),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("create goal: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return &Goal{
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
	}, nil
}

// ListGoals returns goals for a user filtered by status.
func (svc *Service) ListGoals(userID, status string, limit int) ([]Goal, error) {
	if userID == "" {
		userID = "default"
	}
	if limit <= 0 {
		limit = 20
	}

	conditions := []string{fmt.Sprintf("user_id = '%s'", svc.db.Escape(userID))}
	if status != "" {
		conditions = append(conditions, fmt.Sprintf("status = '%s'", svc.db.Escape(status)))
	}

	sql := fmt.Sprintf(`SELECT * FROM goals WHERE %s ORDER BY created_at DESC LIMIT %d;`,
		strings.Join(conditions, " AND "), limit)
	rows, err := svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}

	goals := make([]Goal, 0, len(rows))
	for _, row := range rows {
		goals = append(goals, goalFromRow(row))
	}
	return goals, nil
}

// GetGoal retrieves a single goal by ID.
func (svc *Service) GetGoal(id string) (*Goal, error) {
	sql := fmt.Sprintf(`SELECT * FROM goals WHERE id = '%s';`, svc.db.Escape(id))
	rows, err := svc.db.Query(svc.dbPath, sql)
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
func (svc *Service) UpdateGoal(id string, fields map[string]any) (*Goal, error) {
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
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", col, svc.db.Escape(v)))
		case float64:
			setClauses = append(setClauses, fmt.Sprintf("%s = %d", col, int(v)))
		case int:
			setClauses = append(setClauses, fmt.Sprintf("%s = %d", col, v))
		default:
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", col, svc.db.Escape(fmt.Sprintf("%v", v))))
		}
	}
	if len(setClauses) == 0 {
		return svc.GetGoal(id)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	setClauses = append(setClauses, fmt.Sprintf("updated_at = '%s'", svc.db.Escape(now)))

	sql := fmt.Sprintf(`UPDATE goals SET %s WHERE id = '%s';`,
		strings.Join(setClauses, ", "), svc.db.Escape(id))
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("update goal: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return svc.GetGoal(id)
}

// CompleteMilestone marks a milestone as done and auto-updates progress.
func (svc *Service) CompleteMilestone(goalID, milestoneID string) error {
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

	progress := CalculateMilestoneProgress(goal.Milestones)

	milestonesJSON, err := json.Marshal(goal.Milestones)
	if err != nil {
		return fmt.Errorf("marshal milestones: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(`UPDATE goals SET milestones = '%s', progress = %d, updated_at = '%s' WHERE id = '%s';`,
		svc.db.Escape(string(milestonesJSON)),
		progress,
		svc.db.Escape(now),
		svc.db.Escape(goalID),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("complete milestone: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

// AddMilestone adds a new milestone to an existing goal.
func (svc *Service) AddMilestone(goalID, milestoneID, title, dueDate string) (*Goal, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("milestone title is required")
	}

	goal, err := svc.GetGoal(goalID)
	if err != nil {
		return nil, err
	}

	newMilestone := Milestone{
		ID:      milestoneID,
		Title:   title,
		Done:    false,
		DueDate: dueDate,
	}
	goal.Milestones = append(goal.Milestones, newMilestone)

	progress := CalculateMilestoneProgress(goal.Milestones)

	milestonesJSON, err := json.Marshal(goal.Milestones)
	if err != nil {
		return nil, fmt.Errorf("marshal milestones: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(`UPDATE goals SET milestones = '%s', progress = %d, updated_at = '%s' WHERE id = '%s';`,
		svc.db.Escape(string(milestonesJSON)),
		progress,
		svc.db.Escape(now),
		svc.db.Escape(goalID),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("add milestone: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return svc.GetGoal(goalID)
}

// ReviewGoal adds a review note with the current date.
func (svc *Service) ReviewGoal(goalID, note string) error {
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
		svc.db.Escape(string(reviewNotesJSON)),
		svc.db.Escape(now),
		svc.db.Escape(goalID),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("review goal: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

// GetStaleGoals returns active goals with no update in the given number of days.
func (svc *Service) GetStaleGoals(userID string, staleDays int) ([]Goal, error) {
	if userID == "" {
		userID = "default"
	}
	if staleDays <= 0 {
		staleDays = 14
	}

	cutoff := time.Now().UTC().Add(-time.Duration(staleDays) * 24 * time.Hour).Format(time.RFC3339)

	sql := fmt.Sprintf(`SELECT * FROM goals WHERE user_id = '%s' AND status = 'active' AND updated_at < '%s' ORDER BY updated_at ASC;`,
		svc.db.Escape(userID), svc.db.Escape(cutoff))
	rows, err := svc.db.Query(svc.dbPath, sql)
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
func (svc *Service) GoalSummary(userID string) (map[string]any, error) {
	if userID == "" {
		userID = "default"
	}

	summary := map[string]any{}

	sql := fmt.Sprintf(`SELECT COUNT(*) as cnt FROM goals WHERE user_id = '%s' AND status = 'active';`,
		svc.db.Escape(userID))
	rows, err := svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("summary active: %w", err)
	}
	if len(rows) > 0 {
		summary["active_count"] = jsonInt(rows[0]["cnt"])
	}

	monthStart := time.Now().UTC().Format("2006-01") + "-01T00:00:00Z"
	sql = fmt.Sprintf(`SELECT COUNT(*) as cnt FROM goals WHERE user_id = '%s' AND status = 'completed' AND updated_at >= '%s';`,
		svc.db.Escape(userID), svc.db.Escape(monthStart))
	rows, err = svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("summary completed: %w", err)
	}
	if len(rows) > 0 {
		summary["completed_this_month"] = jsonInt(rows[0]["cnt"])
	}

	today := time.Now().UTC().Format("2006-01-02")
	sql = fmt.Sprintf(`SELECT COUNT(*) as cnt FROM goals WHERE user_id = '%s' AND status = 'active' AND target_date != '' AND target_date < '%s';`,
		svc.db.Escape(userID), svc.db.Escape(today))
	rows, err = svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("summary overdue: %w", err)
	}
	if len(rows) > 0 {
		summary["overdue"] = jsonInt(rows[0]["cnt"])
	}

	sql = fmt.Sprintf(`SELECT category, COUNT(*) as cnt FROM goals WHERE user_id = '%s' AND status = 'active' AND category != '' GROUP BY category ORDER BY cnt DESC;`,
		svc.db.Escape(userID))
	rows, err = svc.db.Query(svc.dbPath, sql)
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

	sql = fmt.Sprintf(`SELECT AVG(progress) as avg_prog FROM goals WHERE user_id = '%s' AND status = 'active';`,
		svc.db.Escape(userID))
	rows, err = svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("summary avg progress: %w", err)
	}
	if len(rows) > 0 {
		summary["average_progress"] = jsonInt(rows[0]["avg_prog"])
	}

	sql = fmt.Sprintf(`SELECT COUNT(*) as cnt FROM goals WHERE user_id = '%s';`,
		svc.db.Escape(userID))
	rows, err = svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("summary total: %w", err)
	}
	if len(rows) > 0 {
		summary["total_count"] = jsonInt(rows[0]["cnt"])
	}

	return summary, nil
}

// --- Helpers ---

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

// ParseMilestonesFromDescription extracts milestones from description text.
func ParseMilestonesFromDescription(description string, newUUID func() string) []Milestone {
	if strings.TrimSpace(description) == "" {
		return DefaultMilestones(newUUID)
	}

	var milestones []Milestone

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

	return DefaultMilestones(newUUID)
}

// DefaultMilestones returns 3 default milestones: Plan, Execute, Review.
func DefaultMilestones(newUUID func() string) []Milestone {
	return []Milestone{
		{ID: newUUID(), Title: "Plan", Done: false},
		{ID: newUUID(), Title: "Execute", Done: false},
		{ID: newUUID(), Title: "Review", Done: false},
	}
}

// CalculateMilestoneProgress returns progress as % of done milestones.
func CalculateMilestoneProgress(milestones []Milestone) int {
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

// --- JSON helpers ---

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func jsonInt(v any) int {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		var i int
		fmt.Sscanf(x, "%d", &i)
		return i
	}
	return 0
}

func (svc *Service) DBPath() string { return svc.dbPath }
