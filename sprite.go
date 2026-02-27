package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// --- Sprite State Constants ---
// Universal agent sprite states — not tied to any specific team.

const (
	SpriteIdle      = "idle"
	SpriteWork      = "work"
	SpriteThink     = "think"
	SpriteTalk      = "talk"
	SpriteReview    = "review"
	SpriteCelebrate = "celebrate"
	SpriteError     = "error"

	SpriteWalkDown  = "walk_down"
	SpriteWalkUp    = "walk_up"
	SpriteWalkLeft  = "walk_left"
	SpriteWalkRight = "walk_right"
)

// --- Sprite Config (user-customizable) ---

// SpriteConfig describes spritesheet layout and per-agent sheet assignments.
// Loaded from ~/.tetora/media/sprites/config.json.
type SpriteConfig struct {
	CellWidth  int                        `json:"cellWidth"`
	CellHeight int                        `json:"cellHeight"`
	Background string                     `json:"background,omitempty"` // optional background PNG filename
	States     []SpriteStateDef           `json:"states"`
	Agents     map[string]AgentSpriteDef  `json:"agents"`
}

// SpriteStateDef maps a state name to a spritesheet row.
type SpriteStateDef struct {
	Name   string `json:"name"`
	Row    int    `json:"row"`
	Frames int    `json:"frames"`
}

// AgentSpriteDef holds per-agent sprite configuration.
// Two modes:
//   - Single sheet: set "sheet" — one PNG with all states as rows (uses States row mapping).
//   - Multi sheet:  set "sheets" — one PNG per state, each is a single horizontal strip.
//
// If both are set, "sheets" entries take priority for matched states; "sheet" is used as fallback.
type AgentSpriteDef struct {
	Sheet  string            `json:"sheet,omitempty"`  // single spritesheet PNG
	Sheets map[string]string `json:"sheets,omitempty"` // state name -> PNG filename
}

// defaultSpriteConfig returns the built-in sprite config with all 11 states.
func defaultSpriteConfig() SpriteConfig {
	return SpriteConfig{
		CellWidth:  32,
		CellHeight: 32,
		States: []SpriteStateDef{
			{Name: SpriteWalkDown, Row: 0, Frames: 4},
			{Name: SpriteWalkUp, Row: 1, Frames: 4},
			{Name: SpriteWalkLeft, Row: 2, Frames: 4},
			{Name: SpriteWalkRight, Row: 3, Frames: 4},
			{Name: SpriteIdle, Row: 4, Frames: 4},
			{Name: SpriteWork, Row: 5, Frames: 4},
			{Name: SpriteThink, Row: 6, Frames: 2},
			{Name: SpriteTalk, Row: 7, Frames: 4},
			{Name: SpriteReview, Row: 8, Frames: 2},
			{Name: SpriteCelebrate, Row: 9, Frames: 4},
			{Name: SpriteError, Row: 10, Frames: 2},
		},
		Agents: map[string]AgentSpriteDef{},
	}
}

// loadSpriteConfig reads config.json from the sprites directory.
// Returns default config if file doesn't exist or is unreadable.
func loadSpriteConfig(spritesDir string) SpriteConfig {
	def := defaultSpriteConfig()
	data, err := os.ReadFile(filepath.Join(spritesDir, "config.json"))
	if err != nil {
		return def
	}
	var cfg SpriteConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return def
	}
	// Fill zero values from defaults.
	if cfg.CellWidth == 0 {
		cfg.CellWidth = def.CellWidth
	}
	if cfg.CellHeight == 0 {
		cfg.CellHeight = def.CellHeight
	}
	if len(cfg.States) == 0 {
		cfg.States = def.States
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]AgentSpriteDef{}
	}
	return cfg
}

// initSpriteConfig writes the default config.json if it doesn't exist.
func initSpriteConfig(spritesDir string) error {
	path := filepath.Join(spritesDir, "config.json")
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}
	cfg := defaultSpriteConfig()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// --- Per-Agent Sprite State Tracker ---

// agentSpriteTracker tracks the current sprite state for each agent.
type agentSpriteTracker struct {
	mu    sync.RWMutex
	state map[string]string // agent name -> sprite state
}

func newAgentSpriteTracker() *agentSpriteTracker {
	return &agentSpriteTracker{state: make(map[string]string)}
}

func (t *agentSpriteTracker) set(agent, state string) {
	t.mu.Lock()
	t.state[agent] = state
	t.mu.Unlock()
}

func (t *agentSpriteTracker) get(agent string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if s, ok := t.state[agent]; ok {
		return s
	}
	return SpriteIdle
}

func (t *agentSpriteTracker) snapshot() map[string]string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	m := make(map[string]string, len(t.state))
	for k, v := range t.state {
		m[k] = v
	}
	return m
}

// --- State Resolution ---

// isChatSource returns true if the task source indicates a chat conversation.
// Uses chatSources map from classify.go.
func isChatSource(source string) bool {
	s := strings.ToLower(source)
	// Source may include channel suffix like "discord:12345".
	if i := strings.IndexByte(s, ':'); i > 0 {
		s = s[:i]
	}
	return chatSources[s]
}

// resolveAgentSprite determines the sprite state from dispatch/task context.
// Priority: error > celebrate > review > talk > think > work > idle.
func resolveAgentSprite(taskStatus, dispatchStatus, source string) string {
	switch taskStatus {
	case "failed", "error":
		return SpriteError
	case "done", "success":
		return SpriteCelebrate
	case "review":
		return SpriteReview
	}

	if isChatSource(source) && (taskStatus == "running" || taskStatus == "doing") {
		return SpriteTalk
	}

	switch dispatchStatus {
	case "dispatching":
		return SpriteThink
	}

	switch taskStatus {
	case "running", "doing", "processing":
		return SpriteWork
	}

	return SpriteIdle
}
