package main

// tool_memory.go — Registration moved to internal/tools/memory.go.
// Compatibility wrappers below are used by tests that call handlers directly.

import (
	"context"
	"encoding/json"

	"tetora/internal/tools"
)

var memoryDepsForTest = tools.MemoryDeps{
	GetMemory: getMemory,
	SetMemory: func(cfg *Config, role, key, value string) error {
		return setMemory(cfg, role, key, value)
	},
	DeleteMemory: deleteMemory,
	SearchMemory: func(cfg *Config, role, query string) ([]tools.MemoryEntry, error) {
		entries, err := searchMemoryFS(cfg, role, query)
		if err != nil {
			return nil, err
		}
		result := make([]tools.MemoryEntry, len(entries))
		for i, e := range entries {
			result[i] = tools.MemoryEntry{Key: e.Key, Value: e.Value}
		}
		return result, nil
	},
}

func toolMemorySearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	h := tools.MakeMemorySearchHandler(memoryDepsForTest)
	return h(ctx, cfg, input)
}

func toolMemoryGet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	h := tools.MakeMemoryGetHandler(memoryDepsForTest)
	return h(ctx, cfg, input)
}

func toolKnowledgeSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	h := tools.MakeKnowledgeSearchHandler()
	return h(ctx, cfg, input)
}
