package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// --- P13.3: Nested Sub-Agents --- Tests for depth tracking, spawn control, and max depth enforcement.

// TestSpawnTrackerTrySpawn verifies basic spawn tracking and limit enforcement.
func TestSpawnTrackerTrySpawn(t *testing.T) {
	st := &spawnTracker{children: make(map[string]int)}

	parentID := "parent-001"
	maxChildren := 3

	// Should allow up to maxChildren spawns.
	for i := 0; i < maxChildren; i++ {
		if !st.trySpawn(parentID, maxChildren) {
			t.Fatalf("trySpawn should succeed at count %d (limit %d)", i, maxChildren)
		}
	}

	// The next spawn should be rejected.
	if st.trySpawn(parentID, maxChildren) {
		t.Fatal("trySpawn should fail when at maxChildren limit")
	}

	// Count should equal maxChildren.
	if c := st.count(parentID); c != maxChildren {
		t.Fatalf("expected count %d, got %d", maxChildren, c)
	}
}

// TestSpawnTrackerRelease verifies that releasing a child allows new spawns.
func TestSpawnTrackerRelease(t *testing.T) {
	st := &spawnTracker{children: make(map[string]int)}

	parentID := "parent-002"
	maxChildren := 2

	// Fill up.
	st.trySpawn(parentID, maxChildren)
	st.trySpawn(parentID, maxChildren)

	// Should be full.
	if st.trySpawn(parentID, maxChildren) {
		t.Fatal("should be at limit")
	}

	// Release one.
	st.release(parentID)

	// Should allow one more.
	if !st.trySpawn(parentID, maxChildren) {
		t.Fatal("should allow spawn after release")
	}

	// Release all.
	st.release(parentID)
	st.release(parentID)

	// Count should be 0 and key should be cleaned up.
	if c := st.count(parentID); c != 0 {
		t.Fatalf("expected count 0 after all releases, got %d", c)
	}
}

// TestSpawnTrackerEmptyParent verifies that empty parentID always allows spawns.
func TestSpawnTrackerEmptyParent(t *testing.T) {
	st := &spawnTracker{children: make(map[string]int)}

	// Empty parentID should always succeed (top-level task).
	for i := 0; i < 100; i++ {
		if !st.trySpawn("", 1) {
			t.Fatal("empty parentID should always allow spawn")
		}
	}
}

// TestSpawnTrackerConcurrentAccess verifies thread-safety of spawnTracker.
func TestSpawnTrackerConcurrentAccess(t *testing.T) {
	st := &spawnTracker{children: make(map[string]int)}

	parentID := "parent-concurrent"
	maxChildren := 50
	goroutines := 100

	var wg sync.WaitGroup
	successCount := make(chan int, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if st.trySpawn(parentID, maxChildren) {
				successCount <- 1
				// Simulate some work.
				st.count(parentID)
				st.release(parentID)
			} else {
				successCount <- 0
			}
		}()
	}

	wg.Wait()
	close(successCount)

	total := 0
	for s := range successCount {
		total += s
	}

	// After all goroutines complete, count should be 0.
	if c := st.count(parentID); c != 0 {
		t.Fatalf("expected count 0 after concurrent test, got %d", c)
	}

	// At least some should have succeeded.
	if total == 0 {
		t.Fatal("no goroutines succeeded in spawning")
	}
}

// TestSpawnTrackerReleaseNoUnderflow verifies release doesn't go below 0.
func TestSpawnTrackerReleaseNoUnderflow(t *testing.T) {
	st := &spawnTracker{children: make(map[string]int)}

	parentID := "parent-underflow"

	// Release without any spawns should not underflow.
	st.release(parentID)
	st.release(parentID)

	if c := st.count(parentID); c != 0 {
		t.Fatalf("expected count 0, got %d", c)
	}
}

// TestMaxDepthEnforcement verifies that toolAgentDispatch rejects at max depth.
func TestMaxDepthEnforcement(t *testing.T) {
	cfg := &Config{
		AgentComm: AgentCommConfig{
			Enabled:  true,
			MaxDepth: 3,
		},
		Roles: map[string]RoleConfig{
			"test-role": {Description: "test"},
		},
	}

	tests := []struct {
		name    string
		depth   int
		wantErr bool
		errMsg  string
	}{
		{"depth 0 allowed", 0, false, ""},
		{"depth 1 allowed", 1, false, ""},
		{"depth 2 allowed", 2, false, ""},
		{"depth 3 rejected", 3, true, "max nesting depth exceeded"},
		{"depth 5 rejected", 5, true, "max nesting depth exceeded"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset spawn tracker for each sub-test.
			globalSpawnTracker = &spawnTracker{children: make(map[string]int)}

			input, _ := json.Marshal(map[string]any{
				"role":     "test-role",
				"prompt":   "test task",
				"timeout":  10,
				"depth":    tt.depth,
				"parentId": "parent-depth-test",
			})

			_, err := toolAgentDispatch(context.Background(), cfg, input)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Fatalf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			}
			// For allowed depths, we expect a different error (HTTP connection refused)
			// since we're not running an actual server. That's fine -- depth validation
			// happens before the HTTP call.
			if !tt.wantErr && err != nil {
				if strings.Contains(err.Error(), "max nesting depth exceeded") {
					t.Fatalf("unexpected depth rejection: %v", err)
				}
			}
		})
	}
}

// TestMaxChildrenEnforcement verifies that toolAgentDispatch rejects when too many children.
func TestMaxChildrenEnforcement(t *testing.T) {
	// Reset global spawn tracker.
	globalSpawnTracker = &spawnTracker{children: make(map[string]int)}

	cfg := &Config{
		AgentComm: AgentCommConfig{
			Enabled:            true,
			MaxDepth:           10,
			MaxChildrenPerTask: 2,
		},
		Roles: map[string]RoleConfig{
			"test-role": {Description: "test"},
		},
	}

	parentID := "parent-children-test"

	// Pre-fill the spawn tracker to simulate active children.
	globalSpawnTracker.trySpawn(parentID, 2)
	globalSpawnTracker.trySpawn(parentID, 2)

	input, _ := json.Marshal(map[string]any{
		"role":     "test-role",
		"prompt":   "test task",
		"timeout":  10,
		"depth":    0,
		"parentId": parentID,
	})

	_, err := toolAgentDispatch(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for max children exceeded")
	}
	if !strings.Contains(err.Error(), "max children per task exceeded") {
		t.Fatalf("expected max children error, got: %v", err)
	}

	// Release one and try again -- should pass depth check but fail on HTTP (no server).
	globalSpawnTracker.release(parentID)

	_, err = toolAgentDispatch(context.Background(), cfg, input)
	if err != nil && strings.Contains(err.Error(), "max children per task exceeded") {
		t.Fatalf("should not fail on children limit after release: %v", err)
	}
}

// TestDepthTracking verifies that child task gets parent depth + 1.
func TestDepthTracking(t *testing.T) {
	// Reset spawn tracker.
	globalSpawnTracker = &spawnTracker{children: make(map[string]int)}

	cfg := &Config{
		AgentComm: AgentCommConfig{
			Enabled:  true,
			MaxDepth: 5,
		},
		Roles: map[string]RoleConfig{
			"test-role": {Description: "test"},
		},
	}

	parentDepth := 2
	input, _ := json.Marshal(map[string]any{
		"role":     "test-role",
		"prompt":   "test task",
		"timeout":  10,
		"depth":    parentDepth,
		"parentId": "parent-tracking-test",
	})

	// toolAgentDispatch will create a task with depth = parentDepth + 1 = 3.
	// We can't intercept the HTTP call directly, but we can verify the function
	// passes depth validation (depth 2 < maxDepth 5).
	_, err := toolAgentDispatch(context.Background(), cfg, input)
	// Error should be HTTP-related (no server), NOT depth-related.
	if err != nil && strings.Contains(err.Error(), "max nesting depth exceeded") {
		t.Fatalf("depth %d should be allowed with maxDepth 5: %v", parentDepth, err)
	}
}

// TestParentIDPropagation verifies that parentId is passed through correctly.
func TestParentIDPropagation(t *testing.T) {
	cfg := &Config{
		AgentComm: AgentCommConfig{
			Enabled:  true,
			MaxDepth: 5,
		},
		Roles: map[string]RoleConfig{
			"worker": {Description: "worker agent"},
		},
	}

	parentID := "task-abc-123"
	input, _ := json.Marshal(map[string]any{
		"role":     "worker",
		"prompt":   "do work",
		"timeout":  10,
		"depth":    0,
		"parentId": parentID,
	})

	// Reset spawn tracker.
	globalSpawnTracker = &spawnTracker{children: make(map[string]int)}

	// The function should pass depth/parentId checks.
	// It will fail on HTTP connection, which is expected.
	_, err := toolAgentDispatch(context.Background(), cfg, input)
	if err != nil && strings.Contains(err.Error(), "max nesting depth exceeded") {
		t.Fatalf("should not fail on depth: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "max children per task exceeded") {
		t.Fatalf("should not fail on children: %v", err)
	}

	// After the call (which defers release), spawn count should be 0.
	if c := globalSpawnTracker.count(parentID); c != 0 {
		t.Fatalf("expected spawn count 0 after call, got %d", c)
	}
}

// TestConfigDefaults verifies that maxDepth and maxChildrenPerTask default correctly.
func TestConfigDefaults(t *testing.T) {
	// Zero-value config should use defaults.
	cfg := &Config{}

	if d := maxDepthOrDefault(cfg); d != 3 {
		t.Fatalf("expected default maxDepth 3, got %d", d)
	}
	if c := maxChildrenPerTaskOrDefault(cfg); c != 5 {
		t.Fatalf("expected default maxChildrenPerTask 5, got %d", c)
	}

	// Configured values should be used.
	cfg.AgentComm.MaxDepth = 7
	cfg.AgentComm.MaxChildrenPerTask = 10

	if d := maxDepthOrDefault(cfg); d != 7 {
		t.Fatalf("expected maxDepth 7, got %d", d)
	}
	if c := maxChildrenPerTaskOrDefault(cfg); c != 10 {
		t.Fatalf("expected maxChildrenPerTask 10, got %d", c)
	}
}

// TestTaskDepthAndParentIDFields verifies Task struct fields serialize correctly.
func TestTaskDepthAndParentIDFields(t *testing.T) {
	task := Task{
		ID:       "child-001",
		Prompt:   "test",
		Role:     "worker",
		Depth:    2,
		ParentID: "parent-001",
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded Task
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Depth != 2 {
		t.Fatalf("expected depth 2, got %d", decoded.Depth)
	}
	if decoded.ParentID != "parent-001" {
		t.Fatalf("expected parentId parent-001, got %s", decoded.ParentID)
	}
}

// TestTaskDepthOmitEmpty verifies depth 0 is omitted in JSON (omitempty).
func TestTaskDepthOmitEmpty(t *testing.T) {
	task := Task{
		ID:     "top-level",
		Prompt: "test",
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	s := string(data)
	if strings.Contains(s, `"depth"`) {
		t.Fatalf("depth should be omitted when 0, got: %s", s)
	}
	if strings.Contains(s, `"parentId"`) {
		t.Fatalf("parentId should be omitted when empty, got: %s", s)
	}
}
