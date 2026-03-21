package coord

import "time"

// Claim declares that an agent is actively working on a task and owns certain
// file regions. File: claims/{task_id}__{agent}.json
type Claim struct {
	Version   string    `json:"version"`
	Type      string    `json:"type"`
	TaskID    string    `json:"task_id"`
	Agent     string    `json:"agent"`
	ClaimedAt time.Time `json:"claimed_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Regions   []string  `json:"regions"`
	Status    string    `json:"status"` // "active" | "released"
}

// Finding records an agent's output, decisions, and notes for downstream
// consumers after completing work.
// File: findings/{task_id}__{agent}__{yyyymmddThhmmss}.json
type Finding struct {
	Version         string    `json:"version"`
	Type            string    `json:"type"`
	TaskID          string    `json:"task_id"`
	Agent           string    `json:"agent"`
	RecordedAt      time.Time `json:"recorded_at"`
	Summary         string    `json:"summary"`
	Artifacts       []string  `json:"artifacts,omitempty"`
	Decisions       []string  `json:"decisions,omitempty"`
	DownstreamNotes string    `json:"downstream_notes,omitempty"`
}

// Blocker signals that an agent is blocked and requires intervention or a
// dependency to be resolved.
// File: blockers/{task_id}__{agent}__{yyyymmddThhmmss}.json
type Blocker struct {
	Version       string     `json:"version"`
	Type          string     `json:"type"`
	TaskID        string     `json:"task_id"`
	Agent         string     `json:"agent"`
	BlockedAt     time.Time  `json:"blocked_at"`
	Severity      string     `json:"severity"` // "low" | "medium" | "high" | "critical"
	Description   string     `json:"description"`
	DependsOnTask string     `json:"depends_on_task,omitempty"`
	Resolution    string     `json:"resolution,omitempty"`
	ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy    string     `json:"resolved_by,omitempty"`
}

// KnownAgents is the set of agents that participate in coordination.
var KnownAgents = map[string]bool{
	"ruri": true, "hisui": true, "kokuyou": true, "kohaku": true, "spinel": true,
}
