package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// --- Handoff Types ---

// Handoff represents an agent-to-agent work transfer.
type Handoff struct {
	ID            string `json:"id"`
	WorkflowRunID string `json:"workflowRunId,omitempty"`
	FromRole      string `json:"fromRole"`
	ToRole        string `json:"toRole"`
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
	FromRole      string `json:"fromRole"`
	ToRole        string `json:"toRole"`
	Type          string `json:"type"` // handoff, request, response, note
	Content       string `json:"content"`
	RefID         string `json:"refId,omitempty"` // references another message
	CreatedAt     string `json:"createdAt"`
}

// AutoDelegation is a parsed delegation marker from agent output.
type AutoDelegation struct {
	Role   string `json:"role"`
	Task   string `json:"task"`
	Reason string `json:"reason,omitempty"`
}

// maxAutoDelegations limits runaway delegation chains per step.
const maxAutoDelegations = 3

// --- DB Init ---

const handoffTablesSQL = `
CREATE TABLE IF NOT EXISTS handoffs (
  id TEXT PRIMARY KEY,
  workflow_run_id TEXT DEFAULT '',
  from_role TEXT NOT NULL DEFAULT '',
  to_role TEXT NOT NULL DEFAULT '',
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
  from_role TEXT NOT NULL DEFAULT '',
  to_role TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL DEFAULT 'note',
  content TEXT NOT NULL DEFAULT '',
  ref_id TEXT DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_messages_workflow ON agent_messages(workflow_run_id);
CREATE INDEX IF NOT EXISTS idx_agent_messages_to ON agent_messages(to_role);
`

func initHandoffTables(dbPath string) {
	if dbPath == "" {
		return
	}
	// Migrations: add workflow_run_id columns if missing (for existing DBs created before this column existed).
	// Must run BEFORE handoffTablesSQL so index creation on workflow_run_id succeeds on old schemas.
	for _, col := range []string{
		`ALTER TABLE handoffs ADD COLUMN workflow_run_id TEXT DEFAULT '';`,
		`ALTER TABLE agent_messages ADD COLUMN workflow_run_id TEXT DEFAULT '';`,
	} {
		if err := execDB(dbPath, col); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "no such table") {
				logWarn("handoff migration failed", "sql", col, "error", err)
			}
		}
	}
	if _, err := queryDB(dbPath, handoffTablesSQL); err != nil {
		logWarn("init handoff tables failed", "error", err)
	}
}

// --- Handoff CRUD ---

func recordHandoff(dbPath string, h Handoff) error {
	if dbPath == "" {
		return nil
	}
	initHandoffTables(dbPath)

	sql := fmt.Sprintf(
		`INSERT INTO handoffs (id, workflow_run_id, from_role, to_role, from_step_id, to_step_id, from_session_id, to_session_id, context, instruction, status, created_at)
		 VALUES ('%s','%s','%s','%s','%s','%s','%s','%s','%s','%s','%s','%s')`,
		escapeSQLite(h.ID),
		escapeSQLite(h.WorkflowRunID),
		escapeSQLite(h.FromRole),
		escapeSQLite(h.ToRole),
		escapeSQLite(h.FromStepID),
		escapeSQLite(h.ToStepID),
		escapeSQLite(h.FromSessionID),
		escapeSQLite(h.ToSessionID),
		escapeSQLite(h.Context),
		escapeSQLite(h.Instruction),
		escapeSQLite(h.Status),
		escapeSQLite(h.CreatedAt),
	)
	if _, err := queryDB(dbPath, sql); err != nil {
		return fmt.Errorf("record handoff: %w", err)
	}
	return nil
}

func updateHandoffStatus(dbPath, id, status string) error {
	if dbPath == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`UPDATE handoffs SET status = '%s' WHERE id = '%s'`,
		escapeSQLite(status), escapeSQLite(id),
	)
	if _, err := queryDB(dbPath, sql); err != nil {
		return fmt.Errorf("update handoff status: %w", err)
	}
	return nil
}

func queryHandoffs(dbPath, workflowRunID string) ([]Handoff, error) {
	if dbPath == "" {
		return nil, nil
	}
	initHandoffTables(dbPath)

	where := ""
	if workflowRunID != "" {
		where = fmt.Sprintf("WHERE workflow_run_id = '%s'", escapeSQLite(workflowRunID))
	}

	sql := fmt.Sprintf(
		`SELECT id, workflow_run_id, from_role, to_role, from_step_id, to_step_id,
		        from_session_id, to_session_id, context, instruction, status, created_at
		 FROM handoffs %s ORDER BY created_at ASC`, where)

	rows, err := queryDB(dbPath, sql)
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
		FromRole:      jsonStr(row["from_role"]),
		ToRole:        jsonStr(row["to_role"]),
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

func sendAgentMessage(dbPath string, msg AgentMessage) error {
	if dbPath == "" {
		return nil
	}
	initHandoffTables(dbPath)

	if msg.ID == "" {
		msg.ID = newUUID()
	}
	if msg.CreatedAt == "" {
		msg.CreatedAt = time.Now().Format(time.RFC3339)
	}

	sql := fmt.Sprintf(
		`INSERT INTO agent_messages (id, workflow_run_id, from_role, to_role, type, content, ref_id, created_at)
		 VALUES ('%s','%s','%s','%s','%s','%s','%s','%s')`,
		escapeSQLite(msg.ID),
		escapeSQLite(msg.WorkflowRunID),
		escapeSQLite(msg.FromRole),
		escapeSQLite(msg.ToRole),
		escapeSQLite(msg.Type),
		escapeSQLite(msg.Content),
		escapeSQLite(msg.RefID),
		escapeSQLite(msg.CreatedAt),
	)
	if _, err := queryDB(dbPath, sql); err != nil {
		return fmt.Errorf("send agent message: %w", err)
	}
	return nil
}

func queryAgentMessages(dbPath, workflowRunID, role string, limit int) ([]AgentMessage, error) {
	if dbPath == "" {
		return nil, nil
	}
	initHandoffTables(dbPath)

	if limit <= 0 {
		limit = 50
	}

	var conditions []string
	if workflowRunID != "" {
		conditions = append(conditions, fmt.Sprintf("workflow_run_id = '%s'", escapeSQLite(workflowRunID)))
	}
	if role != "" {
		conditions = append(conditions, fmt.Sprintf("(from_role = '%s' OR to_role = '%s')",
			escapeSQLite(role), escapeSQLite(role)))
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	sql := fmt.Sprintf(
		`SELECT id, workflow_run_id, from_role, to_role, type, content, ref_id, created_at
		 FROM agent_messages %s ORDER BY created_at ASC LIMIT %d`, where, limit)

	rows, err := queryDB(dbPath, sql)
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
		FromRole:      jsonStr(row["from_role"]),
		ToRole:        jsonStr(row["to_role"]),
		Type:          jsonStr(row["type"]),
		Content:       jsonStr(row["content"]),
		RefID:         jsonStr(row["ref_id"]),
		CreatedAt:     jsonStr(row["created_at"]),
	}
}

// --- Auto-Delegation Parser ---

// parseAutoDelegate scans agent output for delegation markers.
// Supported format: {"_delegate": {"role": "...", "task": "...", "reason": "..."}}
// Multiple markers can appear in the output (one per line or separated by text).
func parseAutoDelegate(output string) []AutoDelegation {
	var delegations []AutoDelegation

	// Scan for JSON objects containing _delegate.
	remaining := output
	for len(remaining) > 0 {
		idx := strings.Index(remaining, `"_delegate"`)
		if idx < 0 {
			break
		}

		// Find the enclosing JSON object.
		start := strings.LastIndex(remaining[:idx], "{")
		if start < 0 {
			remaining = remaining[idx+11:]
			continue
		}

		// Find matching closing brace.
		end := findMatchingBrace(remaining[start:])
		if end < 0 {
			remaining = remaining[idx+11:]
			continue
		}

		jsonStr := remaining[start : start+end+1]
		remaining = remaining[start+end+1:]

		// Parse the delegation.
		var wrapper struct {
			Delegate AutoDelegation `json:"_delegate"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &wrapper); err != nil {
			continue
		}

		if wrapper.Delegate.Role == "" || wrapper.Delegate.Task == "" {
			continue
		}

		delegations = append(delegations, wrapper.Delegate)

		if len(delegations) >= maxAutoDelegations {
			break
		}
	}

	return delegations
}

// findMatchingBrace finds the index of the closing brace matching the opening brace at position 0.
// Returns -1 if no match found.
func findMatchingBrace(s string) int {
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

// --- Handoff Execution ---

// executeHandoff creates a new task for the target role with handoff context.
// Returns the task result and updates the handoff status.
func executeHandoff(ctx context.Context, cfg *Config, h *Handoff,
	state *dispatchState, sem chan struct{}) TaskResult {

	// Build prompt with handoff context.
	prompt := buildHandoffPrompt(h.Context, h.Instruction)

	task := Task{
		ID:        newUUID(),
		Name:      fmt.Sprintf("handoff:%s→%s", h.FromRole, h.ToRole),
		Prompt:    prompt,
		Role:      h.ToRole,
		Source:    "handoff:" + h.FromRole,
		SessionID: h.ToSessionID,
	}
	fillDefaults(cfg, &task)

	// Inject role settings.
	if task.Role != "" {
		if soulPrompt, err := loadRolePrompt(cfg, task.Role); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := cfg.Roles[task.Role]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	// Create session for the handoff target.
	now := time.Now().Format(time.RFC3339)
	createSession(cfg.HistoryDB, Session{
		ID:        task.SessionID,
		Role:      h.ToRole,
		Source:    "handoff:" + h.FromRole,
		Status:    "active",
		Title:     fmt.Sprintf("Handoff from %s", h.FromRole),
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Update handoff status to active.
	h.Status = "active"
	updateHandoffStatus(cfg.HistoryDB, h.ID, "active")

	// Execute.
	result := runSingleTask(ctx, cfg, task, sem, h.ToRole)

	// Record session activity.
	recordSessionActivity(cfg.HistoryDB, task, result, h.ToRole)

	// Update handoff status based on result.
	if result.Status == "success" {
		updateHandoffStatus(cfg.HistoryDB, h.ID, "completed")
	} else {
		updateHandoffStatus(cfg.HistoryDB, h.ID, "error")
	}

	if cfg.Log {
		logInfo("handoff completed", "from", h.FromRole, "to", h.ToRole, "handoff", h.ID[:8], "status", result.Status)
	}

	return result
}

// buildHandoffPrompt constructs a prompt with handoff context.
func buildHandoffPrompt(contextOutput, instruction string) string {
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

// processAutoDelegations handles delegation markers from a dispatch step's output.
// It executes delegated tasks and returns the combined output.
func processAutoDelegations(ctx context.Context, cfg *Config, delegations []AutoDelegation,
	originalOutput, workflowRunID, fromRole, fromStepID string,
	state *dispatchState, sem chan struct{}, broker *sseBroker) string {

	if len(delegations) == 0 {
		return originalOutput
	}

	combinedOutput := originalOutput

	for _, d := range delegations {
		// Validate role exists.
		if _, ok := cfg.Roles[d.Role]; !ok {
			logWarn("auto-delegate role not found, skipping", "role", d.Role)
			continue
		}

		now := time.Now().Format(time.RFC3339)
		handoffID := newUUID()
		toSessionID := newUUID()

		// Record handoff.
		h := Handoff{
			ID:            handoffID,
			WorkflowRunID: workflowRunID,
			FromRole:      fromRole,
			ToRole:        d.Role,
			FromStepID:    fromStepID,
			Context:       truncateStr(originalOutput, 5000),
			Instruction:   d.Task,
			Status:        "pending",
			ToSessionID:   toSessionID,
			CreatedAt:     now,
		}
		recordHandoff(cfg.HistoryDB, h)

		// Record agent message.
		sendAgentMessage(cfg.HistoryDB, AgentMessage{
			WorkflowRunID: workflowRunID,
			FromRole:      fromRole,
			ToRole:        d.Role,
			Type:          "handoff",
			Content:       fmt.Sprintf("Auto-delegated: %s (reason: %s)", d.Task, d.Reason),
			RefID:         handoffID,
			CreatedAt:     now,
		})

		// Publish SSE event.
		if broker != nil {
			broker.PublishMulti([]string{
				"workflow:" + workflowRunID,
			}, SSEEvent{
				Type: "auto_delegation",
				Data: map[string]any{
					"handoffId": handoffID,
					"fromRole":  fromRole,
					"toRole":    d.Role,
					"task":      d.Task,
					"reason":    d.Reason,
				},
			})
		}

		if cfg.Log {
			logInfo("auto-delegate executing", "from", fromRole, "to", d.Role, "task", truncate(d.Task, 60))
		}

		// Execute handoff.
		result := executeHandoff(ctx, cfg, &h, state, sem)

		// Append delegated result.
		if result.Output != "" {
			combinedOutput += fmt.Sprintf("\n---\n[Delegated to %s]\n%s", d.Role, result.Output)
		}

		// Record response message.
		sendAgentMessage(cfg.HistoryDB, AgentMessage{
			WorkflowRunID: workflowRunID,
			FromRole:      d.Role,
			ToRole:        fromRole,
			Type:          "response",
			Content:       truncateStr(result.Output, 2000),
			RefID:         handoffID,
			CreatedAt:     time.Now().Format(time.RFC3339),
		})
	}

	return combinedOutput
}
