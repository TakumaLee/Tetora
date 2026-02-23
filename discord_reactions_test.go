package main

// --- P14.3: Lifecycle Reactions Tests ---

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- Default Emoji Map ---

func TestDefaultReactionEmojis(t *testing.T) {
	emojis := defaultReactionEmojis()

	// Must have all 5 phases.
	phases := validReactionPhases()
	for _, phase := range phases {
		if emoji, ok := emojis[phase]; !ok || emoji == "" {
			t.Errorf("missing default emoji for phase %q", phase)
		}
	}

	// Verify specific defaults.
	if emojis[reactionPhaseQueued] != "\u23F3" {
		t.Errorf("expected hourglass for queued, got %q", emojis[reactionPhaseQueued])
	}
	if emojis[reactionPhaseDone] != "\u2705" {
		t.Errorf("expected check mark for done, got %q", emojis[reactionPhaseDone])
	}
	if emojis[reactionPhaseError] != "\u274C" {
		t.Errorf("expected cross mark for error, got %q", emojis[reactionPhaseError])
	}
}

// --- Reaction Manager Creation ---

func TestNewDiscordReactionManager(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)
	if rm == nil {
		t.Fatal("expected non-nil reaction manager")
	}
	if rm.defaultEmoji == nil {
		t.Error("expected default emoji map")
	}
	if rm.current == nil {
		t.Error("expected current phase map")
	}
}

func TestNewDiscordReactionManager_WithOverrides(t *testing.T) {
	overrides := map[string]string{
		"queued": "\U0001F4E5", // inbox tray
	}
	rm := newDiscordReactionManager(nil, overrides)
	if rm.overrides == nil {
		t.Error("expected overrides map")
	}
	if rm.overrides["queued"] != "\U0001F4E5" {
		t.Errorf("expected override emoji, got %q", rm.overrides["queued"])
	}
}

// --- Emoji For Phase ---

func TestEmojiForPhase_Default(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)

	tests := []struct {
		phase    string
		expected string
	}{
		{reactionPhaseQueued, "\u23F3"},
		{reactionPhaseThinking, "\U0001F914"},
		{reactionPhaseTool, "\U0001F527"},
		{reactionPhaseDone, "\u2705"},
		{reactionPhaseError, "\u274C"},
	}
	for _, tt := range tests {
		got := rm.emojiForPhase(tt.phase)
		if got != tt.expected {
			t.Errorf("emojiForPhase(%q) = %q, want %q", tt.phase, got, tt.expected)
		}
	}
}

func TestEmojiForPhase_Override(t *testing.T) {
	overrides := map[string]string{
		"queued": "\U0001F4E5", // inbox tray
		"done":   "\U0001F389", // party popper
	}
	rm := newDiscordReactionManager(nil, overrides)

	// Overridden phases.
	if got := rm.emojiForPhase("queued"); got != "\U0001F4E5" {
		t.Errorf("expected override for queued, got %q", got)
	}
	if got := rm.emojiForPhase("done"); got != "\U0001F389" {
		t.Errorf("expected override for done, got %q", got)
	}

	// Non-overridden phases fall back to default.
	if got := rm.emojiForPhase("thinking"); got != "\U0001F914" {
		t.Errorf("expected default for thinking, got %q", got)
	}
}

func TestEmojiForPhase_UnknownPhase(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)
	got := rm.emojiForPhase("unknown_phase")
	if got != "" {
		t.Errorf("expected empty for unknown phase, got %q", got)
	}
}

func TestEmojiForPhase_EmptyOverride(t *testing.T) {
	// An empty override string should fall back to default.
	overrides := map[string]string{
		"queued": "",
	}
	rm := newDiscordReactionManager(nil, overrides)
	got := rm.emojiForPhase("queued")
	if got != "\u23F3" {
		t.Errorf("expected default for empty override, got %q", got)
	}
}

// --- Reaction Key ---

func TestReactionKey(t *testing.T) {
	tests := []struct {
		channelID, messageID, expected string
	}{
		{"C123", "M456", "C123:M456"},
		{"", "M456", ":M456"},
		{"C123", "", "C123:"},
	}
	for _, tt := range tests {
		got := reactionKey(tt.channelID, tt.messageID)
		if got != tt.expected {
			t.Errorf("reactionKey(%q, %q) = %q, want %q",
				tt.channelID, tt.messageID, got, tt.expected)
		}
	}
}

// --- Phase Tracking ---

func TestSetPhase_TracksCurrentPhase(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)

	// setPhase with nil bot won't make API calls, but tracking should work.
	rm.setPhase("C1", "M1", reactionPhaseQueued)

	got := rm.getCurrentPhase("C1", "M1")
	if got != reactionPhaseQueued {
		t.Errorf("expected phase %q, got %q", reactionPhaseQueued, got)
	}
}

func TestSetPhase_TransitionUpdatesPhase(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)

	rm.setPhase("C1", "M1", reactionPhaseQueued)
	rm.setPhase("C1", "M1", reactionPhaseThinking)

	got := rm.getCurrentPhase("C1", "M1")
	if got != reactionPhaseThinking {
		t.Errorf("expected phase %q after transition, got %q", reactionPhaseThinking, got)
	}
}

func TestSetPhase_IgnoresEmptyArgs(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)

	rm.setPhase("", "M1", reactionPhaseQueued)
	rm.setPhase("C1", "", reactionPhaseQueued)
	rm.setPhase("C1", "M1", "")

	// None should be tracked.
	if got := rm.getCurrentPhase("", "M1"); got != "" {
		t.Errorf("expected empty for empty channelID, got %q", got)
	}
	if got := rm.getCurrentPhase("C1", ""); got != "" {
		t.Errorf("expected empty for empty messageID, got %q", got)
	}
}

func TestSetPhase_UnknownPhaseIgnored(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)

	rm.setPhase("C1", "M1", "nonexistent_phase")
	got := rm.getCurrentPhase("C1", "M1")
	if got != "" {
		t.Errorf("expected empty for unknown phase, got %q", got)
	}
}

// --- Clear Phase ---

func TestClearPhase(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)

	rm.setPhase("C1", "M1", reactionPhaseQueued)
	rm.clearPhase("C1", "M1")

	got := rm.getCurrentPhase("C1", "M1")
	if got != "" {
		t.Errorf("expected empty after clearPhase, got %q", got)
	}
}

func TestClearPhase_NonExistent(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)
	// Should not panic.
	rm.clearPhase("C999", "M999")
}

// --- Convenience Methods ---

func TestReactQueued(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)
	rm.reactQueued("C1", "M1")
	if got := rm.getCurrentPhase("C1", "M1"); got != reactionPhaseQueued {
		t.Errorf("expected queued, got %q", got)
	}
}

func TestReactDone_ClearsTracking(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)
	rm.setPhase("C1", "M1", reactionPhaseThinking)
	rm.reactDone("C1", "M1")
	// reactDone calls clearPhase, so tracking should be removed.
	if got := rm.getCurrentPhase("C1", "M1"); got != "" {
		t.Errorf("expected empty after reactDone, got %q", got)
	}
}

func TestReactError_ClearsTracking(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)
	rm.setPhase("C1", "M1", reactionPhaseThinking)
	rm.reactError("C1", "M1")
	if got := rm.getCurrentPhase("C1", "M1"); got != "" {
		t.Errorf("expected empty after reactError, got %q", got)
	}
}

// --- Full Lifecycle ---

func TestReactionLifecycle_FullTransition(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)

	// Simulate full lifecycle: queued -> thinking -> tool -> done.
	rm.setPhase("C1", "M1", reactionPhaseQueued)
	if got := rm.getCurrentPhase("C1", "M1"); got != reactionPhaseQueued {
		t.Fatalf("step 1: expected queued, got %q", got)
	}

	rm.setPhase("C1", "M1", reactionPhaseThinking)
	if got := rm.getCurrentPhase("C1", "M1"); got != reactionPhaseThinking {
		t.Fatalf("step 2: expected thinking, got %q", got)
	}

	rm.setPhase("C1", "M1", reactionPhaseTool)
	if got := rm.getCurrentPhase("C1", "M1"); got != reactionPhaseTool {
		t.Fatalf("step 3: expected tool, got %q", got)
	}

	rm.reactDone("C1", "M1")
	if got := rm.getCurrentPhase("C1", "M1"); got != "" {
		t.Fatalf("step 4: expected empty after done, got %q", got)
	}
}

func TestReactionLifecycle_ErrorPath(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)

	rm.setPhase("C1", "M1", reactionPhaseQueued)
	rm.setPhase("C1", "M1", reactionPhaseThinking)
	rm.reactError("C1", "M1")

	if got := rm.getCurrentPhase("C1", "M1"); got != "" {
		t.Errorf("expected empty after error, got %q", got)
	}
}

// --- Multiple Messages ---

func TestReactionManager_MultipleMessages(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)

	rm.setPhase("C1", "M1", reactionPhaseQueued)
	rm.setPhase("C1", "M2", reactionPhaseThinking)
	rm.setPhase("C2", "M3", reactionPhaseTool)

	if got := rm.getCurrentPhase("C1", "M1"); got != reactionPhaseQueued {
		t.Errorf("M1: expected queued, got %q", got)
	}
	if got := rm.getCurrentPhase("C1", "M2"); got != reactionPhaseThinking {
		t.Errorf("M2: expected thinking, got %q", got)
	}
	if got := rm.getCurrentPhase("C2", "M3"); got != reactionPhaseTool {
		t.Errorf("M3: expected tool, got %q", got)
	}
}

// --- Valid Phases ---

func TestValidReactionPhases(t *testing.T) {
	phases := validReactionPhases()
	if len(phases) != 5 {
		t.Errorf("expected 5 phases, got %d", len(phases))
	}

	expected := []string{"queued", "thinking", "tool", "done", "error"}
	for i, p := range expected {
		if phases[i] != p {
			t.Errorf("phase[%d] = %q, want %q", i, phases[i], p)
		}
	}
}

// --- Config Parsing ---

func TestDiscordReactionsConfigParse(t *testing.T) {
	raw := `{"enabled":true,"emojis":{"queued":"\u2b50","done":"\ud83c\udf89"}}`
	var cfg DiscordReactionsConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Error("expected enabled")
	}
	if cfg.Emojis == nil {
		t.Fatal("expected emojis map")
	}
	if cfg.Emojis["queued"] == "" {
		t.Error("expected queued emoji override")
	}
}

func TestDiscordReactionsConfigParse_Disabled(t *testing.T) {
	raw := `{}`
	var cfg DiscordReactionsConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Error("expected disabled by default")
	}
	if cfg.Emojis != nil {
		t.Error("expected nil emojis by default")
	}
}

// --- Phase Constants ---

func TestReactionPhaseConstants(t *testing.T) {
	// Ensure constants match expected strings.
	if reactionPhaseQueued != "queued" {
		t.Errorf("expected 'queued', got %q", reactionPhaseQueued)
	}
	if reactionPhaseThinking != "thinking" {
		t.Errorf("expected 'thinking', got %q", reactionPhaseThinking)
	}
	if reactionPhaseTool != "tool" {
		t.Errorf("expected 'tool', got %q", reactionPhaseTool)
	}
	if reactionPhaseDone != "done" {
		t.Errorf("expected 'done', got %q", reactionPhaseDone)
	}
	if reactionPhaseError != "error" {
		t.Errorf("expected 'error', got %q", reactionPhaseError)
	}
}

// --- Same Phase No-Op ---

func TestSetPhase_SamePhaseNoRemove(t *testing.T) {
	rm := newDiscordReactionManager(nil, nil)

	rm.setPhase("C1", "M1", reactionPhaseQueued)
	// Setting same phase again should not try to remove previous (no-op remove).
	rm.setPhase("C1", "M1", reactionPhaseQueued)

	got := rm.getCurrentPhase("C1", "M1")
	if got != reactionPhaseQueued {
		t.Errorf("expected queued after re-set, got %q", got)
	}
}

// --- Helper: use strings.Contains for substring checks ---

func TestReactionKeyContainsSeparator(t *testing.T) {
	key := reactionKey("C123", "M456")
	if !strings.Contains(key, ":") {
		t.Error("expected key to contain separator ':'")
	}
}
