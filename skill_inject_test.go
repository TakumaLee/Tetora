package main

import (
	"testing"
)

func TestExtractChannelFromSource(t *testing.T) {
	tests := []struct {
		source   string
		expected string
	}{
		{"telegram", "telegram"},
		{"slack:C123", "slack"},
		{"discord:456", "discord"},
		{"chat:telegram:789", "telegram"},
		{"chat:slack:C123", "slack"},
		{"cron", "cron"},
		{"api", "api"},
		{"", ""},
	}

	for _, tt := range tests {
		got := extractChannelFromSource(tt.source)
		if got != tt.expected {
			t.Errorf("extractChannelFromSource(%q) = %q, want %q", tt.source, got, tt.expected)
		}
	}
}

func TestShouldInjectSkill(t *testing.T) {
	// No matcher — always inject.
	skill := SkillConfig{Name: "test"}
	task := Task{Agent: "琉璃", Prompt: "hello", Source: "telegram"}
	if !shouldInjectSkill(skill, task) {
		t.Error("skill without matcher should always inject")
	}

	// Role match.
	skill.Matcher = &SkillMatcher{Agents: []string{"琉璃"}}
	if !shouldInjectSkill(skill, task) {
		t.Error("skill should match role 琉璃")
	}

	skill.Matcher.Agents = []string{"翡翠"}
	if shouldInjectSkill(skill, task) {
		t.Error("skill should not match role 翡翠")
	}

	// Keyword match.
	skill.Matcher = &SkillMatcher{Keywords: []string{"deploy", "build"}}
	task.Prompt = "Please deploy the app"
	if !shouldInjectSkill(skill, task) {
		t.Error("skill should match keyword 'deploy'")
	}

	task.Prompt = "Say hello"
	if shouldInjectSkill(skill, task) {
		t.Error("skill should not match without keyword")
	}

	// Channel match.
	skill.Matcher = &SkillMatcher{Channels: []string{"slack", "discord"}}
	task.Source = "slack:C123"
	if !shouldInjectSkill(skill, task) {
		t.Error("skill should match channel slack")
	}

	task.Source = "telegram"
	if shouldInjectSkill(skill, task) {
		t.Error("skill should not match channel telegram")
	}

	// Multiple conditions (any match).
	skill.Matcher = &SkillMatcher{
		Agents:    []string{"琥珀"},
		Keywords: []string{"image"},
		Channels: []string{"discord"},
	}
	task.Agent = "琥珀"
	task.Prompt = "hello"
	task.Source = "telegram"
	if !shouldInjectSkill(skill, task) {
		t.Error("skill should match role 琥珀")
	}

	task.Agent = "琉璃"
	task.Prompt = "generate an image"
	if !shouldInjectSkill(skill, task) {
		t.Error("skill should match keyword 'image'")
	}

	task.Prompt = "hello"
	task.Source = "discord:456"
	if !shouldInjectSkill(skill, task) {
		t.Error("skill should match channel discord")
	}
}

func TestSelectSkills(t *testing.T) {
	cfg := &Config{
		Skills: []SkillConfig{
			{Name: "deploy", Matcher: &SkillMatcher{Keywords: []string{"deploy"}}},
			{Name: "creative", Matcher: &SkillMatcher{Agents: []string{"琥珀"}}},
			{Name: "slack-only", Matcher: &SkillMatcher{Channels: []string{"slack"}}},
			{Name: "always", Matcher: nil}, // no matcher = always inject
		},
	}

	task := Task{Agent: "琉璃", Prompt: "deploy the app", Source: "telegram"}
	skills := selectSkills(cfg, task)

	if len(skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(skills))
	}

	// Should have "deploy" (keyword match) and "always" (no matcher).
	hasDeploy := false
	hasAlways := false
	for _, s := range skills {
		if s.Name == "deploy" {
			hasDeploy = true
		}
		if s.Name == "always" {
			hasAlways = true
		}
	}
	if !hasDeploy || !hasAlways {
		t.Error("selectSkills missing expected skills")
	}
}

func TestBuildSkillsPrompt(t *testing.T) {
	cfg := &Config{
		Skills: []SkillConfig{
			{Name: "test", Description: "Test skill", Example: "test arg1 arg2"},
		},
	}

	task := Task{Prompt: "hello"}
	prompt := buildSkillsPrompt(cfg, task)

	if prompt == "" {
		t.Fatal("buildSkillsPrompt returned empty string")
	}

	if !skillStringContains(prompt, "Available Skills") {
		t.Error("prompt missing header")
	}
	if !skillStringContains(prompt, "test") {
		t.Error("prompt missing skill name")
	}
	if !skillStringContains(prompt, "Test skill") {
		t.Error("prompt missing description")
	}
	if !skillStringContains(prompt, "test arg1 arg2") {
		t.Error("prompt missing example")
	}
}

func skillStringContains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && skillFindSubstr(s, substr)
}

func skillFindSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
