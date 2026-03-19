// Package team provides team definition management: generation, storage,
// and application of pre-built agent teams to the Tetora config.
package team

import "time"

// TeamDef represents a complete team definition stored as a single JSON file.
type TeamDef struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Builtin     bool       `json:"builtin"`
	CreatedAt   time.Time  `json:"createdAt"`
	Agents      []AgentDef `json:"agents"`
}

// AgentDef describes a single agent within a team.
type AgentDef struct {
	Key            string   `json:"key"`
	DisplayName    string   `json:"displayName"`
	Description    string   `json:"description"`
	Model          string   `json:"model"`
	Keywords       []string `json:"keywords"`
	Patterns       []string `json:"patterns,omitempty"`
	PermissionMode string   `json:"permissionMode,omitempty"`
	Soul           string   `json:"soul"`
}
