package handoff

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tetora/internal/db"
	"tetora/internal/log"
)

// --- Handoff Types ---

// Handoff represents an agent-to-agent work transfer.
type Handoff struct {
	ID            string `json:"id"`
	WorkflowRunID string `json:"workflowRunId,omitempty"`
	FromAgent     string `json:"fromAgent"`
	ToAgent       string `json:"toAgent"`
	FromStepID    string `json:"fromStepId,omitempty"`
	ToStepID      string `json:"toStepId,omitempty"`
	FromSessionID string `json:"fromSessionId,omitempty"`
	ToSessionID   string `json:"toSessionId,omitempty"`
	Context       string `json:"context"`     // output from source agent
	Instruction   string `json:"instruction"` // what the target should do
	Status        string `json:"status"`      // pending, active, completed, error
	CreatedAt     string `json:"createdAt"`
}

// AgentMessage is an inter-agent communication record.
type AgentMessage struct {
	ID            string `json:"id"`
	WorkflowRunID string `json:"workflowRunId,omitempty"`
	FromAgent     string `json:"fromAgent"`
	ToAgent       string `json:"toAgent"`
	Type          string `json:"type"` // handoff, request, response, note
	Content       string `json:"content"`
	RefID         string `json:"refId,omitempty"` // references another message
	CreatedAt     string `json:"createdAt"`
}

// AutoDelegation is a parsed delegation marker from agent output.
type AutoDelegation struct {
	Agent  string `json:"agent"`
	Task   string `json:"task"`
	Reason string `json:"reason,omitempty"`
}

// UnmarshalJSON implements custom unmarshalling to accept both "role" and "agent" keys.
func (d *AutoDelegation) UnmarshalJSON(data []byte) error {
	type Alias AutoDelegation
	aux := &struct {
		*Alias
		Role string `json:"role"`
	}{Alias: (*Alias)(d)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if d.Agent == "" && aux.Role != "" {
		d.Agent = aux.Role
	}
	return nil
}

// MaxAutoDelegations limits runaway delegation chains per step.
const MaxAutoDelegations = 3

// --- DB Init ---

const handoffTablesSQL = `
CREATE TABLE IF NOT EXISTS handoffs (
  id TEXT PRIMARY KEY,
  workflow_run_id TEXT DEFAULT '',
  from_agent TEXT NOT NULL DEFAULT '',
  to_agent TEXT NOT NULL DEFAULT '',
  from_step_id TEXT DEFAULT '',
  to_step_id TEXT DEFAULT '',
  from_session_id TEXT DEFAULT '',
  to_session_id TEXT DEFAULT '',
  context TEXT DEFAULT '',
  instruction TEXT DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_handoffs_workflow ON handoffs(workflow_run_id);
CREATE INDEX IF NOT EXISTS idx_handoffs_status ON handoffs(status);

CREATE TABLE IF NOT EXISTS agent_messages (
  id TEXT PRIMARY KEY,
  workflow_run_id TEXT DEFAULT '',
  from_agent TEXT NOT NULL DEFAULT '',
  to_agent TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL DEFAULT 'note',
  content TEXT NOT NULL DEFAULT '',
  ref_id TEXT DEFAULT '',
  message TEXT DEFAULT '',
  session_id TEXT DEFAULT '',
  read_at TEXT DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_messages_workflow ON agent_messages(workflow_run_id);
CREATE INDEX IF NOT EXISTS idx_agent_messages_to ON agent_messages(to_agent);
CREATE INDEX IF NOT EXISTS idx_agent_messages_to_agent ON agent_messages(to_agent, read_at);
CREATE INDEX IF NOT EXISTS idx_agent_messages_session ON agent_messages(session_id);
`

func InitTables(dbPath string) {
	if dbPath == "" {
		return
	}
	for _, col := range []string{
		`ALTER TABLE handoffs ADD COLUMN workflow_run_id TEXT DEFAULT '';`,
		`ALTER TABLE agent_messages ADD COLUMN workflow_run_id TEXT DEFAULT '';`,
		`ALTER TABLE agent_messages ADD COLUMN type TEXT NOT NULL DEFAULT 'note';`,
		`ALTER TABLE agent_messages ADD COLUMN message TEXT DEFAULT '';`,
		`ALTER TABLE agent_messages ADD COLUMN session_id TEXT DEFAULT '';`,
		`ALTER TABLE agent_messages ADD COLUMN read_at TEXT DEFAULT '';`,
	} {
		if err := db.Exec(dbPath, col); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "no such table") {
				log.Warn("handoff migration failed", "sql", col, "error", err)
			}
		}
	}
	for _, stmt := range []string{
		`ALTER TABLE handoffs RENAME COLUMN from_role TO from_agent;`,
		`ALTER TABLE handoffs RENAME COLUMN to_role TO to_agent;`,
		`ALTER TABLE agent_messages RENAME COLUMN from_role TO from_agent;`,
		`ALTER TABLE agent_messages RENAME COLUMN to_role TO to_agent;`,
	} {
		if err := db.Exec(dbPath, stmt); err != nil {
			if !strings.Contains(err.Error(), "no such column") && !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "no such table") {
				log.Warn("handoff rename migration", "sql", stmt, "error", err)
			}
		}
	}
	if _, err := db.Query(dbPath, handoffTablesSQL); err != nil {
		log.Warn("init handoff tables failed", "error", err)
	}
}

// --- Handoff CRUD ---

func RecordHandoff(dbPath string, h Handoff) error {
	if dbPath == "" {
		return nil
	}
	InitTables(dbPath)

	sql := fmt.Sprintf(
		`INSERT INTO handoffs (id, workflow_run_id, from_agent, to_agent, from_step_id, to_step_id, from_session_id, to_session_id, context, instruction, status, created_at)
		 VALUES ('%s','%s','%s','%s','%s','%s','%s','%s','%s','%s','%s','%s')`,
		db.Escape(h.ID),
		db.Escape(h.WorkflowRunID),
		db.Escape(h.FromAgent),
		db.Escape(h.ToAgent),
		db.Escape(h.FromStepID),
		db.Escape(h.ToStepID),
		db.Escape(h.FromSessionID),
		db.Escape(h.ToSessionID),
		db.Escape(h.Context),
		db.Escape(h.Instruction),
		db.Escape(h.Status),
		db.Escape(h.CreatedAt),
	)
	if _, err := db.Query(dbPath, sql); err != nil {
		return fmt.Errorf("record handoff: %w", err)
	}
	return nil
}

func UpdateStatus(dbPath, id, status string) error {
	if dbPath == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`UPDATE handoffs SET status = '%s' WHERE id = '%s'`,
		db.Escape(status), db.Escape(id),
	)
	if _, err := db.Query(dbPath, sql); err != nil {
		return fmt.Errorf("update handoff status: %w", err)
	}
	return nil
}

func QueryHandoffs(dbPath, workflowRunID string) ([]Handoff, error) {
	if dbPath == "" {
		return nil, nil
	}
	InitTables(dbPath)

	where := ""
	if workflowRunID != "" {
		where = fmt.Sprintf("WHERE workflow_run_id = '%s'", db.Escape(workflowRunID))
	}

	sql := fmt.Sprintf(
		`SELECT id, workflow_run_id, from_agent, to_agent, from_step_id, to_step_id,
		        from_session_id, to_session_id, context, instruction, status, created_at
		 FROM handoffs %s ORDER BY created_at ASC`, where)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}

	var handoffs []Handoff
	for _, row := range rows {
		handoffs = append(handoffs, handoffFromRow(row))
	}
	return handoffs, nil
}

func handoffFromRow(row map[string]any) Handoff {
	return Handoff{
		ID:            jsonStr(row["id"]),
		WorkflowRunID: jsonStr(row["workflow_run_id"]),
		FromAgent:     jsonStr(row["from_agent"]),
		ToAgent:       jsonStr(row["to_agent"]),
		FromStepID:    jsonStr(row["from_step_id"]),
		ToStepID:      jsonStr(row["to_step_id"]),
		FromSessionID: jsonStr(row["from_session_id"]),
		ToSessionID:   jsonStr(row["to_session_id"]),
		Context:       jsonStr(row["context"]),
		Instruction:   jsonStr(row["instruction"]),
		Status:        jsonStr(row["status"]),
		CreatedAt:     jsonStr(row["created_at"]),
	}
}

// --- Agent Message CRUD ---

func SendAgentMessage(dbPath string, msg AgentMessage, newUUID func() string) error {
	if dbPath == "" {
		return nil
	}
	InitTables(dbPath)

	if msg.ID == "" {
		msg.ID = newUUID()
	}
	if msg.CreatedAt == "" {
		msg.CreatedAt = time.Now().Format(time.RFC3339)
	}

	sql := fmt.Sprintf(
		`INSERT INTO agent_messages (id, workflow_run_id, from_agent, to_agent, type, content, ref_id, created_at)
		 VALUES ('%s','%s','%s','%s','%s','%s','%s','%s')`,
		db.Escape(msg.ID),
		db.Escape(msg.WorkflowRunID),
		db.Escape(msg.FromAgent),
		db.Escape(msg.ToAgent),
		db.Escape(msg.Type),
		db.Escape(msg.Content),
		db.Escape(msg.RefID),
		db.Escape(msg.CreatedAt),
	)
	if _, err := db.Query(dbPath, sql); err != nil {
		return fmt.Errorf("send agent message: %w", err)
	}
	return nil
}

func QueryAgentMessages(dbPath, workflowRunID, role string, limit int) ([]AgentMessage, error) {
	if dbPath == "" {
		return nil, nil
	}
	InitTables(dbPath)

	if limit <= 0 {
		limit = 50
	}

	var conditions []string
	if workflowRunID != "" {
		conditions = append(conditions, fmt.Sprintf("workflow_run_id = '%s'", db.Escape(workflowRunID)))
	}
	if role != "" {
		conditions = append(conditions, fmt.Sprintf("(from_agent = '%s' OR to_agent = '%s')",
			db.Escape(role), db.Escape(role)))
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	sql := fmt.Sprintf(
		`SELECT id, workflow_run_id, from_agent, to_agent, type, content, ref_id, created_at
		 FROM agent_messages %s ORDER BY created_at ASC LIMIT %d`, where, limit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}

	var msgs []AgentMessage
	for _, row := range rows {
		msgs = append(msgs, agentMessageFromRow(row))
	}
	return msgs, nil
}

func agentMessageFromRow(row map[string]any) AgentMessage {
	return AgentMessage{
		ID:            jsonStr(row["id"]),
		WorkflowRunID: jsonStr(row["workflow_run_id"]),
		FromAgent:     jsonStr(row["from_agent"]),
		ToAgent:       jsonStr(row["to_agent"]),
		Type:          jsonStr(row["type"]),
		Content:       jsonStr(row["content"]),
		RefID:         jsonStr(row["ref_id"]),
		CreatedAt:     jsonStr(row["created_at"]),
	}
}

// --- Auto-Delegation Parser ---

// ParseAutoDelegate scans agent output for delegation markers.
func ParseAutoDelegate(output string) []AutoDelegation {
	var delegations []AutoDelegation

	remaining := output
	for len(remaining) > 0 {
		idx := strings.Index(remaining, `"_delegate"`)
		if idx < 0 {
			break
		}

		start := strings.LastIndex(remaining[:idx], "{")
		if start < 0 {
			remaining = remaining[idx+11:]
			continue
		}

		end := FindMatchingBrace(remaining[start:])
		if end < 0 {
			remaining = remaining[idx+11:]
			continue
		}

		jsonStr := remaining[start : start+end+1]
		remaining = remaining[start+end+1:]

		var wrapper struct {
			Delegate AutoDelegation `json:"_delegate"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &wrapper); err != nil {
			continue
		}

		if wrapper.Delegate.Agent == "" || wrapper.Delegate.Task == "" {
			continue
		}

		delegations = append(delegations, wrapper.Delegate)

		if len(delegations) >= MaxAutoDelegations {
			break
		}
	}

	return delegations
}

// FindMatchingBrace finds the index of the closing brace matching the opening brace at position 0.
func FindMatchingBrace(s string) int {
	depth := 0
	inString := false
	escape := false

	for i, c := range s {
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inString {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// BuildHandoffPrompt constructs a prompt with handoff context.
func BuildHandoffPrompt(contextOutput, instruction string) string {
	var parts []string
	if contextOutput != "" {
		parts = append(parts, fmt.Sprintf("[Handoff Context]\n%s", contextOutput))
	}
	if instruction != "" {
		parts = append(parts, fmt.Sprintf("[Instruction]\n%s", instruction))
	}
	if len(parts) == 0 {
		return instruction
	}
	return strings.Join(parts, "\n\n")
}

// jsonStr safely extracts a string from a map value.
func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	default:
		return fmt.Sprintf("%v", s)
	}
}
