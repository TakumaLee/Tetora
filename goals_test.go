package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- Test Helpers ---

func setupGoalsTestDB(t *testing.T) (string, *GoalsService) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_goals.db")

	f, err := os.Create(dbPath)
	if err != nil {
		t.Fatalf("create db file: %v", err)
	}
	f.Close()

	if err := initGoalsDB(dbPath); err != nil {
		t.Fatalf("initGoalsDB: %v", err)
	}
	cfg := &Config{HistoryDB: dbPath}
	svc := &GoalsService{dbPath: dbPath, cfg: cfg}
	return dbPath, svc
}

// --- DB Init ---

func TestInitGoalsDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	f, err := os.Create(dbPath)
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	f.Close()

	if err := initGoalsDB(dbPath); err != nil {
		t.Fatalf("initGoalsDB: %v", err)
	}

	// Verify table exists by running a query.
	rows, err := queryDB(dbPath, "SELECT COUNT(*) as cnt FROM goals;")
	if err != nil {
		t.Fatalf("query after init: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected query result")
	}
}

// --- CreateGoal ---

func TestCreateGoal(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, err := svc.CreateGoal("user1", "Learn Go", "Master Go programming", "learning", "2026-12-31")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if goal.ID == "" {
		t.Error("expected non-empty ID")
	}
	if goal.Title != "Learn Go" {
		t.Errorf("expected title 'Learn Go', got %q", goal.Title)
	}
	if goal.Status != "active" {
		t.Errorf("expected status 'active', got %q", goal.Status)
	}
	if goal.Progress != 0 {
		t.Errorf("expected progress 0, got %d", goal.Progress)
	}
	if goal.Category != "learning" {
		t.Errorf("expected category 'learning', got %q", goal.Category)
	}
	if goal.TargetDate != "2026-12-31" {
		t.Errorf("expected target_date '2026-12-31', got %q", goal.TargetDate)
	}
	if goal.CreatedAt == "" {
		t.Error("expected non-empty CreatedAt")
	}
	// Should have default milestones since description has no numbered/bullet items.
	if len(goal.Milestones) != 3 {
		t.Errorf("expected 3 default milestones, got %d", len(goal.Milestones))
	}
}

func TestCreateGoal_EmptyTitle(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	_, err := svc.CreateGoal("user1", "", "no title", "", "")
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestCreateGoal_WithMilestones(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	desc := `Study plan:
1. Buy textbooks
2. Complete chapters 1-5
3. Take practice tests
4. Review weak areas`

	goal, err := svc.CreateGoal("user1", "Pass JLPT N2", desc, "learning", "2026-07-01")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if len(goal.Milestones) != 4 {
		t.Errorf("expected 4 milestones from description, got %d", len(goal.Milestones))
	}
	if goal.Milestones[0].Title != "Buy textbooks" {
		t.Errorf("expected first milestone 'Buy textbooks', got %q", goal.Milestones[0].Title)
	}
	if goal.Milestones[3].Title != "Review weak areas" {
		t.Errorf("expected last milestone 'Review weak areas', got %q", goal.Milestones[3].Title)
	}
}

func TestCreateGoal_BulletMilestones(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	desc := `Steps:
- Research options
- Make a decision
- Execute plan`

	goal, err := svc.CreateGoal("user1", "Buy a house", desc, "financial", "2027-01-01")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if len(goal.Milestones) != 3 {
		t.Errorf("expected 3 milestones from bullets, got %d", len(goal.Milestones))
	}
	if goal.Milestones[0].Title != "Research options" {
		t.Errorf("expected 'Research options', got %q", goal.Milestones[0].Title)
	}
}

func TestCreateGoal_DefaultUserID(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, err := svc.CreateGoal("", "Test goal", "", "", "")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if goal.UserID != "default" {
		t.Errorf("expected user_id 'default', got %q", goal.UserID)
	}
}

// --- ListGoals ---

func TestListGoals(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	// Create multiple goals.
	svc.CreateGoal("user1", "Goal A", "", "career", "")
	svc.CreateGoal("user1", "Goal B", "", "health", "")
	svc.CreateGoal("user2", "Goal C", "", "learning", "")

	goals, err := svc.ListGoals("user1", "", 10)
	if err != nil {
		t.Fatalf("ListGoals: %v", err)
	}
	if len(goals) != 2 {
		t.Errorf("expected 2 goals for user1, got %d", len(goals))
	}
}

func TestListGoals_FilterStatus(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	g1, _ := svc.CreateGoal("user1", "Active goal", "", "", "")
	svc.CreateGoal("user1", "Another active", "", "", "")

	// Complete one goal.
	svc.UpdateGoal(g1.ID, map[string]any{"status": "completed"})

	active, err := svc.ListGoals("user1", "active", 10)
	if err != nil {
		t.Fatalf("ListGoals active: %v", err)
	}
	if len(active) != 1 {
		t.Errorf("expected 1 active goal, got %d", len(active))
	}

	completed, err := svc.ListGoals("user1", "completed", 10)
	if err != nil {
		t.Fatalf("ListGoals completed: %v", err)
	}
	if len(completed) != 1 {
		t.Errorf("expected 1 completed goal, got %d", len(completed))
	}
}

func TestListGoals_Limit(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	for i := 0; i < 5; i++ {
		svc.CreateGoal("user1", "Goal", "", "", "")
	}

	goals, err := svc.ListGoals("user1", "", 3)
	if err != nil {
		t.Fatalf("ListGoals: %v", err)
	}
	if len(goals) != 3 {
		t.Errorf("expected 3 goals with limit, got %d", len(goals))
	}
}

// --- GetGoal ---

func TestGetGoal(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	created, _ := svc.CreateGoal("user1", "Test Goal", "Some desc", "career", "2026-12-31")

	got, err := svc.GetGoal(created.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.Title != "Test Goal" {
		t.Errorf("expected 'Test Goal', got %q", got.Title)
	}
	if got.Description != "Some desc" {
		t.Errorf("expected 'Some desc', got %q", got.Description)
	}
	if len(got.Milestones) != 3 {
		t.Errorf("expected 3 milestones, got %d", len(got.Milestones))
	}
}

func TestGetGoal_NotFound(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	_, err := svc.GetGoal("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent goal")
	}
}

// --- UpdateGoal ---

func TestUpdateGoal_Status(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	created, _ := svc.CreateGoal("user1", "Goal", "", "", "")

	updated, err := svc.UpdateGoal(created.ID, map[string]any{"status": "paused"})
	if err != nil {
		t.Fatalf("UpdateGoal: %v", err)
	}
	if updated.Status != "paused" {
		t.Errorf("expected status 'paused', got %q", updated.Status)
	}
}

func TestUpdateGoal_Progress(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	created, _ := svc.CreateGoal("user1", "Goal", "", "", "")

	updated, err := svc.UpdateGoal(created.ID, map[string]any{"progress": 50})
	if err != nil {
		t.Fatalf("UpdateGoal: %v", err)
	}
	if updated.Progress != 50 {
		t.Errorf("expected progress 50, got %d", updated.Progress)
	}
}

func TestUpdateGoal_MultipleFields(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	created, _ := svc.CreateGoal("user1", "Old Title", "", "", "")

	updated, err := svc.UpdateGoal(created.ID, map[string]any{
		"title":       "New Title",
		"category":    "health",
		"target_date": "2027-01-01",
	})
	if err != nil {
		t.Fatalf("UpdateGoal: %v", err)
	}
	if updated.Title != "New Title" {
		t.Errorf("expected 'New Title', got %q", updated.Title)
	}
	if updated.Category != "health" {
		t.Errorf("expected 'health', got %q", updated.Category)
	}
	if updated.TargetDate != "2027-01-01" {
		t.Errorf("expected '2027-01-01', got %q", updated.TargetDate)
	}
}

func TestUpdateGoal_EmptyFields(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	created, _ := svc.CreateGoal("user1", "Goal", "", "", "")

	updated, err := svc.UpdateGoal(created.ID, map[string]any{})
	if err != nil {
		t.Fatalf("UpdateGoal empty: %v", err)
	}
	if updated.Title != "Goal" {
		t.Errorf("expected unchanged title 'Goal', got %q", updated.Title)
	}
}

// --- CompleteMilestone ---

func TestCompleteMilestone(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal("user1", "Goal", "", "", "")
	if len(goal.Milestones) < 3 {
		t.Fatalf("expected at least 3 milestones, got %d", len(goal.Milestones))
	}

	err := svc.CompleteMilestone(goal.ID, goal.Milestones[0].ID)
	if err != nil {
		t.Fatalf("CompleteMilestone: %v", err)
	}

	got, _ := svc.GetGoal(goal.ID)
	if !got.Milestones[0].Done {
		t.Error("expected first milestone to be done")
	}
}

func TestCompleteMilestone_AutoProgress(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal("user1", "Goal", "", "", "")
	// Default: 3 milestones.

	// Complete 1 of 3 -> 33%.
	svc.CompleteMilestone(goal.ID, goal.Milestones[0].ID)
	got, _ := svc.GetGoal(goal.ID)
	if got.Progress != 33 {
		t.Errorf("expected progress 33 after 1/3, got %d", got.Progress)
	}

	// Complete 2 of 3 -> 66%.
	svc.CompleteMilestone(goal.ID, goal.Milestones[1].ID)
	got, _ = svc.GetGoal(goal.ID)
	if got.Progress != 66 {
		t.Errorf("expected progress 66 after 2/3, got %d", got.Progress)
	}

	// Complete 3 of 3 -> 100%.
	svc.CompleteMilestone(goal.ID, goal.Milestones[2].ID)
	got, _ = svc.GetGoal(goal.ID)
	if got.Progress != 100 {
		t.Errorf("expected progress 100 after 3/3, got %d", got.Progress)
	}
}

func TestCompleteMilestone_NotFound(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal("user1", "Goal", "", "", "")

	err := svc.CompleteMilestone(goal.ID, "nonexistent-milestone")
	if err == nil {
		t.Fatal("expected error for nonexistent milestone")
	}
}

// --- AddMilestone ---

func TestAddMilestone(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal("user1", "Goal", "", "", "")
	initialCount := len(goal.Milestones)

	updated, err := svc.AddMilestone(goal.ID, "New milestone", "2026-06-01")
	if err != nil {
		t.Fatalf("AddMilestone: %v", err)
	}
	if len(updated.Milestones) != initialCount+1 {
		t.Errorf("expected %d milestones, got %d", initialCount+1, len(updated.Milestones))
	}

	last := updated.Milestones[len(updated.Milestones)-1]
	if last.Title != "New milestone" {
		t.Errorf("expected 'New milestone', got %q", last.Title)
	}
	if last.DueDate != "2026-06-01" {
		t.Errorf("expected due_date '2026-06-01', got %q", last.DueDate)
	}
	if last.Done {
		t.Error("new milestone should not be done")
	}
}

func TestAddMilestone_EmptyTitle(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal("user1", "Goal", "", "", "")

	_, err := svc.AddMilestone(goal.ID, "", "")
	if err == nil {
		t.Fatal("expected error for empty milestone title")
	}
}

func TestAddMilestone_RecalculatesProgress(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal("user1", "Goal", "", "", "")
	// Default 3 milestones. Complete 1 -> 33%.
	svc.CompleteMilestone(goal.ID, goal.Milestones[0].ID)

	// Add a 4th milestone. Now 1/4 done -> 25%.
	updated, _ := svc.AddMilestone(goal.ID, "Extra step", "")
	if updated.Progress != 25 {
		t.Errorf("expected progress 25 after adding milestone (1/4 done), got %d", updated.Progress)
	}
}

// --- ReviewGoal ---

func TestReviewGoal(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal("user1", "Goal", "", "", "")

	err := svc.ReviewGoal(goal.ID, "Making good progress")
	if err != nil {
		t.Fatalf("ReviewGoal: %v", err)
	}

	got, _ := svc.GetGoal(goal.ID)
	if len(got.ReviewNotes) != 1 {
		t.Fatalf("expected 1 review note, got %d", len(got.ReviewNotes))
	}
	if got.ReviewNotes[0].Note != "Making good progress" {
		t.Errorf("expected note 'Making good progress', got %q", got.ReviewNotes[0].Note)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if got.ReviewNotes[0].Date != today {
		t.Errorf("expected date %s, got %s", today, got.ReviewNotes[0].Date)
	}
}

func TestReviewGoal_Multiple(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal("user1", "Goal", "", "", "")

	svc.ReviewGoal(goal.ID, "Week 1 review")
	svc.ReviewGoal(goal.ID, "Week 2 review")

	got, _ := svc.GetGoal(goal.ID)
	if len(got.ReviewNotes) != 2 {
		t.Errorf("expected 2 review notes, got %d", len(got.ReviewNotes))
	}
}

func TestReviewGoal_EmptyNote(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal("user1", "Goal", "", "", "")

	err := svc.ReviewGoal(goal.ID, "")
	if err == nil {
		t.Fatal("expected error for empty note")
	}
}

// --- GetStaleGoals ---

func TestGetStaleGoals(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	// Create a goal and manually set its updated_at to 30 days ago.
	goal, _ := svc.CreateGoal("user1", "Old goal", "", "", "")
	oldDate := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	svc.UpdateGoal(goal.ID, map[string]any{"title": "Old goal"}) // just to have a valid update
	// Force the updated_at to be old via direct SQL.
	forceSQL := "UPDATE goals SET updated_at = '" + escapeSQLite(oldDate) + "' WHERE id = '" + escapeSQLite(goal.ID) + "';"
	queryDB(svc.dbPath, forceSQL)

	// Create a fresh goal.
	svc.CreateGoal("user1", "Fresh goal", "", "", "")

	stale, err := svc.GetStaleGoals("user1", 14)
	if err != nil {
		t.Fatalf("GetStaleGoals: %v", err)
	}
	if len(stale) != 1 {
		t.Errorf("expected 1 stale goal, got %d", len(stale))
	}
	if len(stale) > 0 && stale[0].Title != "Old goal" {
		t.Errorf("expected stale goal 'Old goal', got %q", stale[0].Title)
	}
}

func TestGetStaleGoals_NoStale(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	// Create a fresh goal (just created, not stale).
	svc.CreateGoal("user1", "Fresh goal", "", "", "")

	stale, err := svc.GetStaleGoals("user1", 14)
	if err != nil {
		t.Fatalf("GetStaleGoals: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("expected 0 stale goals, got %d", len(stale))
	}
}

func TestGetStaleGoals_ExcludesCompleted(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal("user1", "Old completed", "", "", "")
	oldDate := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	svc.UpdateGoal(goal.ID, map[string]any{"status": "completed"})
	forceSQL := "UPDATE goals SET updated_at = '" + escapeSQLite(oldDate) + "' WHERE id = '" + escapeSQLite(goal.ID) + "';"
	queryDB(svc.dbPath, forceSQL)

	stale, err := svc.GetStaleGoals("user1", 14)
	if err != nil {
		t.Fatalf("GetStaleGoals: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("expected 0 stale goals (completed excluded), got %d", len(stale))
	}
}

// --- GoalSummary ---

func TestGoalSummary(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	svc.CreateGoal("user1", "Career goal", "", "career", "")
	svc.CreateGoal("user1", "Health goal", "", "health", "")
	g3, _ := svc.CreateGoal("user1", "Learning goal", "", "learning", "")

	// Complete one goal.
	svc.UpdateGoal(g3.ID, map[string]any{"status": "completed"})

	summary, err := svc.GoalSummary("user1")
	if err != nil {
		t.Fatalf("GoalSummary: %v", err)
	}

	activeCount, ok := summary["active_count"]
	if !ok {
		t.Fatal("expected active_count in summary")
	}
	if activeCount.(int) != 2 {
		t.Errorf("expected 2 active, got %v", activeCount)
	}

	totalCount, ok := summary["total_count"]
	if !ok {
		t.Fatal("expected total_count in summary")
	}
	if totalCount.(int) != 3 {
		t.Errorf("expected 3 total, got %v", totalCount)
	}

	byCategory, ok := summary["by_category"]
	if !ok {
		t.Fatal("expected by_category in summary")
	}
	cats := byCategory.(map[string]int)
	if cats["career"] != 1 {
		t.Errorf("expected 1 career goal, got %d", cats["career"])
	}
	if cats["health"] != 1 {
		t.Errorf("expected 1 health goal, got %d", cats["health"])
	}
}

func TestGoalSummary_Overdue(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	// Create a goal with past target date.
	svc.CreateGoal("user1", "Overdue goal", "", "", "2020-01-01")
	svc.CreateGoal("user1", "Future goal", "", "", "2099-12-31")

	summary, err := svc.GoalSummary("user1")
	if err != nil {
		t.Fatalf("GoalSummary: %v", err)
	}

	overdue, ok := summary["overdue"]
	if !ok {
		t.Fatal("expected overdue in summary")
	}
	if overdue.(int) != 1 {
		t.Errorf("expected 1 overdue, got %v", overdue)
	}
}

func TestGoalSummary_Empty(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	summary, err := svc.GoalSummary("user1")
	if err != nil {
		t.Fatalf("GoalSummary: %v", err)
	}
	if summary["active_count"].(int) != 0 {
		t.Errorf("expected 0 active, got %v", summary["active_count"])
	}
	if summary["total_count"].(int) != 0 {
		t.Errorf("expected 0 total, got %v", summary["total_count"])
	}
}

// --- Tool Handlers ---

func TestToolGoalCreate(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	old := globalGoalsService
	globalGoalsService = svc
	defer func() { globalGoalsService = old }()

	input := json.RawMessage(`{"title":"Pass JLPT N2","description":"Study Japanese","category":"learning","target_date":"2026-07-01"}`)
	result, err := toolGoalCreate(context.Background(), svc.cfg, input)
	if err != nil {
		t.Fatalf("toolGoalCreate: %v", err)
	}

	var goal Goal
	if err := json.Unmarshal([]byte(result), &goal); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if goal.Title != "Pass JLPT N2" {
		t.Errorf("expected title 'Pass JLPT N2', got %q", goal.Title)
	}
	if goal.Category != "learning" {
		t.Errorf("expected category 'learning', got %q", goal.Category)
	}
}

func TestToolGoalCreate_EmptyTitle(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	old := globalGoalsService
	globalGoalsService = svc
	defer func() { globalGoalsService = old }()

	input := json.RawMessage(`{"title":"","category":"learning"}`)
	_, err := toolGoalCreate(context.Background(), svc.cfg, input)
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestToolGoalCreate_NilService(t *testing.T) {
	old := globalGoalsService
	globalGoalsService = nil
	defer func() { globalGoalsService = old }()

	input := json.RawMessage(`{"title":"test"}`)
	_, err := toolGoalCreate(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
}

func TestToolGoalList(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	old := globalGoalsService
	globalGoalsService = svc
	defer func() { globalGoalsService = old }()

	// Create some goals.
	svc.CreateGoal("default", "Goal A", "", "", "")
	svc.CreateGoal("default", "Goal B", "", "", "")

	input := json.RawMessage(`{"user_id":"default","status":"active","limit":10}`)
	result, err := toolGoalList(context.Background(), svc.cfg, input)
	if err != nil {
		t.Fatalf("toolGoalList: %v", err)
	}

	var goals []Goal
	if err := json.Unmarshal([]byte(result), &goals); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(goals) != 2 {
		t.Errorf("expected 2 goals, got %d", len(goals))
	}
}

func TestToolGoalUpdate(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	old := globalGoalsService
	globalGoalsService = svc
	defer func() { globalGoalsService = old }()

	goal, _ := svc.CreateGoal("default", "Goal", "", "", "")

	// Test update action.
	input := json.RawMessage(`{"id":"` + goal.ID + `","action":"update","status":"paused"}`)
	result, err := toolGoalUpdate(context.Background(), svc.cfg, input)
	if err != nil {
		t.Fatalf("toolGoalUpdate: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}

	got, _ := svc.GetGoal(goal.ID)
	if got.Status != "paused" {
		t.Errorf("expected status 'paused', got %q", got.Status)
	}
}

func TestToolGoalUpdate_CompleteMilestone(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	old := globalGoalsService
	globalGoalsService = svc
	defer func() { globalGoalsService = old }()

	goal, _ := svc.CreateGoal("default", "Goal", "", "", "")
	msID := goal.Milestones[0].ID

	input := json.RawMessage(`{"id":"` + goal.ID + `","action":"complete_milestone","milestone_id":"` + msID + `"}`)
	result, err := toolGoalUpdate(context.Background(), svc.cfg, input)
	if err != nil {
		t.Fatalf("toolGoalUpdate complete_milestone: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}

	got, _ := svc.GetGoal(goal.ID)
	if !got.Milestones[0].Done {
		t.Error("expected milestone to be done")
	}
}

func TestToolGoalUpdate_AddMilestone(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	old := globalGoalsService
	globalGoalsService = svc
	defer func() { globalGoalsService = old }()

	goal, _ := svc.CreateGoal("default", "Goal", "", "", "")
	initialCount := len(goal.Milestones)

	input := json.RawMessage(`{"id":"` + goal.ID + `","action":"add_milestone","title":"Extra step","due_date":"2026-06-01"}`)
	_, err := toolGoalUpdate(context.Background(), svc.cfg, input)
	if err != nil {
		t.Fatalf("toolGoalUpdate add_milestone: %v", err)
	}

	got, _ := svc.GetGoal(goal.ID)
	if len(got.Milestones) != initialCount+1 {
		t.Errorf("expected %d milestones, got %d", initialCount+1, len(got.Milestones))
	}
}

func TestToolGoalUpdate_Review(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	old := globalGoalsService
	globalGoalsService = svc
	defer func() { globalGoalsService = old }()

	goal, _ := svc.CreateGoal("default", "Goal", "", "", "")

	input := json.RawMessage(`{"id":"` + goal.ID + `","action":"review","note":"Going well"}`)
	_, err := toolGoalUpdate(context.Background(), svc.cfg, input)
	if err != nil {
		t.Fatalf("toolGoalUpdate review: %v", err)
	}

	got, _ := svc.GetGoal(goal.ID)
	if len(got.ReviewNotes) != 1 {
		t.Errorf("expected 1 review note, got %d", len(got.ReviewNotes))
	}
}

func TestToolGoalUpdate_MissingID(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	old := globalGoalsService
	globalGoalsService = svc
	defer func() { globalGoalsService = old }()

	input := json.RawMessage(`{"action":"update","status":"paused"}`)
	_, err := toolGoalUpdate(context.Background(), svc.cfg, input)
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestToolGoalReview(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	old := globalGoalsService
	globalGoalsService = svc
	defer func() { globalGoalsService = old }()

	svc.CreateGoal("default", "Active goal", "", "career", "")

	input := json.RawMessage(`{"user_id":"default"}`)
	result, err := toolGoalReview(context.Background(), svc.cfg, input)
	if err != nil {
		t.Fatalf("toolGoalReview: %v", err)
	}

	var review map[string]any
	if err := json.Unmarshal([]byte(result), &review); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if _, ok := review["summary"]; !ok {
		t.Error("expected summary in review result")
	}
	if _, ok := review["stale_goals"]; !ok {
		t.Error("expected stale_goals in review result")
	}
}

func TestToolGoalReview_NilService(t *testing.T) {
	old := globalGoalsService
	globalGoalsService = nil
	defer func() { globalGoalsService = old }()

	input := json.RawMessage(`{"user_id":"default"}`)
	_, err := toolGoalReview(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
}

// --- Milestone Parsing ---

func TestParseMilestonesFromDescription_Numbered(t *testing.T) {
	desc := "1. First step\n2. Second step\n3. Third step"
	ms := parseMilestonesFromDescription(desc)
	if len(ms) != 3 {
		t.Errorf("expected 3 milestones, got %d", len(ms))
	}
	if ms[0].Title != "First step" {
		t.Errorf("expected 'First step', got %q", ms[0].Title)
	}
}

func TestParseMilestonesFromDescription_Bullets(t *testing.T) {
	desc := "- Step A\n- Step B\n- Step C"
	ms := parseMilestonesFromDescription(desc)
	if len(ms) != 3 {
		t.Errorf("expected 3 milestones, got %d", len(ms))
	}
	if ms[0].Title != "Step A" {
		t.Errorf("expected 'Step A', got %q", ms[0].Title)
	}
}

func TestParseMilestonesFromDescription_Empty(t *testing.T) {
	ms := parseMilestonesFromDescription("")
	if len(ms) != 3 {
		t.Errorf("expected 3 default milestones, got %d", len(ms))
	}
	if ms[0].Title != "Plan" {
		t.Errorf("expected 'Plan', got %q", ms[0].Title)
	}
	if ms[1].Title != "Execute" {
		t.Errorf("expected 'Execute', got %q", ms[1].Title)
	}
	if ms[2].Title != "Review" {
		t.Errorf("expected 'Review', got %q", ms[2].Title)
	}
}

func TestParseMilestonesFromDescription_NoPatterns(t *testing.T) {
	desc := "Just a plain text description with no bullet points or numbers."
	ms := parseMilestonesFromDescription(desc)
	if len(ms) != 3 {
		t.Errorf("expected 3 default milestones for plain text, got %d", len(ms))
	}
}

func TestParseMilestonesFromDescription_SingleBullet(t *testing.T) {
	// Only 1 bullet point is not enough to count as structured milestones.
	desc := "- Only one item"
	ms := parseMilestonesFromDescription(desc)
	if len(ms) != 3 {
		t.Errorf("expected 3 default milestones for single bullet, got %d", len(ms))
	}
}

// --- Progress Calculation ---

func TestCalculateMilestoneProgress(t *testing.T) {
	tests := []struct {
		name     string
		ms       []Milestone
		expected int
	}{
		{"empty", []Milestone{}, 0},
		{"none done", []Milestone{{Done: false}, {Done: false}}, 0},
		{"half done", []Milestone{{Done: true}, {Done: false}}, 50},
		{"all done", []Milestone{{Done: true}, {Done: true}}, 100},
		{"one of three", []Milestone{{Done: true}, {Done: false}, {Done: false}}, 33},
		{"two of three", []Milestone{{Done: true}, {Done: true}, {Done: false}}, 66},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateMilestoneProgress(tt.ms)
			if got != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, got)
			}
		})
	}
}
