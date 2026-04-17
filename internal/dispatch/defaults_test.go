package dispatch

import (
	"testing"

	"tetora/internal/config"
)

func TestFillDefaults_EmptyModelWithEmptyGlobalDefault(t *testing.T) {
	cfg := &config.Config{
		DefaultModel: "",
		Agents:       map[string]config.AgentConfig{},
	}
	task := &Task{
		Prompt: "test prompt",
	}

	FillDefaults(cfg, task)

	if task.Model != DefaultFallbackModel {
		t.Errorf("expected fallback model %q, got %q", DefaultFallbackModel, task.Model)
	}
}

func TestFillDefaults_EmptyModelWithAgentNoModel(t *testing.T) {
	cfg := &config.Config{
		DefaultModel: "",
		Agents: map[string]config.AgentConfig{
			"kokuyou": {Model: ""},
		},
	}
	task := &Task{
		Agent:  "kokuyou",
		Prompt: "review spec",
	}

	FillDefaults(cfg, task)

	if task.Model != DefaultFallbackModel {
		t.Errorf("expected fallback model %q, got %q", DefaultFallbackModel, task.Model)
	}
}

func TestFillDefaults_ExplicitModelNotOverridden(t *testing.T) {
	cfg := &config.Config{
		DefaultModel: "claude-opus-4-6",
		Agents:       map[string]config.AgentConfig{},
	}
	task := &Task{
		Model:  "claude-haiku-4-5-20251001",
		Prompt: "quick check",
	}

	FillDefaults(cfg, task)

	if task.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("explicit model should not be overridden, got %q", task.Model)
	}
}

func TestFillDefaults_WorkdirResolution(t *testing.T) {
	const base = "/tmp/tetora/outputs"
	const workspace = "/tmp/workspace"
	const defaultDir = "/tmp/default"

	cases := []struct {
		name            string
		agent           string
		outputOnly      bool
		agentOutputBase string
		workspaceDir    string
		defaultWorkdir  string
		wantWorkdir     string
	}{
		{
			name:            "output-only agent with AgentOutputBase",
			agent:           "reporter",
			outputOnly:      true,
			agentOutputBase: base,
			workspaceDir:    workspace,
			defaultWorkdir:  defaultDir,
			wantWorkdir:     base + "/reporter/" + AgentOutputSubdir,
		},
		{
			name:            "output-only agent without AgentOutputBase falls back to WorkspaceDir",
			agent:           "reporter",
			outputOnly:      true,
			agentOutputBase: "",
			workspaceDir:    workspace,
			defaultWorkdir:  defaultDir,
			wantWorkdir:     workspace,
		},
		{
			name:            "normal agent ignores AgentOutputBase, uses WorkspaceDir",
			agent:           "coder",
			outputOnly:      false,
			agentOutputBase: base,
			workspaceDir:    workspace,
			defaultWorkdir:  defaultDir,
			wantWorkdir:     workspace,
		},
		{
			name:            "no agent falls back to WorkspaceDir",
			agent:           "",
			agentOutputBase: base,
			workspaceDir:    workspace,
			defaultWorkdir:  defaultDir,
			wantWorkdir:     workspace,
		},
		{
			name:            "no agent and no WorkspaceDir falls back to DefaultWorkdir",
			agent:           "",
			agentOutputBase: base,
			workspaceDir:    "",
			defaultWorkdir:  defaultDir,
			wantWorkdir:     defaultDir,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agents := map[string]config.AgentConfig{}
			if tc.agent != "" {
				agents[tc.agent] = config.AgentConfig{OutputOnly: tc.outputOnly}
			}
			cfg := &config.Config{
				DefaultModel:    DefaultFallbackModel,
				AgentOutputBase: tc.agentOutputBase,
				WorkspaceDir:    tc.workspaceDir,
				DefaultWorkdir:  tc.defaultWorkdir,
				Agents:          agents,
			}
			task := &Task{Agent: tc.agent, Prompt: "test"}
			FillDefaults(cfg, task)
			if task.Workdir != tc.wantWorkdir {
				t.Errorf("want workdir %q, got %q", tc.wantWorkdir, task.Workdir)
			}
		})
	}
}
