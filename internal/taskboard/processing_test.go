package taskboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tetora/internal/config"
	"tetora/internal/db"
	"tetora/internal/dispatch"
	"tetora/internal/log"
)

// =============================================================================
// Test helpers
// =============================================================================

// mockExecutor implements dispatch.TaskExecutor with a pre-loaded results queue.
type mockExecutor struct {
	results []dispatch.TaskResult
	calls   atomic.Int32
}

func (m *mockExecutor) RunTask(_ context.Context, task dispatch.Task, _ string) dispatch.TaskResult {
	idx := int(m.calls.Add(1)) - 1
	if idx < len(m.results) {
		return m.results[idx]
	}
	// Default: return success with empty output if queue exhausted.
	return dispatch.TaskResult{Status: "success", Output: ""}
}

// mockSkills implements SkillsProvider and records AppendFailure calls.
type mockSkills struct {
	skills         []config.SkillConfig
	appendedSkills []string
}

func (m *mockSkills) SelectSkills(_ dispatch.Task) []config.SkillConfig {
	return m.skills
}

func (m *mockSkills) LoadFailuresByName(_ string) string { return "" }

func (m *mockSkills) AppendFailure(skillName, _, _, _ string) {
	m.appendedSkills = append(m.appendedSkills, skillName)
}

func (m *mockSkills) MaxInject() int { return 4096 }

// newTestDispatcher creates an Engine with a temp SQLite DB, initialises schema,
// and returns a Dispatcher ready for testing.
func newTestDispatcher(t *testing.T, tbCfg config.TaskBoardConfig, deps DispatcherDeps) *Dispatcher {
	t.Helper()
	return newTestDispatcherWithConfig(t, tbCfg, &config.Config{}, deps)
}

// newTestDispatcherWithConfig is like newTestDispatcher but accepts a full Config.
func newTestDispatcherWithConfig(t *testing.T, tbCfg config.TaskBoardConfig, cfg *config.Config, deps DispatcherDeps) *Dispatcher {
	t.Helper()
	dbPath := t.TempDir() + "/test.db"
	engine := NewEngine(dbPath, tbCfg, nil)
	if err := engine.InitSchema(); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	return NewDispatcher(engine, cfg, deps)
}

// initGitRepo creates a temporary directory initialised as a git repository.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"git", "init", dir},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args[1:], err, out)
		}
	}
	return dir
}

// mockWorktrees implements WorktreeManageable for tests.
type mockWorktrees struct {
	createFn    func(repoDir, taskID, branch string) (string, error)
	created     []string
	removed     []string
	commitCount int
	hasChanges  bool
}

func (m *mockWorktrees) Create(repoDir, taskID, branch string) (string, error) {
	if m.createFn != nil {
		dir, err := m.createFn(repoDir, taskID, branch)
		if err == nil && dir != "" {
			m.created = append(m.created, dir)
		}
		return dir, err
	}
	dir := "/tmp/wt-" + taskID
	m.created = append(m.created, dir)
	return dir, nil
}

func (m *mockWorktrees) Remove(_, worktreeDir string) error {
	m.removed = append(m.removed, worktreeDir)
	return nil
}
func (m *mockWorktrees) CommitCount(_ string) int    { return m.commitCount }
func (m *mockWorktrees) HasChanges(_ string) bool    { return m.hasChanges }
func (m *mockWorktrees) Merge(_, _, _ string) (string, error) { return "", nil }
func (m *mockWorktrees) AcquireSessionLock(_ string) func() { return func() {} }

// reviewJSON builds a review JSON response string.
func reviewJSON(verdict, comment string) string {
	b, _ := json.Marshal(map[string]string{"verdict": verdict, "comment": comment})
	return string(b)
}

// reviewJSONWithItems builds a review JSON response with actionable items.
func reviewJSONWithItems(verdict, comment string, items []reviewActionableItem) string {
	payload := struct {
		Verdict         string                 `json:"verdict"`
		Comment         string                 `json:"comment"`
		ActionableItems []reviewActionableItem `json:"actionable_items"`
	}{verdict, comment, items}
	b, _ := json.Marshal(payload)
	return string(b)
}

// =============================================================================
// devQALoop tests
// =============================================================================

func TestDevQALoop_PassFirstAttempt(t *testing.T) {
	// First call: dev execution succeeds.
	// Second call: review (Source="auto-review") returns approve.
	ex := &mockExecutor{
		results: []dispatch.TaskResult{
			{Status: "success", Output: "great work"},
			{Status: "success", Output: reviewJSON("approve", "looks good")},
		},
	}

	tbCfg := config.TaskBoardConfig{
		MaxRetries: 2,
		AutoDispatch: config.TaskBoardDispatchConfig{
			ReviewLoop: true,
		},
	}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor: ex,
	})

	// Create test task in DB so AddComment doesn't fail.
	task, err := d.engine.CreateTask(TaskBoard{
		Title:    "test task",
		Status:   "todo",
		Assignee: "ruri",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	dispTask := dispatch.Task{
		Name:   "board:" + task.ID,
		Prompt: "do the thing",
		Agent:  task.Assignee,
		Source: "taskboard",
	}

	result := d.devQALoop(context.Background(), task, dispTask, false, "")

	if !result.QAApproved {
		t.Errorf("expected QAApproved=true, got false")
	}
	if result.Attempts != 1 {
		t.Errorf("expected Attempts=1, got %d", result.Attempts)
	}
}

func TestDevQALoop_RetryOnRejection(t *testing.T) {
	// Attempt 1: dev OK, review rejects.
	// Attempt 2: dev OK, review approves.
	exec := &mockExecutor{
		results: []dispatch.TaskResult{
			{Status: "success", Output: "first attempt output"},
			{Status: "success", Output: reviewJSON("fix", "missing error handling")},
			{Status: "success", Output: "fixed output"},
			{Status: "success", Output: reviewJSON("approve", "fixed")},
		},
	}

	tbCfg := config.TaskBoardConfig{
		MaxRetries: 2,
		AutoDispatch: config.TaskBoardDispatchConfig{
			ReviewLoop: true,
		},
	}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor: exec,
	})

	task, err := d.engine.CreateTask(TaskBoard{
		Title:    "retry test task",
		Status:   "todo",
		Assignee: "ruri",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	dispTask := dispatch.Task{
		Name:   "board:" + task.ID,
		Prompt: "do the thing",
		Agent:  task.Assignee,
		Source: "taskboard",
	}

	result := d.devQALoop(context.Background(), task, dispTask, false, "")

	if !result.QAApproved {
		t.Errorf("expected QAApproved=true, got false")
	}
	if result.Attempts != 2 {
		t.Errorf("expected Attempts=2, got %d", result.Attempts)
	}
}

func TestDevQALoop_ExhaustsMaxRetries(t *testing.T) {
	// MaxRetries=1 means 2 total attempts. Both reviews reject.
	exec := &mockExecutor{
		results: []dispatch.TaskResult{
			{Status: "success", Output: "attempt 1 output"},
			{Status: "success", Output: reviewJSON("fix", "still broken")},
			{Status: "success", Output: "attempt 2 output"},
			{Status: "success", Output: reviewJSON("fix", "still broken")},
		},
	}

	tbCfg := config.TaskBoardConfig{
		MaxRetries: 1,
		AutoDispatch: config.TaskBoardDispatchConfig{
			ReviewLoop: true,
		},
	}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor: exec,
	})

	task, err := d.engine.CreateTask(TaskBoard{
		Title:    "exhausts retries task",
		Status:   "todo",
		Assignee: "ruri",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	dispTask := dispatch.Task{
		Name:   "board:" + task.ID,
		Prompt: "do the thing",
		Agent:  task.Assignee,
		Source: "taskboard",
	}

	result := d.devQALoop(context.Background(), task, dispTask, false, "")

	if result.QAApproved {
		t.Errorf("expected QAApproved=false, got true")
	}
	// MaxRetries=1 → loop runs for attempt=0 and attempt=1 (2 total attempts).
	if result.Attempts != 2 {
		t.Errorf("expected Attempts=2, got %d", result.Attempts)
	}
}

// =============================================================================
// postTaskProblemScan tests
// =============================================================================

func TestPostTaskProblemScan_Disabled(t *testing.T) {
	exec := &mockExecutor{}

	tbCfg := config.TaskBoardConfig{
		ProblemScan: false,
	}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor: exec,
	})

	task, err := d.engine.CreateTask(TaskBoard{
		Title:  "scan disabled task",
		Status: "todo",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.postTaskProblemScan(task, "some output here", "done")

	if calls := exec.calls.Load(); calls != 0 {
		t.Errorf("expected 0 executor calls when ProblemScan=false, got %d", calls)
	}
}

func TestPostTaskProblemScan_ValidJSON(t *testing.T) {
	scanOutput := `{"problems":[{"severity":"high","summary":"null pointer","detail":"line 42"}],"followup":[{"title":"Fix null pointer","description":"add nil check","priority":"high"}]}`
	exec := &mockExecutor{
		results: []dispatch.TaskResult{
			{Status: "success", Output: scanOutput},
		},
	}

	tbCfg := config.TaskBoardConfig{
		ProblemScan: true,
	}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor: exec,
	})

	task, err := d.engine.CreateTask(TaskBoard{
		Title:   "valid json scan task",
		Status:  "done",
		Project: "myproject",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.postTaskProblemScan(task, "some output that has problems", "done")

	// Verify a comment was added to the original task.
	comments, err := d.engine.GetThread(task.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	found := false
	for _, c := range comments {
		if c.Author == "system" && len(c.Content) > 0 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a system comment to be added to the task after problem scan")
	}

	// Verify a follow-up task was created.
	followups, err := d.engine.ListTasks("backlog", "", "myproject")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(followups) != 1 {
		t.Errorf("expected 1 follow-up task, got %d", len(followups))
	}
}

func TestPostTaskProblemScan_MalformedJSON(t *testing.T) {
	exec := &mockExecutor{
		results: []dispatch.TaskResult{
			{Status: "success", Output: "not json at all"},
		},
	}

	tbCfg := config.TaskBoardConfig{
		ProblemScan: true,
	}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor: exec,
	})

	task, err := d.engine.CreateTask(TaskBoard{
		Title:  "malformed json task",
		Status: "done",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Must not panic or crash.
	d.postTaskProblemScan(task, "some output", "done")

	// No comment should be added (malformed JSON → early return).
	comments, err := d.engine.GetThread(task.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	for _, c := range comments {
		if c.Author == "system" {
			t.Errorf("expected no system comment on malformed JSON, got: %s", c.Content)
		}
	}
}

func TestPostTaskProblemScan_CapsFollowup(t *testing.T) {
	// 5 follow-up items in response — only 3 should be created.
	var followups []map[string]string
	for i := 0; i < 5; i++ {
		followups = append(followups, map[string]string{
			"title":       fmt.Sprintf("followup %d", i+1),
			"description": "fix something",
			"priority":    "normal",
		})
	}
	scanResult := map[string]any{
		"problems": []any{},
		"followup": followups,
	}
	scanJSON, _ := json.Marshal(scanResult)

	exec := &mockExecutor{
		results: []dispatch.TaskResult{
			{Status: "success", Output: string(scanJSON)},
		},
	}

	tbCfg := config.TaskBoardConfig{
		ProblemScan: true,
	}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor: exec,
	})

	task, err := d.engine.CreateTask(TaskBoard{
		Title:   "cap test task",
		Status:  "done",
		Project: "capproject",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.postTaskProblemScan(task, "some output", "done")

	created, err := d.engine.ListTasks("backlog", "", "capproject")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(created) != 3 {
		t.Errorf("expected 3 follow-up tasks (capped), got %d", len(created))
	}
}

// =============================================================================
// postTaskSkillFailures tests
// =============================================================================

func TestPostTaskSkillFailures_EmptyError(t *testing.T) {
	skills := &mockSkills{
		skills: []config.SkillConfig{{Name: "my-skill"}},
	}

	tbCfg := config.TaskBoardConfig{}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Skills: skills,
	})

	task := TaskBoard{ID: "t1", Title: "some task", Assignee: "ruri"}
	dispTask := dispatch.Task{Name: "board:t1"}

	d.postTaskSkillFailures(task, dispTask, "")

	if len(skills.appendedSkills) != 0 {
		t.Errorf("expected no AppendFailure calls for empty errMsg, got %d", len(skills.appendedSkills))
	}
}

func TestPostTaskSkillFailures_RecordsFailure(t *testing.T) {
	skills := &mockSkills{
		skills: []config.SkillConfig{{Name: "my-skill"}},
	}

	tbCfg := config.TaskBoardConfig{}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Skills: skills,
	})

	task := TaskBoard{ID: "t1", Title: "some task", Assignee: "ruri"}
	dispTask := dispatch.Task{Name: "board:t1"}

	d.postTaskSkillFailures(task, dispTask, "something went wrong")

	if len(skills.appendedSkills) != 1 {
		t.Fatalf("expected 1 AppendFailure call, got %d", len(skills.appendedSkills))
	}
	if skills.appendedSkills[0] != "my-skill" {
		t.Errorf("expected skill name 'my-skill', got %q", skills.appendedSkills[0])
	}
}

// =============================================================================
// Completion Status + Review integration tests
// =============================================================================

func TestThoroughReview_WithCompletionContext(t *testing.T) {
	// Reviewer sees agent's concerns and returns approve.
	ex := &mockExecutor{
		results: []dispatch.TaskResult{
			{Status: "success", Output: reviewJSON("approve", "LGTM, concerns noted")},
		},
	}

	tbCfg := config.TaskBoardConfig{}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor:    ex,
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	ctx := context.Background()
	concerns := "Agent status: DONE_WITH_CONCERNS\nConcerns: test coverage is only 40%"
	rv := d.thoroughReview(ctx, "original task prompt", "agent output here", "kokuyou", "ruri", &concerns)

	if rv.Verdict != reviewApprove {
		t.Errorf("expected reviewApprove, got %q", rv.Verdict)
	}
	// Verify the executor was called (meaning review was executed).
	if ex.calls.Load() != 1 {
		t.Errorf("expected 1 executor call, got %d", ex.calls.Load())
	}
}

func TestThoroughReview_WithoutCompletionContext(t *testing.T) {
	// No completion context — should still work (backward compat).
	ex := &mockExecutor{
		results: []dispatch.TaskResult{
			{Status: "success", Output: reviewJSON("fix", "missing error handling")},
		},
	}

	tbCfg := config.TaskBoardConfig{}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor:    ex,
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	ctx := context.Background()
	// Call without completion context (nil pointer).
	rv := d.thoroughReview(ctx, "original task", "agent output", "kokuyou", "ruri", nil)

	if rv.Verdict != reviewFix {
		t.Errorf("expected reviewFix, got %q", rv.Verdict)
	}
}

func TestThoroughReview_EmptyCompletionContext(t *testing.T) {
	// Empty string completion context — should not inject anything.
	ex := &mockExecutor{
		results: []dispatch.TaskResult{
			{Status: "success", Output: reviewJSON("escalate", "needs human")},
		},
	}

	tbCfg := config.TaskBoardConfig{}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor:    ex,
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	ctx := context.Background()
	empty := ""
	rv := d.thoroughReview(ctx, "task", "output", "agent", "ruri", &empty)

	if rv.Verdict != reviewEscalate {
		t.Errorf("expected reviewEscalate, got %q", rv.Verdict)
	}
}

// =============================================================================
// Section: Review actionable items tests
// =============================================================================

func TestThoroughReview_ActionableItemsParsed(t *testing.T) {
	items := []reviewActionableItem{
		{Action: "add unit tests for edge case", Type: "chore", Priority: "normal", Adopt: true, Assignee: "kokuyou"},
		{Action: "extract helper func", Type: "refactor", Priority: "low", Adopt: false, Reason: "too minor"},
	}
	ex := &mockExecutor{
		results: []dispatch.TaskResult{
			{Status: "success", Output: reviewJSONWithItems("approve", "looks good", items)},
		},
	}

	tbCfg := config.TaskBoardConfig{}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor:     ex,
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	ctx := context.Background()
	rv := d.thoroughReview(ctx, "task", "output", "kokuyou", "ruri", nil)

	if rv.Verdict != reviewApprove {
		t.Fatalf("expected reviewApprove, got %q", rv.Verdict)
	}
	if len(rv.ActionableItems) != 2 {
		t.Fatalf("expected 2 actionable items, got %d", len(rv.ActionableItems))
	}
	if rv.ActionableItems[0].Action != "add unit tests for edge case" {
		t.Errorf("unexpected action: %s", rv.ActionableItems[0].Action)
	}
	if rv.ActionableItems[1].Adopt != false {
		t.Errorf("expected second item adopt=false")
	}
}

func TestThoroughReview_EmptyActionableItems(t *testing.T) {
	ex := &mockExecutor{
		results: []dispatch.TaskResult{
			{Status: "success", Output: reviewJSONWithItems("approve", "all good", nil)},
		},
	}

	tbCfg := config.TaskBoardConfig{}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor:     ex,
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	rv := d.thoroughReview(context.Background(), "task", "output", "agent", "ruri", nil)

	if rv.Verdict != reviewApprove {
		t.Fatalf("expected reviewApprove, got %q", rv.Verdict)
	}
	if len(rv.ActionableItems) != 0 {
		t.Errorf("expected 0 actionable items, got %d", len(rv.ActionableItems))
	}
}

func TestThoroughReview_OldFormatBackwardCompat(t *testing.T) {
	// Old format without actionable_items field — should still parse fine.
	ex := &mockExecutor{
		results: []dispatch.TaskResult{
			{Status: "success", Output: reviewJSON("approve", "lgtm")},
		},
	}

	tbCfg := config.TaskBoardConfig{}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor:     ex,
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	rv := d.thoroughReview(context.Background(), "task", "output", "agent", "ruri", nil)

	if rv.Verdict != reviewApprove {
		t.Fatalf("expected reviewApprove, got %q", rv.Verdict)
	}
	if rv.ActionableItems != nil {
		t.Errorf("expected nil actionable items for old format, got %v", rv.ActionableItems)
	}
}

func TestSpawnReviewSubtasks_AdoptedCreatesTask(t *testing.T) {
	tbCfg := config.TaskBoardConfig{}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor:     &mockExecutor{},
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	// Create a parent task first.
	parent := TaskBoard{
		Title:   "parent task",
		Project: "test-project",
		Status:  "done",
	}
	parent, err := d.engine.CreateTask(parent)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	items := []reviewActionableItem{
		{Action: "add error handling to foo()", Type: "fix", Priority: "normal", Adopt: true, Assignee: "kokuyou"},
	}

	d.spawnReviewSubtasks(parent, items, "ruri")

	// Verify child task was created.
	children, _ := d.engine.ListTasks("todo", "", "")
	found := false
	for _, c := range children {
		if c.ParentID == parent.ID && c.Title == "add error handling to foo()" {
			found = true
			if c.Assignee != "kokuyou" {
				t.Errorf("expected assignee kokuyou, got %s", c.Assignee)
			}
			if c.Priority != "normal" {
				t.Errorf("expected priority normal, got %s", c.Priority)
			}
			if c.Type != "fix" {
				t.Errorf("expected type fix, got %s", c.Type)
			}
		}
	}
	if !found {
		t.Errorf("child task not found in todo list")
	}
}

func TestSpawnReviewSubtasks_RejectedOnlyComment(t *testing.T) {
	tbCfg := config.TaskBoardConfig{}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor:     &mockExecutor{},
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	parent := TaskBoard{
		Title:   "parent task",
		Project: "test-project",
		Status:  "done",
	}
	parent, err := d.engine.CreateTask(parent)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	items := []reviewActionableItem{
		{Action: "refactor X", Type: "refactor", Priority: "low", Adopt: false, Reason: "not worth it"},
	}

	d.spawnReviewSubtasks(parent, items, "ruri")

	// No child tasks should be created.
	children, _ := d.engine.ListTasks("todo", "", "")
	for _, c := range children {
		if c.ParentID == parent.ID {
			t.Errorf("unexpected child task created for rejected item: %s", c.Title)
		}
	}

	// A rejection comment must be written on the parent task.
	thread, err := d.engine.GetThread(parent.ID)
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	found := false
	for _, c := range thread {
		if strings.Contains(c.Content, "Rejected") && strings.Contains(c.Content, "refactor X") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected rejection comment on parent task, got %d comments", len(thread))
	}
}

func TestSpawnReviewSubtasks_EmptyItems(t *testing.T) {
	tbCfg := config.TaskBoardConfig{}
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor:     &mockExecutor{},
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	parent := TaskBoard{Title: "parent", Status: "done"}
	parent, _ = d.engine.CreateTask(parent)

	// Should not panic or error with nil/empty items.
	d.spawnReviewSubtasks(parent, nil, "ruri")
	d.spawnReviewSubtasks(parent, []reviewActionableItem{}, "ruri")
}

func TestSpawnReviewSubtasks_DefaultAssigneeAndPriority(t *testing.T) {
	tbCfg := config.TaskBoardConfig{}
	tbCfg.AutoDispatch.DefaultAgent = "spinel"
	d := newTestDispatcher(t, tbCfg, DispatcherDeps{
		Executor:     &mockExecutor{},
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	parent := TaskBoard{Title: "parent", Project: "proj", Status: "done"}
	parent, _ = d.engine.CreateTask(parent)

	items := []reviewActionableItem{
		{Action: "add docs", Type: "", Priority: "", Adopt: true, Assignee: ""},
	}

	d.spawnReviewSubtasks(parent, items, "ruri")

	children, _ := d.engine.ListTasks("todo", "", "")
	for _, c := range children {
		if c.ParentID == parent.ID {
			if c.Assignee != "spinel" {
				t.Errorf("expected fallback assignee spinel, got %s", c.Assignee)
			}
			if c.Priority != "low" {
				t.Errorf("expected fallback priority low, got %s", c.Priority)
			}
			if c.Type != "chore" {
				t.Errorf("expected fallback type chore, got %s", c.Type)
			}
			return
		}
	}
	t.Errorf("child task not found")
}

// =============================================================================
// Worktree gate tests
// =============================================================================

// taskComments returns all system comments for a task.
func taskComments(t *testing.T, d *Dispatcher, taskID string) []TaskComment {
	t.Helper()
	comments, err := d.engine.GetThread(taskID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	return comments
}

// hasComment returns true if any comment content contains substr.
func hasComment(comments []TaskComment, substr string) bool {
	for _, c := range comments {
		if strings.Contains(c.Content, substr) {
			return true
		}
	}
	return false
}

// TestWorktreeGate_SkippedWhenDisabled verifies that no worktree is created
// when GitWorktree=false, even when a git repo workdir is provided.
func TestWorktreeGate_SkippedWhenDisabled(t *testing.T) {
	repoDir := initGitRepo(t)
	wt := &mockWorktrees{}

	tbCfg := config.TaskBoardConfig{GitWorktree: false}
	cfg := &config.Config{WorkspaceDir: repoDir}
	d := newTestDispatcherWithConfig(t, tbCfg, cfg, DispatcherDeps{
		Executor:     &mockExecutor{results: []dispatch.TaskResult{{Status: "success", Output: "ok"}}},
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
		Worktrees:    wt,
	})

	task, err := d.engine.CreateTask(TaskBoard{Title: "gate-disabled", Status: "todo", Assignee: "ruri"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.dispatchTask(task)

	if len(wt.created) != 0 {
		t.Errorf("expected no worktree created, got %v", wt.created)
	}
	comments := taskComments(t, d, task.ID)
	if hasComment(comments, "[worktree] Running in isolated worktree") {
		t.Errorf("unexpected worktree comment when GitWorktree=false")
	}
}

// TestWorktreeGate_SkippedWhenNotGitRepo verifies that no worktree is created
// when the workdir exists but is not a git repository.
func TestWorktreeGate_SkippedWhenNotGitRepo(t *testing.T) {
	nonGitDir := t.TempDir() // plain dir, not a git repo
	wt := &mockWorktrees{}

	tbCfg := config.TaskBoardConfig{GitWorktree: true}
	cfg := &config.Config{WorkspaceDir: nonGitDir}
	d := newTestDispatcherWithConfig(t, tbCfg, cfg, DispatcherDeps{
		Executor:     &mockExecutor{results: []dispatch.TaskResult{{Status: "success", Output: "ok"}}},
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
		Worktrees:    wt,
	})

	task, err := d.engine.CreateTask(TaskBoard{Title: "non-git-dir", Status: "todo", Assignee: "ruri"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.dispatchTask(task)

	if len(wt.created) != 0 {
		t.Errorf("expected no worktree for non-git dir, got %v", wt.created)
	}
}

// TestWorktreeGate_CreatesWorktreeForGitRepo verifies that a worktree is created
// and the task comment is written when all gate conditions are met.
func TestWorktreeGate_CreatesWorktreeForGitRepo(t *testing.T) {
	repoDir := initGitRepo(t)
	wt := &mockWorktrees{}

	tbCfg := config.TaskBoardConfig{GitWorktree: true}
	cfg := &config.Config{WorkspaceDir: repoDir}
	d := newTestDispatcherWithConfig(t, tbCfg, cfg, DispatcherDeps{
		Executor:     &mockExecutor{results: []dispatch.TaskResult{{Status: "success", Output: "ok"}}},
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
		Worktrees:    wt,
	})

	task, err := d.engine.CreateTask(TaskBoard{Title: "worktree-test", Status: "todo", Assignee: "ruri"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.dispatchTask(task)

	if len(wt.created) == 0 {
		t.Error("expected worktree to be created, none found")
	}
	comments := taskComments(t, d, task.ID)
	if !hasComment(comments, "[worktree] Running in isolated worktree") {
		t.Errorf("expected worktree comment, got: %v", comments)
	}
}

// TestWorktreeGate_FallbackOnCreationError verifies that when worktree creation
// fails, the task continues using the shared workdir and a failure comment is added.
func TestWorktreeGate_FallbackOnCreationError(t *testing.T) {
	repoDir := initGitRepo(t)
	wt := &mockWorktrees{
		createFn: func(_, _, _ string) (string, error) {
			return "", errors.New("disk full")
		},
	}

	tbCfg := config.TaskBoardConfig{GitWorktree: true}
	cfg := &config.Config{WorkspaceDir: repoDir}
	d := newTestDispatcherWithConfig(t, tbCfg, cfg, DispatcherDeps{
		Executor:     &mockExecutor{results: []dispatch.TaskResult{{Status: "success", Output: "ok"}}},
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
		Worktrees:    wt,
	})

	task, err := d.engine.CreateTask(TaskBoard{Title: "worktree-fail", Status: "todo", Assignee: "ruri"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.dispatchTask(task)

	comments := taskComments(t, d, task.ID)
	if !hasComment(comments, "Failed to create isolated worktree") {
		t.Errorf("expected failure comment, got: %v", comments)
	}
	if hasComment(comments, "[worktree] Running in isolated worktree") {
		t.Errorf("unexpected success comment when creation failed")
	}
}

// =============================================================================
// scanReviews stale escalation tests
// =============================================================================

func TestScanReviews_StaleEscalatedReviewAutoApproved(t *testing.T) {
	// Given a task in "review" assigned to escalateUser for >4h
	tbCfg := config.TaskBoardConfig{
		AutoDispatch: config.TaskBoardDispatchConfig{
			ReviewLoop:       true,
			EscalateAssignee: "takuma",
			ReviewAgent:      "ruri",
		},
	}
	cfg := &config.Config{
		Agents: map[string]config.AgentConfig{
			"ruri": {Description: "reviewer"},
		},
	}
	d := newTestDispatcherWithConfig(t, tbCfg, cfg, DispatcherDeps{
		Executor: &mockExecutor{},
	})
	d.ctx = context.Background()

	// Create a task stuck in review, assigned to escalateUser.
	task, err := d.engine.CreateTask(TaskBoard{
		Title:    "stuck escalated task",
		Status:   "review",
		Assignee: "takuma",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Backdate updated_at to 5 hours ago so it exceeds the 4h threshold.
	fiveHoursAgo := time.Now().UTC().Add(-5 * time.Hour).Format(time.RFC3339)
	if err := db.Exec(d.engine.dbPath,
		fmt.Sprintf("UPDATE tasks SET updated_at = '%s' WHERE id = '%s'",
			db.Escape(fiveHoursAgo), db.Escape(task.ID))); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	// When scanReviews runs
	d.scanReviews()

	// Then the task should be auto-approved (status=done)
	updated, err := d.engine.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if updated.Status != "done" {
		t.Errorf("expected status 'done', got %q", updated.Status)
	}

	// And an auto-approval comment should exist
	comments := taskComments(t, d, task.ID)
	if !hasComment(comments, "Escalated review unhandled") {
		t.Errorf("expected auto-approval comment, got: %v", comments)
	}
}

func TestScanReviews_RecentEscalatedReviewNotAutoApproved(t *testing.T) {
	// Given a task in "review" assigned to escalateUser for <4h
	tbCfg := config.TaskBoardConfig{
		AutoDispatch: config.TaskBoardDispatchConfig{
			ReviewLoop:       true,
			EscalateAssignee: "takuma",
			ReviewAgent:      "ruri",
		},
	}
	cfg := &config.Config{
		Agents: map[string]config.AgentConfig{
			"ruri": {Description: "reviewer"},
		},
	}
	d := newTestDispatcherWithConfig(t, tbCfg, cfg, DispatcherDeps{
		Executor: &mockExecutor{},
	})
	d.ctx = context.Background()

	// Create a task in review, assigned to escalateUser, updated recently.
	task, err := d.engine.CreateTask(TaskBoard{
		Title:    "recent escalated task",
		Status:   "review",
		Assignee: "takuma",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// When scanReviews runs (task was just created, well within 4h)
	d.scanReviews()

	// Then the task should NOT be auto-approved
	updated, err := d.engine.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if updated.Status != "review" {
		t.Errorf("expected status 'review' (unchanged), got %q", updated.Status)
	}
}

// TestTruncStr_ASCIIText verifies truncStr handles ASCII text correctly.
func TestTruncStr_ASCIIText(t *testing.T) {
	result := truncStr("hello world", 5)
	if result != "hello" {
		t.Errorf("expected 'hello', got '%s'", result)
	}
	if len([]rune(result)) != 5 {
		t.Errorf("expected 5 runes, got %d", len([]rune(result)))
	}
}

// TestTruncStr_CJKText verifies truncStr handles CJK characters without breaking multi-byte sequences.
func TestTruncStr_CJKText(t *testing.T) {
	// 漢字 is 2 CJK characters = 6 bytes in UTF-8
	input := "漢字"
	result := truncStr(input, 2)
	if result != "漢字" {
		t.Errorf("expected '漢字', got '%s'", result)
	}
	// Verify no U+FFFD in JSON encoding
	data, err := json.Marshal(map[string]string{"text": result})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(data), "\ufffd") {
		t.Errorf("found U+FFFD replacement character in JSON: %s", string(data))
	}
}

// TestTruncStr_MixedASCIIAndCJK verifies truncStr handles mixed ASCII and CJK text.
func TestTruncStr_MixedASCIIAndCJK(t *testing.T) {
	input := "ABC漢字XYZ"
	result := truncStr(input, 5)
	expected := "ABC漢字"
	if result != expected {
		t.Errorf("expected '%s', got '%s'", expected, result)
	}
	// Verify no U+FFFD in JSON encoding
	data, err := json.Marshal(map[string]string{"text": result})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(data), "\ufffd") {
		t.Errorf("found U+FFFD replacement character in JSON: %s", string(data))
	}
}

// =============================================================================
// resolveRegions tests
// =============================================================================

func TestResolveRegions_TaskWorkdirsTakesPrecedence(t *testing.T) {
	cfg := &config.Config{
		WorkspaceDir: "/workspace",
	}
	got := resolveRegions([]string{"/workspace/frontend", "/workspace/backend"}, "/workspace", cfg)
	if len(got) != 2 || got[0] != "/workspace/frontend" || got[1] != "/workspace/backend" {
		t.Errorf("expected task workdirs, got %v", got)
	}
}

func TestResolveRegions_FallsBackToProjectWorkdir(t *testing.T) {
	cfg := &config.Config{
		WorkspaceDir: "/workspace",
	}
	got := resolveRegions(nil, "/workspace/myproject", cfg)
	if len(got) != 1 || got[0] != "/workspace/myproject" {
		t.Errorf("expected project workdir, got %v", got)
	}
}

func TestResolveRegions_FallsBackToWorkspaceDir(t *testing.T) {
	cfg := &config.Config{
		WorkspaceDir: "/workspace",
	}
	got := resolveRegions(nil, "", cfg)
	if len(got) != 1 || got[0] != "/workspace" {
		t.Errorf("expected workspace dir, got %v", got)
	}
}

func TestResolveRegions_EmptyTaskWorkdirsIgnored(t *testing.T) {
	cfg := &config.Config{
		WorkspaceDir: "/workspace",
	}
	// Empty slice (not nil) should also fall back.
	got := resolveRegions([]string{}, "/workspace/proj", cfg)
	if len(got) != 1 || got[0] != "/workspace/proj" {
		t.Errorf("expected project workdir when taskWorkdirs is empty, got %v", got)
	}
}

// =============================================================================
// dispatchTask empty-workdir warning tests
// =============================================================================

// TestDispatchTask_EmptyWorkdirWarning asserts that when a project exists but has
// no workdir configured, dispatchTask emits a log.Warn and adds a system comment
// that includes the fix hint. This exercises the else-if branch in processing.go
// that was previously covered only by integration-level dispatch flows.
func TestDispatchTask_EmptyWorkdirWarning(t *testing.T) {
	// Redirect the default logger to a buffer so we can assert on log output.
	var buf bytes.Buffer
	origLogger := log.Default()
	log.SetDefault(log.New(log.LevelWarn, log.FormatText, &buf))
	t.Cleanup(func() { log.SetDefault(origLogger) })

	const projectName = "no-workdir-project"

	cfg := &config.Config{
		WorkspaceDir: t.TempDir(), // must be non-empty so the fallback path is exercised
	}
	deps := DispatcherDeps{
		GetProject: func(_, id string) *ProjectInfo {
			if id == projectName {
				// Project found but Workdir is intentionally empty.
				return &ProjectInfo{Name: projectName, Workdir: ""}
			}
			return nil
		},
	}
	d := newTestDispatcherWithConfig(t, config.TaskBoardConfig{}, cfg, deps)

	task, err := d.engine.CreateTask(TaskBoard{
		Title:    "task with empty-workdir project",
		Status:   "todo",
		Assignee: "kokuyou",
		Project:  projectName,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// dispatchTask is synchronous; Executor is nil so actual agent execution is skipped.
	d.dispatchTask(task)

	// --- assert log.Warn was called ---
	logOutput := buf.String()
	if !strings.Contains(logOutput, "project has no workdir configured") {
		t.Errorf("expected warn log containing %q, got: %q",
			"project has no workdir configured", logOutput)
	}

	// --- assert system comment was added ---
	comments, err := d.engine.GetThread(task.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	var found bool
	for _, c := range comments {
		if c.Author == "system" && strings.Contains(c.Content, "has no workdir configured") {
			found = true
			break
		}
	}
	if !found {
		var contents []string
		for _, c := range comments {
			contents = append(contents, fmt.Sprintf("[%s] %s", c.Author, c.Content))
		}
		t.Errorf("expected system comment about missing workdir; comments: %v", contents)
	}
}

// =============================================================================
// Worktree failure preservation tests
// =============================================================================

// TestWorktreeFailure_HasChanges_PreservesWorktree verifies that when the executor
// returns failed but the worktree has commits, the worktree is preserved and the
// task is moved to partial-done.
func TestWorktreeFailure_HasChanges_PreservesWorktree(t *testing.T) {
	repoDir := initGitRepo(t)
	wt := &mockWorktrees{
		commitCount: 3,
		hasChanges:  false,
	}

	tbCfg := config.TaskBoardConfig{GitWorktree: true}
	cfg := &config.Config{WorkspaceDir: repoDir}
	d := newTestDispatcherWithConfig(t, tbCfg, cfg, DispatcherDeps{
		Executor:     &mockExecutor{results: []dispatch.TaskResult{{Status: "failed", Error: "timeout"}}},
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
		Worktrees:    wt,
	})

	task, err := d.engine.CreateTask(TaskBoard{Title: "wt-fail-preserve", Status: "todo", Assignee: "ruri"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.dispatchTask(task)

	// Worktree must NOT be removed.
	if len(wt.removed) != 0 {
		t.Errorf("expected worktree to be preserved (not removed), got removed: %v", wt.removed)
	}

	// Task should be moved to partial-done.
	updated, err := d.engine.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if updated.Status != "partial-done" {
		t.Errorf("expected status 'partial-done', got %q", updated.Status)
	}

	// Comment should mention commit count and preservation.
	comments := taskComments(t, d, task.ID)
	if !hasComment(comments, "Worktree preserved") {
		t.Errorf("expected preservation comment, got: %v", comments)
	}
	if !hasComment(comments, "3 commit(s)") {
		t.Errorf("expected commit count in comment, got: %v", comments)
	}
}

// TestWorktreeFailure_UncommittedOnly_PreservesWorktree verifies that when the
// executor returns failed with commitCount=0 but hasChanges=true (uncommitted
// changes only), the worktree is still preserved and the task moved to partial-done.
func TestWorktreeFailure_UncommittedOnly_PreservesWorktree(t *testing.T) {
	repoDir := initGitRepo(t)
	wt := &mockWorktrees{
		commitCount: 0,
		hasChanges:  true,
	}

	tbCfg := config.TaskBoardConfig{GitWorktree: true}
	cfg := &config.Config{WorkspaceDir: repoDir}
	d := newTestDispatcherWithConfig(t, tbCfg, cfg, DispatcherDeps{
		Executor:     &mockExecutor{results: []dispatch.TaskResult{{Status: "failed", Error: "timeout"}}},
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
		Worktrees:    wt,
	})

	task, err := d.engine.CreateTask(TaskBoard{Title: "wt-fail-uncommitted-only", Status: "todo", Assignee: "ruri"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.dispatchTask(task)

	// Worktree must NOT be removed.
	if len(wt.removed) != 0 {
		t.Errorf("expected worktree to be preserved (not removed), got removed: %v", wt.removed)
	}

	// Task should be moved to partial-done.
	updated, err := d.engine.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if updated.Status != "partial-done" {
		t.Errorf("expected status 'partial-done', got %q", updated.Status)
	}

	// Comment should mention preservation.
	comments := taskComments(t, d, task.ID)
	if !hasComment(comments, "Worktree preserved") {
		t.Errorf("expected preservation comment, got: %v", comments)
	}
}

// TestWorktreeFailure_NoChanges_DiscardsWorktree verifies that when the executor
// returns failed and the worktree has no commits or changes, the worktree is
// discarded normally.
func TestWorktreeFailure_NoChanges_DiscardsWorktree(t *testing.T) {
	repoDir := initGitRepo(t)
	wt := &mockWorktrees{
		commitCount: 0,
		hasChanges:  false,
	}

	tbCfg := config.TaskBoardConfig{GitWorktree: true}
	cfg := &config.Config{WorkspaceDir: repoDir}
	d := newTestDispatcherWithConfig(t, tbCfg, cfg, DispatcherDeps{
		Executor:     &mockExecutor{results: []dispatch.TaskResult{{Status: "failed", Error: "timeout"}}},
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
		Worktrees:    wt,
	})

	task, err := d.engine.CreateTask(TaskBoard{Title: "wt-fail-discard", Status: "todo", Assignee: "ruri"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.dispatchTask(task)

	// Worktree should be removed (mergeOK=true path).
	if len(wt.removed) == 0 {
		t.Error("expected worktree to be removed when no changes, but it was preserved")
	}

	// Task should NOT be in partial-done (stays failed).
	updated, err := d.engine.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if updated.Status == "partial-done" {
		t.Errorf("expected status NOT to be 'partial-done' when no changes, got %q", updated.Status)
	}

	// Comment should mention no changes.
	comments := taskComments(t, d, task.ID)
	if !hasComment(comments, "no changes found") {
		t.Errorf("expected 'no changes found' comment, got: %v", comments)
	}
}

// =============================================================================
// dispatchTask prompt construction tests
// =============================================================================

// capturingExecutor records the last prompt passed to RunTask.
type capturingExecutor struct {
	capturedPrompt string
	result         dispatch.TaskResult
}

func (c *capturingExecutor) RunTask(_ context.Context, task dispatch.Task, _ string) dispatch.TaskResult {
	c.capturedPrompt = task.Prompt
	return c.result
}

// TestDispatchTask_PreflightNotInjectedByProcessing verifies that processing.go
// no longer injects preflight-header.md into the prompt (dedupe fix).
// The authoritative injection lives in internal/prompt/tier.go (BuildTieredPrompt),
// which is called inside the real executor — not the mock. So the prompt captured
// by capturingExecutor must NOT contain the preflight content; if it does, the
// duplicate injection from processing.go was not removed.
func TestDispatchTask_PreflightNotInjectedByProcessing(t *testing.T) {
	agentsDir := t.TempDir()
	agentName := "kokuyou"
	if err := os.MkdirAll(filepath.Join(agentsDir, agentName), 0o755); err != nil {
		t.Fatal(err)
	}
	preflightContent := "⛔ PREFLIGHT-DEDUPE-SENTINEL"
	if err := os.WriteFile(
		filepath.Join(agentsDir, agentName, "preflight-header.md"),
		[]byte(preflightContent), 0o644,
	); err != nil {
		t.Fatal(err)
	}

	ex := &capturingExecutor{result: dispatch.TaskResult{Status: "success", Output: ""}}

	cfg := &config.Config{AgentsDir: agentsDir}
	d := newTestDispatcherWithConfig(t, config.TaskBoardConfig{}, cfg, DispatcherDeps{
		Executor:     ex,
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	task, err := d.engine.CreateTask(TaskBoard{
		Title:    "preflight dedup test",
		Status:   "todo",
		Assignee: agentName,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.dispatchTask(task)

	count := strings.Count(ex.capturedPrompt, preflightContent)
	if count != 0 {
		t.Errorf("processing.go must not inject preflight; found %d occurrence(s) in captured prompt", count)
	}
}

// TestDispatchTask_PromptContainsTaskIDTitle asserts that the prompt built by
// dispatchTask includes the exact "[task-ID] title" string required for git
// commit messages. This is a regression guard: if the formatting line in
// processing.go is accidentally changed or removed, this test will catch it.
func TestDispatchTask_PromptContainsTaskIDTitle(t *testing.T) {
	ex := &capturingExecutor{result: dispatch.TaskResult{Status: "success", Output: ""}}

	d := newTestDispatcher(t, config.TaskBoardConfig{}, DispatcherDeps{
		Executor:     ex,
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	task, err := d.engine.CreateTask(TaskBoard{
		Title:    "修正 dispatch prompt 格式（繁體中文）",
		Status:   "todo",
		Assignee: "kokuyou",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.dispatchTask(task)

	want := fmt.Sprintf("[%s] %s", task.ID, task.Title)
	if !strings.Contains(ex.capturedPrompt, want) {
		t.Errorf("prompt does not contain %q\nfull prompt:\n%s", want, ex.capturedPrompt)
	}
}

// =============================================================================
// Context cancellation → retryable (reset to todo) tests
// =============================================================================

// fixedResultExecutor always returns the same result, regardless of call count.
type fixedResultExecutor struct {
	result dispatch.TaskResult
}

func (f *fixedResultExecutor) RunTask(_ context.Context, _ dispatch.Task, _ string) dispatch.TaskResult {
	return f.result
}

func TestDispatchTask_ContextCanceled_ResetsToTodo_NoRetryBurn(t *testing.T) {
	// When the executor returns an error containing "context canceled"
	// (e.g. daemon shutdown), the task should be reset to "todo" — and
	// retry_count must NOT be incremented (context cancellation is
	// infrastructure noise, not a task failure).
	ex := &fixedResultExecutor{result: dispatch.TaskResult{
		Status: "error",
		Error:  "context canceled",
		Output: "partial output before cancellation",
	}}

	d := newTestDispatcher(t, config.TaskBoardConfig{}, DispatcherDeps{
		Executor:     ex,
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	task, err := d.engine.CreateTask(TaskBoard{
		Title:    "task interrupted by daemon shutdown",
		Status:   "todo",
		Assignee: "kokuyou",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.dispatchTask(task)

	got, err := d.engine.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != "todo" {
		t.Errorf("expected status 'todo' after context canceled, got %q", got.Status)
	}
	// Context cancellation must NOT burn a retry — retry_count should remain 0.
	if got.RetryCount != 0 {
		t.Errorf("expected retry_count 0 (no retry burn), got %d", got.RetryCount)
	}

	// Verify system comment was added.
	comments, _ := d.engine.GetThread(task.ID)
	var found bool
	for _, c := range comments {
		if c.Author == "system" && strings.Contains(c.Content, "[auto-reset]") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected [auto-reset] system comment for context cancellation")
	}
}

func TestDispatchTask_ContextCanceled_WrappedError_ResetsToTodo(t *testing.T) {
	// Errors wrapping "context canceled" deeper in the message should also be retryable.
	ex := &fixedResultExecutor{result: dispatch.TaskResult{
		Status: "error",
		Error:  "dispatch failed: rpc error: context canceled: deadline exceeded",
		Output: "partial",
	}}

	d := newTestDispatcher(t, config.TaskBoardConfig{}, DispatcherDeps{
		Executor:     ex,
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	task, err := d.engine.CreateTask(TaskBoard{
		Title:    "task with wrapped context canceled error",
		Status:   "todo",
		Assignee: "kokuyou",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.dispatchTask(task)

	got, err := d.engine.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != "todo" {
		t.Errorf("expected status 'todo' after wrapped context canceled, got %q", got.Status)
	}
	if got.RetryCount != 0 {
		t.Errorf("expected retry_count 0 (no retry burn for context cancel), got %d", got.RetryCount)
	}
}

func TestDispatchTask_RealError_AutoRetried(t *testing.T) {
	// A genuine runtime error (not context cancellation) goes through normal
	// failure → AutoRetryFailed path, which resets to "todo" but increments
	// retry_count. This test verifies the retry_count IS incremented.
	ex := &fixedResultExecutor{result: dispatch.TaskResult{
		Status: "error",
		Error:  "runtime error: index out of range [5] with length 3",
		Output: "some output",
	}}

	d := newTestDispatcher(t, config.TaskBoardConfig{}, DispatcherDeps{
		Executor:     ex,
		FillDefaults: func(_ *config.Config, _ *dispatch.Task) {},
	})

	task, err := d.engine.CreateTask(TaskBoard{
		Title:    "task with real runtime error",
		Status:   "todo",
		Assignee: "kokuyou",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	d.dispatchTask(task)

	got, err := d.engine.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// AutoRetryFailed resets to "todo" but increments retry_count.
	if got.Status != "todo" {
		t.Errorf("expected status 'todo' after auto-retry, got %q", got.Status)
	}
	if got.RetryCount != 1 {
		t.Errorf("expected retry_count 1 (auto-retry increments), got %d", got.RetryCount)
	}
}
