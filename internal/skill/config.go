package skill

import (
	"encoding/json"
	"time"
)

// AppConfig holds configuration fields that skill functions need.
type AppConfig struct {
	Skills               []SkillConfig
	SkillStore           SkillStoreConfig
	WorkspaceDir         string
	HistoryDB            string
	BaseDir              string
	MaxSkillsPerTask     int
	SkillsMax            int
	SkillsOnDemandEnabled bool // if true, BuildSkillsPromptWithMeta trims injection by tier (Simple: mandatory-only; Standard: no Tier 2 docs)
	Browser              BrowserRelay
	NotifyFn             func(string) // optional: called after scan if findings warrant notification
}

func (c *AppConfig) maxSkillsPerTaskOrDefault() int {
	if c.MaxSkillsPerTask > 0 {
		return c.MaxSkillsPerTask
	}
	return 3
}

func (c *AppConfig) skillsMaxOrDefault() int {
	if c.SkillsMax > 0 {
		return c.SkillsMax
	}
	return 4000
}

// TaskContext holds task fields used by skill injection.
type TaskContext struct {
	Agent     string
	Prompt    string
	Source    string
	SessionID string
}

// TaskCompletion holds the result of a task, used by RecordSkillCompletion.
type TaskCompletion struct {
	Status string
	Error  string
}

// BrowserRelay is the interface skill_notebooklm needs.
type BrowserRelay interface {
	Connected() bool
	SendCommand(action string, params json.RawMessage, timeout time.Duration) (string, error)
}
