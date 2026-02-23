package main

import (
	"fmt"
	"strings"
)

// HotkeyManager handles global hotkey registration.
type HotkeyManager struct {
	app    *App
	cfg    HotkeyConfig
	active bool
}

func NewHotkeyManager(app *App, cfg HotkeyConfig) *HotkeyManager {
	return &HotkeyManager{app: app, cfg: cfg}
}

// Start registers the global hotkey.
// Requires platform-specific hotkey library for actual implementation.
func (h *HotkeyManager) Start() error {
	if !h.cfg.Enabled {
		return nil
	}
	h.active = true
	return nil
}

// Stop unregisters the global hotkey.
func (h *HotkeyManager) Stop() {
	h.active = false
}

// ParseBinding converts a binding string to key components.
func ParseBinding(binding string) (modifiers []string, key string, err error) {
	parts := strings.Split(binding, "+")
	if len(parts) < 2 {
		return nil, "", fmt.Errorf("invalid binding: %s (need modifier+key)", binding)
	}
	key = parts[len(parts)-1]
	modifiers = parts[:len(parts)-1]
	for i, m := range modifiers {
		modifiers[i] = strings.TrimSpace(strings.ToLower(m))
	}
	return modifiers, strings.TrimSpace(key), nil
}
