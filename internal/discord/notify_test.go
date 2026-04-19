package discord

import (
	"testing"

	dtypes "tetora/internal/dispatch"
)

func TestResolveLevel_DefaultsToThread(t *testing.T) {
	n := &TaskNotifier{opts: NotifyOptions{}}
	task := dtypes.Task{ID: "t1", Agent: "ruri", Name: "anything"}
	for _, event := range []string{"start", "ok", "fail"} {
		if got := n.resolveLevel(task, event); got != LevelThread {
			t.Errorf("event=%s: got %q, want %q", event, got, LevelThread)
		}
	}
}

func TestResolveLevel_TopLevelOverride(t *testing.T) {
	n := &TaskNotifier{opts: NotifyOptions{
		TaskStart:        LevelOff,
		TaskCompleteOk:   LevelOff,
		TaskCompleteFail: LevelChannel,
	}}
	task := dtypes.Task{ID: "t1", Agent: "ruri", Name: "cron-job"}
	if got := n.resolveLevel(task, "start"); got != LevelOff {
		t.Errorf("start: got %q, want off", got)
	}
	if got := n.resolveLevel(task, "ok"); got != LevelOff {
		t.Errorf("ok: got %q, want off", got)
	}
	if got := n.resolveLevel(task, "fail"); got != LevelChannel {
		t.Errorf("fail: got %q, want channel", got)
	}
}

func TestResolveLevel_OverrideWins(t *testing.T) {
	n := &TaskNotifier{opts: NotifyOptions{
		TaskCompleteOk: LevelOff, // silence default success
		Overrides: []NotifyOverride{
			{
				Match:          NotifyMatch{NameContains: "War Room"},
				TaskCompleteOk: LevelChannel,
			},
		},
	}}
	warRoom := dtypes.Task{ID: "t1", Agent: "ruri", Name: "War Room Daily"}
	other := dtypes.Task{ID: "t2", Agent: "ruri", Name: "Something Else"}

	if got := n.resolveLevel(warRoom, "ok"); got != LevelChannel {
		t.Errorf("warRoom ok: got %q, want channel", got)
	}
	if got := n.resolveLevel(other, "ok"); got != LevelOff {
		t.Errorf("other ok: got %q, want off", got)
	}
}

func TestResolveLevel_OverrideInheritsEmptyFields(t *testing.T) {
	// Override sets only TaskCompleteOk; TaskStart/Fail should fall through to
	// top-level config, not to override defaults.
	n := &TaskNotifier{opts: NotifyOptions{
		TaskStart:        LevelOff,
		TaskCompleteFail: LevelOff,
		Overrides: []NotifyOverride{
			{
				Match:          NotifyMatch{Agent: "ruri"},
				TaskCompleteOk: LevelChannel,
			},
		},
	}}
	task := dtypes.Task{ID: "t1", Agent: "ruri", Name: "x"}
	if got := n.resolveLevel(task, "start"); got != LevelOff {
		t.Errorf("start: got %q, want off (inherited from top-level)", got)
	}
	if got := n.resolveLevel(task, "fail"); got != LevelOff {
		t.Errorf("fail: got %q, want off (inherited from top-level)", got)
	}
	if got := n.resolveLevel(task, "ok"); got != LevelChannel {
		t.Errorf("ok: got %q, want channel (from override)", got)
	}
}

func TestResolveLevel_FirstMatchWins(t *testing.T) {
	n := &TaskNotifier{opts: NotifyOptions{
		Overrides: []NotifyOverride{
			{
				Match:          NotifyMatch{Agent: "ruri"},
				TaskCompleteOk: LevelChannel,
			},
			{
				Match:          NotifyMatch{NameContains: "War Room"},
				TaskCompleteOk: LevelOff,
			},
		},
	}}
	task := dtypes.Task{ID: "t1", Agent: "ruri", Name: "War Room Daily"}
	if got := n.resolveLevel(task, "ok"); got != LevelChannel {
		t.Errorf("got %q, want channel (first match wins)", got)
	}
}

func TestMatchTask(t *testing.T) {
	task := dtypes.Task{ID: "job-xyz", Agent: "ruri", Name: "War Room Daily"}

	cases := []struct {
		name  string
		match NotifyMatch
		want  bool
	}{
		{"empty match never matches", NotifyMatch{}, false},
		{"agent exact", NotifyMatch{Agent: "ruri"}, true},
		{"agent miss", NotifyMatch{Agent: "kokuyou"}, false},
		{"jobId exact", NotifyMatch{JobID: "job-xyz"}, true},
		{"jobId miss", NotifyMatch{JobID: "other"}, false},
		{"name substring", NotifyMatch{NameContains: "War"}, true},
		{"name substring case-sensitive miss", NotifyMatch{NameContains: "war"}, false},
		{"name miss", NotifyMatch{NameContains: "Morning"}, false},
		{"all three match", NotifyMatch{Agent: "ruri", JobID: "job-xyz", NameContains: "War"}, true},
		{"AND: one miss fails", NotifyMatch{Agent: "ruri", NameContains: "Morning"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchTask(tc.match, task); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "c"); got != "c" {
		t.Errorf("got %q, want c", got)
	}
	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Errorf("got %q, want a", got)
	}
	if got := firstNonEmpty(); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
