package dispatch

import (
	"context"
	"fmt"
	"time"

	"tetora/internal/config"
	
	"tetora/internal/log"
	"tetora/internal/session"
	"tetora/internal/trace"
)

// HandoffDeps bundles callbacks that depend on root-only constructs.
// Root constructs a HandoffDeps and passes it into ExecuteHandoff / ProcessAutoDelegations.
type HandoffDeps struct {
	// FillDefaults populates Task fields with config-derived defaults.
	FillDefaults func(t *Task)
	// LoadAgentPrompt returns the SOUL prompt for the named agent (empty string on miss).
	LoadAgentPrompt func(agentName string) string
	// CreateSession persists a new session record.
	CreateSession func(dbPath string, s session.Session) error
	// RecordSessionActivity records user message and assistant response for a completed task.
	RecordSessionActivity func(dbPath string, task Task, result TaskResult, role string)
	// RunTask executes a task and returns its result.
	RunTask func(ctx context.Context, task Task, sem, childSem chan struct{}, agentName string) TaskResult
	// TruncateStr truncates a string to maxLen characters with a "..." suffix.
	TruncateStr func(s string, maxLen int) string
}

// ExecuteHandoff creates a new task for the target agent with handoff context
// and updates the handoff record status on completion.
func ExecuteHandoff(ctx context.Context, cfg *config.Config, h *Handoff,
	deps HandoffDeps, sem, childSem chan struct{}) TaskResult {

	prompt := BuildHandoffPrompt(h.Context, h.Instruction)

	task := Task{
		ID:        trace.NewUUID(),
		Name:      fmt.Sprintf("handoff:%s→%s", h.FromAgent, h.ToAgent),
		Prompt:    prompt,
		Agent:     h.ToAgent,
		Source:    "handoff:" + h.FromAgent,
		SessionID: h.ToSessionID,
	}
	deps.FillDefaults(&task)

	// Inject agent SOUL prompt (model/permission already applied by FillDefaults→ApplyAgentDefaults).
	if task.Agent != "" {
		if soulPrompt := deps.LoadAgentPrompt(task.Agent); soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
	}

	// Create session for the handoff target.
	now := time.Now().Format(time.RFC3339)
	deps.CreateSession(cfg.HistoryDB, session.Session{
		ID:        task.SessionID,
		Agent:     h.ToAgent,
		Source:    "handoff:" + h.FromAgent,
		Status:    "active",
		Title:     fmt.Sprintf("Handoff from %s", h.FromAgent),
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Update handoff status to active.
	h.Status = "active"
	UpdateStatus(cfg.HistoryDB, h.ID, "active")

	// Execute.
	result := deps.RunTask(ctx, task, sem, childSem, h.ToAgent)

	// Record session activity.
	deps.RecordSessionActivity(cfg.HistoryDB, task, result, h.ToAgent)

	// Update handoff status based on result.
	if result.Status == "success" {
		UpdateStatus(cfg.HistoryDB, h.ID, "completed")
	} else {
		UpdateStatus(cfg.HistoryDB, h.ID, "error")
	}

	if cfg.Log {
		log.Info("handoff completed", "from", h.FromAgent, "to", h.ToAgent, "handoff", h.ID[:8], "status", result.Status)
	}

	return result
}

// ProcessAutoDelegations handles delegation markers from a dispatch step's output.
// It executes delegated tasks and returns the combined output.
func ProcessAutoDelegations(ctx context.Context, cfg *config.Config, delegations []AutoDelegation,
	originalOutput, workflowRunID, fromAgent, fromStepID string,
	deps HandoffDeps, sem, childSem chan struct{}, broker SSEBrokerPublisher) string {

	if len(delegations) == 0 {
		return originalOutput
	}

	combinedOutput := originalOutput

	for _, d := range delegations {
		// Validate agent exists.
		if _, ok := cfg.Agents[d.Agent]; !ok {
			log.Warn("auto-delegate agent not found, skipping", "agent", d.Agent)
			continue
		}

		now := time.Now().Format(time.RFC3339)
		handoffID := trace.NewUUID()
		toSessionID := trace.NewUUID()

		// Build context string, truncated to budget.
		contextStr := originalOutput
		if deps.TruncateStr != nil {
			contextStr = deps.TruncateStr(originalOutput, cfg.PromptBudget.ContextMaxOrDefault())
		}

		// Record handoff.
		h := Handoff{
			ID:            handoffID,
			WorkflowRunID: workflowRunID,
			FromAgent:     fromAgent,
			ToAgent:       d.Agent,
			FromStepID:    fromStepID,
			Context:       contextStr,
			Instruction:   d.Task,
			Status:        "pending",
			ToSessionID:   toSessionID,
			CreatedAt:     now,
		}
		RecordHandoff(cfg.HistoryDB, h)

		// Record agent message.
		SendAgentMessage(cfg.HistoryDB, AgentMessage{
			WorkflowRunID: workflowRunID,
			FromAgent:     fromAgent,
			ToAgent:       d.Agent,
			Type:          "handoff",
			Content:       fmt.Sprintf("Auto-delegated: %s (reason: %s)", d.Task, d.Reason),
			RefID:         handoffID,
			CreatedAt:     now,
		}, trace.NewUUID)

		// Publish SSE event.
		if broker != nil {
			broker.PublishMulti([]string{
				"workflow:" + workflowRunID,
			}, SSEEvent{
				Type: "auto_delegation",
				Data: map[string]any{
					"handoffId": handoffID,
					"fromAgent": fromAgent,
					"toAgent":   d.Agent,
					"task":      d.Task,
					"reason":    d.Reason,
				},
			})
		}

		if cfg.Log {
			log.Info("auto-delegate executing", "from", fromAgent, "to", d.Agent, "task", dispatchTruncate(d.Task, 60))
		}

		// Execute handoff.
		result := ExecuteHandoff(ctx, cfg, &h, deps, sem, childSem)

		// Append delegated result.
		if result.Output != "" {
			combinedOutput += fmt.Sprintf("\n---\n[Delegated to %s]\n%s", d.Agent, result.Output)
		}

		// Record response message.
		responseContent := result.Output
		if deps.TruncateStr != nil {
			responseContent = deps.TruncateStr(result.Output, 2000)
		}
		SendAgentMessage(cfg.HistoryDB, AgentMessage{
			WorkflowRunID: workflowRunID,
			FromAgent:     d.Agent,
			ToAgent:       fromAgent,
			Type:          "response",
			Content:       responseContent,
			RefID:         handoffID,
			CreatedAt:     time.Now().Format(time.RFC3339),
		}, trace.NewUUID)
	}

	return combinedOutput
}

// dispatchTruncate shortens a string to maxLen characters with a "..." suffix.
func dispatchTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
