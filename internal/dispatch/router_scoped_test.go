package dispatch

import (
	"testing"

	"tetora/internal/config"
)

// cfg with two agents: hisui (strategic) and spinel (trading).
func scopedTestCfg() *config.Config {
	return &config.Config{
		Agents: map[string]config.AgentConfig{
			"hisui": {
				Description: "strategic analyst",
				Keywords:    []string{"strategy", "market"},
			},
			"spinel": {
				Description: "trading executor",
				Keywords:    []string{"trade", "buy", "sell"},
			},
			"kokuyou": {
				Description: "CTO",
				Keywords:    []string{"code", "architecture"},
			},
		},
		SmartDispatch: config.SmartDispatchConfig{
			DefaultAgent: "hisui",
			Rules: []config.RoutingRule{
				{Agent: "spinel", Keywords: []string{"btc", "eth"}},
				{Agent: "kokuyou", Keywords: []string{"refactor", "deploy"}},
			},
		},
	}
}

func TestClassifyByKeywords_NoFilter_MatchesAll(t *testing.T) {
	cfg := scopedTestCfg()

	// "btc" should match spinel rule with no filter.
	result := ClassifyByKeywords(cfg, "buy btc now", nil)
	if result == nil {
		t.Fatal("expected a match, got nil")
	}
	if result.Agent != "spinel" {
		t.Errorf("expected spinel, got %s", result.Agent)
	}
}

func TestClassifyByKeywords_AllowedAgents_FiltersRules(t *testing.T) {
	cfg := scopedTestCfg()

	// "btc" would normally match spinel, but spinel is not in allowed list.
	result := ClassifyByKeywords(cfg, "buy btc now", []string{"hisui", "kokuyou"})
	// Should not match spinel rule.
	if result != nil && result.Agent == "spinel" {
		t.Errorf("spinel should be excluded, got %s", result.Agent)
	}
}

func TestClassifyByKeywords_AllowedAgents_MatchesAgentKeyword(t *testing.T) {
	cfg := scopedTestCfg()

	// "trade" is in spinel's agent keywords and spinel is allowed.
	result := ClassifyByKeywords(cfg, "I want to trade something", []string{"spinel"})
	if result == nil {
		t.Fatal("expected a match, got nil")
	}
	if result.Agent != "spinel" {
		t.Errorf("expected spinel, got %s", result.Agent)
	}
}

func TestClassifyByKeywords_AllowedAgents_ExcludesAgentKeyword(t *testing.T) {
	cfg := scopedTestCfg()

	// "code" is in kokuyou's keywords but kokuyou is not allowed.
	result := ClassifyByKeywords(cfg, "code review", []string{"hisui", "spinel"})
	if result != nil && result.Agent == "kokuyou" {
		t.Errorf("kokuyou should be excluded, got %s", result.Agent)
	}
}

func TestInAllowed_EmptyList_AlwaysTrue(t *testing.T) {
	if !inAllowed("anyone", nil) {
		t.Error("empty allowed list should return true for any agent")
	}
	if !inAllowed("anyone", []string{}) {
		t.Error("empty allowed list should return true for any agent")
	}
}

func TestInAllowed_NonEmptyList(t *testing.T) {
	allowed := []string{"hisui", "spinel"}
	if !inAllowed("hisui", allowed) {
		t.Error("hisui should be allowed")
	}
	if inAllowed("kokuyou", allowed) {
		t.Error("kokuyou should not be allowed")
	}
}
