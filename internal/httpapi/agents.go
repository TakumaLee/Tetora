package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

)

// AgentDeps holds all dependencies for the agent HTTP handlers.
type AgentDeps struct {
	HistoryDB string

	// Agent Messages
	QueryAgentMessages func(workflowRun, role string, limit int) (any, error)
	SendAgentMessage   func(body json.RawMessage) (id string, err error)
	QueryHandoffs      func(workflowRun string) (any, error)

	// Task Board
	TaskBoardEnabled   bool
	ListTasksPaginated func(status, assignee, project string, page, limit int) (any, error)
	CreateTask         func(body json.RawMessage) (any, error)
	GetTask            func(id string) (any, error)
	UpdateTask         func(id string, body json.RawMessage) (any, error)
	DeleteTask         func(id string) error
	MoveTask           func(id, status string) (any, error)
	AssignTask         func(id, assignee string) (any, error)
	GetBoardView       func(params map[string]string) (any, error)
	ListChildren       func(parentID string) (any, error)
	AddComment         func(taskID, author, content, ctype string) (any, error)
	// GetThread returns a slice of comments as any (e.g. []TaskComment from root package).
	// Each element must marshal to {"type":string,"content":string,...}.
	GetThread func(taskID string) (any, error)

	// Task board SSE publish
	PublishBoardUpdate func(data map[string]any)

	// Quick Actions
	ListQuickActions   func() any
	RunQuickAction     func(ctx context.Context, name string, params map[string]any) (any, error)
	SearchQuickActions func(query string) any

	// Agent Communication
	ListAgents       func(ctx context.Context) (string, error) // returns raw JSON string
	GetAgentMessages func(role string, markAsRead bool) (any, error)
	SendAgentMsg     func(ctx context.Context, body json.RawMessage) (string, error)
	GetRunningAgents func() any // returns {running: [...], count: N}

	// Trust
	GetAllTrustStatuses func() any
	GetTrustStatus      func(agent string) any
	AgentExists         func(name string) bool
	SetTrustLevel       func(agent, level, ip string) (any, error) // returns updated status
	ValidTrustLevels    func() []string
	IsValidTrustLevel   func(level string) bool
	QueryTrustEvents    func(role string, limit int) (any, error)

	// Audit
	AuditLog func(action, source, detail, ip string)
}

// comment is used internally when iterating GetThread results to inspect Type/Content.
type comment struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// RegisterAgentRoutes registers all agent-related HTTP routes.
func RegisterAgentRoutes(mux *http.ServeMux, d AgentDeps) {
	h := &agentHandler{d: d}

	// Agent Messages
	mux.HandleFunc("/agent-messages", h.handleAgentMessages)
	mux.HandleFunc("/handoffs", h.handleHandoffs)

	// Task Board
	mux.HandleFunc("/api/tasks", h.handleTasks)
	mux.HandleFunc("/api/tasks/board", h.handleTasksBoard)
	mux.HandleFunc("/api/tasks/", h.handleTaskByID)

	// Quick Actions
	mux.HandleFunc("/api/quick/list", h.handleQuickList)
	mux.HandleFunc("/api/quick/run", h.handleQuickRun)
	mux.HandleFunc("/api/quick/search", h.handleQuickSearch)

	// Agent Communication
	mux.HandleFunc("/api/agents", h.handleAgents)
	mux.HandleFunc("/api/agents/messages", h.handleAgentMessages2)
	mux.HandleFunc("/api/agents/message", h.handleAgentMessage)
	mux.HandleFunc("/api/agents/running", h.handleRunningAgents)

	// Trust
	mux.HandleFunc("/trust", h.handleTrust)
	mux.HandleFunc("/trust/", h.handleTrustAgent)
	mux.HandleFunc("/trust-events", h.handleTrustEvents)
}

type agentHandler struct {
	d AgentDeps
}

// --- Agent Messages ---

func (h *agentHandler) handleAgentMessages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		workflowRun := r.URL.Query().Get("workflowRun")
		role := r.URL.Query().Get("role")
		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		msgs, err := h.d.QueryAgentMessages(workflowRun, role, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if msgs == nil {
			msgs = []any{}
		}
		json.NewEncoder(w).Encode(msgs)

	case http.MethodPost:
		// Decode and validate before passing to the callback.
		var msg struct {
			ID            string `json:"id"`
			FromAgent     string `json:"fromAgent"`
			ToAgent       string `json:"toAgent"`
			Content       string `json:"content"`
			Type          string `json:"type"`
			WorkflowRunID string `json:"workflowRunId"`
			RefID         string `json:"refId"`
			CreatedAt     string `json:"createdAt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}
		if msg.FromAgent == "" || msg.ToAgent == "" || msg.Content == "" {
			http.Error(w, `{"error":"fromAgent, toAgent, and content are required"}`, http.StatusBadRequest)
			return
		}
		if msg.Type == "" {
			msg.Type = "note"
		}
		raw, _ := json.Marshal(msg)
		id, err := h.d.SendAgentMessage(raw)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if h.d.AuditLog != nil {
			detail := fmt.Sprintf("%s→%s type=%s", msg.FromAgent, msg.ToAgent, msg.Type)
			h.d.AuditLog("agent.message", "http", detail, clientIP(r))
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "sent", "id": id})

	default:
		http.Error(w, `{"error":"GET or POST only"}`, http.StatusMethodNotAllowed)
	}
}

func (h *agentHandler) handleHandoffs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		return
	}
	workflowRun := r.URL.Query().Get("workflowRun")
	handoffs, err := h.d.QueryHandoffs(workflowRun)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}
	if handoffs == nil {
		handoffs = []any{}
	}
	json.NewEncoder(w).Encode(handoffs)
}

// --- Task Board ---

func (h *agentHandler) handleTasks(w http.ResponseWriter, r *http.Request) {
	if !h.d.TaskBoardEnabled {
		http.Error(w, `{"error":"task board not enabled"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		status := r.URL.Query().Get("status")
		assignee := r.URL.Query().Get("assignee")
		project := r.URL.Query().Get("project")
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if page < 1 {
			page = 1
		}
		if limit < 1 {
			limit = 50
		}
		result, err := h.d.ListTasksPaginated(status, assignee, project, page, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(result)

	case http.MethodPost:
		raw, err := readBody(r)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid request: %v"}`, err), http.StatusBadRequest)
			return
		}
		created, err := h.d.CreateTask(raw)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(created)
		if h.d.PublishBoardUpdate != nil {
			h.d.PublishBoardUpdate(map[string]any{"action": "created"})
		}

	default:
		http.Error(w, `{"error":"GET or POST only"}`, http.StatusMethodNotAllowed)
	}
}

func (h *agentHandler) handleTasksBoard(w http.ResponseWriter, r *http.Request) {
	if !h.d.TaskBoardEnabled {
		http.Error(w, `{"error":"task board not enabled"}`, http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	params := map[string]string{
		"project":     r.URL.Query().Get("project"),
		"assignee":    r.URL.Query().Get("assignee"),
		"priority":    r.URL.Query().Get("priority"),
		"workflow":    r.URL.Query().Get("workflow"),
		"includeDone": r.URL.Query().Get("includeDone"),
	}
	board, err := h.d.GetBoardView(params)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(board)
}

func (h *agentHandler) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	if !h.d.TaskBoardEnabled {
		http.Error(w, `{"error":"task board not enabled"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
		return
	}
	taskID := parts[0]

	// DELETE /api/tasks/{id}
	if r.Method == http.MethodDelete && len(parts) == 1 {
		if err := h.d.DeleteTask(taskID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"deleted": taskID})
		if h.d.PublishBoardUpdate != nil {
			h.d.PublishBoardUpdate(map[string]any{"taskId": taskID, "action": "deleted"})
		}
		return
	}

	// GET /api/tasks/{id}
	if r.Method == http.MethodGet && len(parts) == 1 {
		task, err := h.d.GetTask(taskID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(task)
		return
	}

	// PATCH /api/tasks/{id}
	if r.Method == http.MethodPatch && len(parts) == 1 {
		raw, err := readBody(r)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid request: %v"}`, err), http.StatusBadRequest)
			return
		}
		task, err := h.d.UpdateTask(taskID, raw)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(task)
		if h.d.PublishBoardUpdate != nil {
			h.d.PublishBoardUpdate(map[string]any{"taskId": taskID, "action": "updated"})
		}
		return
	}

	// GET /api/tasks/{id}/subtasks
	if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "subtasks" {
		type subtaskNode struct {
			Task      any           `json:"task"`
			Children  []subtaskNode `json:"children"`
			Truncated bool          `json:"truncated,omitempty"`
		}
		var buildTree func(parentID string, depth int) ([]subtaskNode, error)
		buildTree = func(parentID string, depth int) ([]subtaskNode, error) {
			children, err := h.d.ListChildren(parentID)
			if err != nil {
				return nil, err
			}
			// children is a slice of any; iterate via JSON round-trip to get IDs
			childSlice, ok := toAnySlice(children)
			if !ok {
				return nil, nil
			}
			var nodes []subtaskNode
			for _, child := range childSlice {
				node := subtaskNode{Task: child}
				childID := extractStringField(child, "id")
				if depth < 3 && childID != "" {
					sub, err := buildTree(childID, depth+1)
					if err != nil {
						return nil, err
					}
					node.Children = sub
				} else if childID != "" {
					deeper, _ := h.d.ListChildren(childID)
					if s, ok := toAnySlice(deeper); ok && len(s) > 0 {
						node.Truncated = true
					}
				}
				nodes = append(nodes, node)
			}
			return nodes, nil
		}
		tree, err := buildTree(taskID, 1)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"children": tree})
		return
	}

	// POST /api/tasks/{id}/move
	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "move" {
		var req struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid request: %v"}`, err), http.StatusBadRequest)
			return
		}
		task, err := h.d.MoveTask(taskID, req.Status)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(task)
		if h.d.PublishBoardUpdate != nil {
			h.d.PublishBoardUpdate(map[string]any{"taskId": taskID, "action": "moved", "status": req.Status})
		}
		return
	}

	// POST /api/tasks/{id}/assign
	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "assign" {
		var req struct {
			Assignee string `json:"assignee"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid request: %v"}`, err), http.StatusBadRequest)
			return
		}
		task, err := h.d.AssignTask(taskID, req.Assignee)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(task)
		if h.d.PublishBoardUpdate != nil {
			h.d.PublishBoardUpdate(map[string]any{"taskId": taskID, "action": "assigned"})
		}
		return
	}

	// POST /api/tasks/{id}/comment
	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "comment" {
		var req struct {
			Author  string `json:"author"`
			Content string `json:"content"`
			Type    string `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid request: %v"}`, err), http.StatusBadRequest)
			return
		}
		comment, err := h.d.AddComment(taskID, req.Author, req.Content, req.Type)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(comment)
		return
	}

	// GET /api/tasks/{id}/thread
	if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "thread" {
		raw, err := h.d.GetThread(taskID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if raw == nil {
			raw = []any{}
		}
		json.NewEncoder(w).Encode(map[string]any{"comments": raw})
		return
	}

	// GET /api/tasks/{id}/diff
	if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "diff" {
		raw, err := h.d.GetThread(taskID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		comments := toCommentSlice(raw)
		var diffContent string
		for i := len(comments) - 1; i >= 0; i-- {
			if comments[i].Type == "diff" {
				diffContent = comments[i].Content
				break
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"diff": diffContent, "taskId": taskID})
		return
	}

	// POST /api/tasks/{id}/review-comment
	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "review-comment" {
		var req struct {
			File    string `json:"file"`
			Line    int    `json:"line"`
			Comment string `json:"comment"`
			Author  string `json:"author"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid request: %v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Comment == "" {
			http.Error(w, `{"error":"comment is required"}`, http.StatusBadRequest)
			return
		}
		if req.Author == "" {
			req.Author = "user"
		}
		reviewData, _ := json.Marshal(map[string]any{
			"file":    req.File,
			"line":    req.Line,
			"comment": req.Comment,
		})
		comment, err := h.d.AddComment(taskID, req.Author, string(reviewData), "review")
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(comment)
		if h.d.PublishBoardUpdate != nil {
			h.d.PublishBoardUpdate(map[string]any{"taskId": taskID, "action": "review_comment", "comment": comment})
		}
		return
	}

	// POST /api/tasks/{id}/review-feedback
	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "review-feedback" {
		var req struct {
			Action  string `json:"action"`
			Summary string `json:"summary"`
			Author  string `json:"author"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid request: %v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Action != "approve" && req.Action != "request-changes" {
			http.Error(w, `{"error":"action must be 'approve' or 'request-changes'"}`, http.StatusBadRequest)
			return
		}
		if req.Author == "" {
			req.Author = "user"
		}

		// Compile all review comments into feedback.
		rawComments, _ := h.d.GetThread(taskID)
		comments := toCommentSlice(rawComments)
		var reviewComments []map[string]any
		for _, c := range comments {
			if c.Type == "review" {
				var data map[string]any
				if json.Unmarshal([]byte(c.Content), &data) == nil {
					reviewComments = append(reviewComments, data)
				}
			}
		}

		feedbackData, _ := json.Marshal(map[string]any{
			"action":         req.Action,
			"summary":        req.Summary,
			"reviewComments": reviewComments,
		})

		var feedbackMsg string
		if req.Action == "approve" {
			feedbackMsg = fmt.Sprintf("[REVIEW APPROVED] %s", req.Summary)
		} else {
			feedbackMsg = fmt.Sprintf("[REVIEW: CHANGES REQUESTED] %s\n\nInline comments: %d", req.Summary, len(reviewComments))
		}
		h.d.AddComment(taskID, req.Author, feedbackMsg, "system") //nolint:errcheck

		if req.Action == "approve" {
			h.d.MoveTask(taskID, "done") //nolint:errcheck
		}

		json.NewEncoder(w).Encode(map[string]any{
			"status":       "ok",
			"action":       req.Action,
			"feedback":     json.RawMessage(feedbackData),
			"commentCount": len(reviewComments),
		})
		if h.d.PublishBoardUpdate != nil {
			h.d.PublishBoardUpdate(map[string]any{"taskId": taskID, "action": "review_feedback", "reviewAction": req.Action})
		}
		return
	}

	http.Error(w, `{"error":"invalid path or method"}`, http.StatusBadRequest)
}

// --- Quick Actions ---

func (h *agentHandler) handleQuickList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		return
	}
	actions := h.d.ListQuickActions()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(actions)
}

func (h *agentHandler) handleQuickRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name   string         `json:"name"`
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid request: %v"}`, err), http.StatusBadRequest)
		return
	}
	result, err := h.d.RunQuickAction(r.Context(), req.Name, req.Params)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *agentHandler) handleQuickSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query().Get("q")
	actions := h.d.SearchQuickActions(query)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(actions)
}

// --- Agent Communication ---

func (h *agentHandler) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		return
	}
	result, err := h.d.ListAgents(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(result))
}

func (h *agentHandler) handleAgentMessages2(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		return
	}
	role := r.URL.Query().Get("role")
	if role == "" {
		http.Error(w, `{"error":"role parameter required"}`, http.StatusBadRequest)
		return
	}
	markAsRead := r.URL.Query().Get("markAsRead") == "true"
	messages, err := h.d.GetAgentMessages(role, markAsRead)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Wrap in {messages, count} — replicate original behaviour.
	type response struct {
		Messages any `json:"messages"`
		Count    int `json:"count"`
	}
	var count int
	if ms, ok := toAnySlice(messages); ok {
		count = len(ms)
	}
	json.NewEncoder(w).Encode(response{Messages: messages, Count: count})
}

func (h *agentHandler) handleAgentMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}
	raw, err := readBody(r)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
		return
	}
	result, err := h.d.SendAgentMsg(r.Context(), raw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(result))
}

func (h *agentHandler) handleRunningAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		return
	}
	result := h.d.GetRunningAgents()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// --- Trust ---

func (h *agentHandler) handleTrust(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	statuses := h.d.GetAllTrustStatuses()
	json.NewEncoder(w).Encode(statuses)
}

func (h *agentHandler) handleTrustAgent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	agentName := strings.TrimPrefix(r.URL.Path, "/trust/")
	agentName = strings.TrimSuffix(agentName, "/")
	if agentName == "" {
		http.Error(w, `{"error":"agent name required"}`, http.StatusBadRequest)
		return
	}
	if !h.d.AgentExists(agentName) {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		status := h.d.GetTrustStatus(agentName)
		json.NewEncoder(w).Encode(status)

	case http.MethodPost, http.MethodPut:
		var body struct {
			Level string `json:"level"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if !h.d.IsValidTrustLevel(body.Level) {
			http.Error(w, fmt.Sprintf(`{"error":"invalid level, valid: %s"}`,
				strings.Join(h.d.ValidTrustLevels(), ", ")), http.StatusBadRequest)
			return
		}
		status, err := h.d.SetTrustLevel(agentName, body.Level, clientIP(r))
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(status)

	default:
		http.Error(w, `{"error":"GET or POST"}`, http.StatusMethodNotAllowed)
	}
}

func (h *agentHandler) handleTrustEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	role := r.URL.Query().Get("role")
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	events, err := h.d.QueryTrustEvents(role, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []map[string]any{}
	}
	json.NewEncoder(w).Encode(events)
}

// --- helpers ---

// toCommentSlice converts an any value (expected to be a slice of comment-like
// structs) into []comment by JSON round-trip, so the handler can inspect Type/Content.
func toCommentSlice(v any) []comment {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var result []comment
	if err := json.Unmarshal(b, &result); err != nil {
		return nil
	}
	return result
}

// toAnySlice attempts to convert a value to []any. It handles the case where
// the value is already a slice via JSON round-trip.
func toAnySlice(v any) ([]any, bool) {
	if v == nil {
		return nil, true
	}
	if s, ok := v.([]any); ok {
		return s, true
	}
	// Round-trip through JSON to handle typed slices.
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var result []any
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, false
	}
	return result, true
}

// extractStringField extracts a string field from an any value (expected to be
// a map or struct) by JSON round-trip.
func extractStringField(v any, field string) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return ""
	}
	if s, ok := m[field].(string); ok {
		return s
	}
	return ""
}
