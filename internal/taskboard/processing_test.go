package taskboard

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"tetora/internal/config"
	"tetora/internal/dispatch"
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
	dbPath := t.TempDir() + "/test.db"
	engine := NewEngine(dbPath, tbCfg, nil)
	if err := engine.InitSchema(); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	cfg := &config.Config{}
	return NewDispatcher(engine, cfg, deps)
}

// reviewJSON builds a review JSON response string.
func reviewJSON(verdict, comment string) string {
	b, _ := json.Marshal(map[string]string{"verdict": verdict, "comment": comment})
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
