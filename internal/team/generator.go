package team

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tetora/internal/provider"
)

// GenerateRequest specifies what kind of team to generate.
type GenerateRequest struct {
	Description string // free-form description of the desired team
	Size        int    // desired number of agents (0 = let LLM decide)
	Template    string // optional base template name to reference
}

// Generator creates team definitions via LLM.
type Generator struct {
	provider provider.Provider
	model    string // model to use for generation (e.g. "opus")
}

// NewGenerator creates a Generator backed by the given provider.
func NewGenerator(p provider.Provider, model string) *Generator {
	return &Generator{provider: p, model: model}
}

// Generate calls the LLM to create a team definition from a description.
func (g *Generator) Generate(ctx context.Context, req GenerateRequest) (*TeamDef, error) {
	prompt := buildGenerationPrompt(req)

	result, err := g.provider.Execute(ctx, provider.Request{
		Prompt:         prompt,
		Model:          g.model,
		Timeout:        120 * time.Second,
		Budget:         2.0,
		PermissionMode: "auto",
	})
	if err != nil {
		return nil, fmt.Errorf("LLM generation failed: %w", err)
	}
	if result.IsError {
		return nil, fmt.Errorf("LLM error: %s", result.Error)
	}

	team, err := parseTeamResponse(result.Output)
	if err != nil {
		return nil, fmt.Errorf("parse LLM response: %w", err)
	}

	team.Builtin = false
	team.CreatedAt = time.Now()
	return team, nil
}

func buildGenerationPrompt(req GenerateRequest) string {
	var sb strings.Builder
	sb.WriteString(`You are a team architect for an AI agent platform called Tetora.
Your task is to design a team of AI agents based on the user's description.

IMPORTANT: Respond with ONLY a valid JSON object. No markdown, no code fences, no explanation.

The JSON must follow this exact schema:
{
  "name": "team-slug-name",
  "description": "One-line team description",
  "agents": [
    {
      "key": "agent-slug",
      "displayName": "Display Name",
      "description": "One-line agent description",
      "model": "opus|sonnet|haiku",
      "keywords": ["at least 15 keywords for routing"],
      "patterns": ["at least 3 regex patterns for routing"],
      "soul": "# Agent Name\n\n## Role\n...\n\n## Personality\n...\n\n## Communication\n...\n\n## Responsibilities\n...\n\n## Work Discipline\n..."
    }
  ]
}

Rules for team design:
1. "key" must be a lowercase slug (letters, numbers, hyphens only)
2. "name" must be a lowercase slug for the team
3. "model" should be "opus" for lead/architect roles, "sonnet" for skilled workers, "haiku" for simple/repetitive tasks
4. "keywords" must have at least 15 entries — these are used for smart routing
5. "patterns" must have at least 3 regex patterns — used for routing (use (?i) for case-insensitive)
6. "soul" must contain these sections: Role, Personality, Communication, Responsibilities, Work Discipline
7. Each agent should have a distinct personality and communication style
8. The team should have clear role separation with minimal overlap

`)

	if req.Size > 0 {
		sb.WriteString(fmt.Sprintf("The team should have exactly %d agents.\n\n", req.Size))
	}

	if req.Template != "" {
		sb.WriteString(fmt.Sprintf("Use the %q template as inspiration for structure.\n\n", req.Template))
	}

	sb.WriteString("User's team description:\n")
	sb.WriteString(req.Description)

	return sb.String()
}

func parseTeamResponse(output string) (*TeamDef, error) {
	// Strip markdown code fences if present.
	text := strings.TrimSpace(output)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		// Remove first and last lines (fences).
		if len(lines) >= 3 {
			lines = lines[1 : len(lines)-1]
			text = strings.Join(lines, "\n")
		}
	}

	// Find JSON object boundaries.
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object found in response")
	}
	text = text[start : end+1]

	var team TeamDef
	if err := json.Unmarshal([]byte(text), &team); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	// Validate minimum requirements.
	if team.Name == "" {
		return nil, fmt.Errorf("team name is empty")
	}
	if len(team.Agents) == 0 {
		return nil, fmt.Errorf("team has no agents")
	}
	for i, a := range team.Agents {
		if a.Key == "" {
			return nil, fmt.Errorf("agent %d has no key", i)
		}
		if a.Soul == "" {
			return nil, fmt.Errorf("agent %q has no soul", a.Key)
		}
	}

	return &team, nil
}
