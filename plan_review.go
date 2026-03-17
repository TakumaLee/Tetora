package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"


	"tetora/internal/db"
)

// --- Plan Review System ---
// Tracks plan mode approvals for rich Discord/Dashboard review experience.

// PlanReview represents a plan waiting for or having received review.
type PlanReview struct {
	ID         string `json:"id"`
	SessionID  string `json:"sessionId"`
	WorkerName string `json:"workerName,omitempty"`
	Agent      string `json:"agent,omitempty"`
	PlanText   string `json:"planText"`
	Status     string `json:"status"` // pending, approved, rejected
	Reviewer   string `json:"reviewer,omitempty"`
	ReviewNote string `json:"reviewNote,omitempty"`
	CreatedAt  string `json:"createdAt"`
	ReviewedAt string `json:"reviewedAt,omitempty"`
}

// initPlanReviewDB creates the plan_reviews table if it doesn't exist.
func initPlanReviewDB(dbPath string) error {
	sql := `
CREATE TABLE IF NOT EXISTS plan_reviews (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    worker_name TEXT DEFAULT '',
    agent TEXT DEFAULT '',
    plan_text TEXT NOT NULL,
    status TEXT DEFAULT 'pending',
    reviewer TEXT DEFAULT '',
    review_note TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    reviewed_at TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_plan_reviews_session ON plan_reviews(session_id);
CREATE INDEX IF NOT EXISTS idx_plan_reviews_status ON plan_reviews(status);
`
	return db.Exec(dbPath, sql)
}

// insertPlanReview creates a new plan review record.
func insertPlanReview(dbPath string, review *PlanReview) error {
	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO plan_reviews (id, session_id, worker_name, agent, plan_text, status, created_at)
		 VALUES ('%s','%s','%s','%s','%s','pending','%s')`,
		db.Escape(review.ID),
		db.Escape(review.SessionID),
		db.Escape(review.WorkerName),
		db.Escape(review.Agent),
		db.Escape(review.PlanText),
		db.Escape(review.CreatedAt),
	)
	return db.Exec(dbPath, sql)
}

// updatePlanReviewStatus marks a plan review as approved or rejected.
func updatePlanReviewStatus(dbPath, id, status, reviewer, note string) error {
	sql := fmt.Sprintf(
		`UPDATE plan_reviews SET status='%s', reviewer='%s', review_note='%s', reviewed_at='%s' WHERE id='%s'`,
		db.Escape(status),
		db.Escape(reviewer),
		db.Escape(note),
		db.Escape(time.Now().Format(time.RFC3339)),
		db.Escape(id),
	)
	return db.Exec(dbPath, sql)
}

// listPendingPlanReviews returns all pending plan reviews.
func listPendingPlanReviews(dbPath string) ([]PlanReview, error) {
	sql := `SELECT id, session_id, worker_name, agent, plan_text, status, reviewer, review_note, created_at, reviewed_at FROM plan_reviews WHERE status='pending' ORDER BY created_at DESC`
	return queryPlanReviews(dbPath, sql)
}

// listRecentPlanReviews returns recent plan reviews (all statuses).
func listRecentPlanReviews(dbPath string, limit int) ([]PlanReview, error) {
	if limit <= 0 {
		limit = 20
	}
	sql := fmt.Sprintf(`SELECT id, session_id, worker_name, agent, plan_text, status, reviewer, review_note, created_at, reviewed_at FROM plan_reviews ORDER BY created_at DESC LIMIT %d`, limit)
	return queryPlanReviews(dbPath, sql)
}

func queryPlanReviews(dbPath, sqlStr string) ([]PlanReview, error) {
	rows, err := db.Query(dbPath, sqlStr)
	if err != nil {
		return nil, err
	}
	var reviews []PlanReview
	for _, row := range rows {
		reviews = append(reviews, PlanReview{
			ID:         mapStr(row, "id"),
			SessionID:  mapStr(row, "session_id"),
			WorkerName: mapStr(row, "worker_name"),
			Agent:      mapStr(row, "agent"),
			PlanText:   mapStr(row, "plan_text"),
			Status:     mapStr(row, "status"),
			Reviewer:   mapStr(row, "reviewer"),
			ReviewNote: mapStr(row, "review_note"),
			CreatedAt:  mapStr(row, "created_at"),
			ReviewedAt: mapStr(row, "reviewed_at"),
		})
	}
	return reviews, nil
}

func mapStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// --- HTTP API ---

// registerPlanReviewRoutes registers plan review API endpoints.
func (s *Server) registerPlanReviewRoutes(mux *http.ServeMux) {
	cfg := s.cfg

	// GET /api/plan-reviews — list plan reviews.
	mux.HandleFunc("/api/plan-reviews", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		status := r.URL.Query().Get("status")
		var reviews []PlanReview
		var err error
		if status == "pending" {
			reviews, err = listPendingPlanReviews(cfg.HistoryDB)
		} else {
			reviews, err = listRecentPlanReviews(cfg.HistoryDB, 50)
		}
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		if reviews == nil {
			reviews = []PlanReview{}
		}
		json.NewEncoder(w).Encode(reviews)
	})

	// POST /api/plan-reviews/{id}/approve — approve a plan.
	// POST /api/plan-reviews/{id}/reject — reject a plan.
	mux.HandleFunc("/api/plan-reviews/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/api/plan-reviews/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[0] == "" {
			http.Error(w, `{"error":"invalid path, use /api/plan-reviews/{id}/approve or /reject"}`, http.StatusBadRequest)
			return
		}

		reviewID := parts[0]
		action := parts[1]

		if action != "approve" && action != "reject" {
			http.Error(w, `{"error":"action must be approve or reject"}`, http.StatusBadRequest)
			return
		}

		var body struct {
			Reviewer string `json:"reviewer"`
			Note     string `json:"note"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		status := "approved"
		if action == "reject" {
			status = "rejected"
		}

		if err := updatePlanReviewStatus(cfg.HistoryDB, reviewID, status, body.Reviewer, body.Note); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}

		// Publish review decision to SSE.
		if s.hookReceiver != nil && s.hookReceiver.broker != nil {
			s.hookReceiver.broker.Publish(SSEDashboardKey, SSEEvent{
				Type: SSEPlanReview,
				Data: map[string]any{
					"reviewId": reviewID,
					"action":   action,
					"reviewer": body.Reviewer,
				},
			})
		}

		auditLog(cfg.HistoryDB, "plan_review."+action, "http",
			fmt.Sprintf("id=%s reviewer=%s", reviewID, body.Reviewer), clientIP(r))

		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "action": action})
	})
}

// --- Discord Plan Review Formatting ---

// buildPlanReviewEmbed creates a rich Discord embed for plan review.
func buildPlanReviewEmbed(review *PlanReview) discordEmbed {
	color := 0x3498db // Blue for pending

	// Truncate plan text for Discord embed (max 4096 chars for description).
	planPreview := review.PlanText
	if len(planPreview) > 3500 {
		planPreview = planPreview[:3500] + "\n\n... (truncated, see dashboard for full plan)"
	}

	embed := discordEmbed{
		Title:       "Plan Review Required",
		Description: planPreview,
		Color:       color,
		Fields: []discordEmbedField{
			{Name: "Session", Value: truncate(review.SessionID, 36), Inline: true},
		},
		Timestamp: review.CreatedAt,
	}

	if review.Agent != "" {
		embed.Fields = append(embed.Fields, discordEmbedField{
			Name: "Agent", Value: review.Agent, Inline: true,
		})
	}
	if review.WorkerName != "" {
		embed.Fields = append(embed.Fields, discordEmbedField{
			Name: "Worker", Value: review.WorkerName, Inline: true,
		})
	}

	return embed
}

// buildPlanReviewComponents creates Approve/Reject/Request Changes buttons.
func buildPlanReviewComponents(reviewID string) []discordComponent {
	return []discordComponent{
		discordActionRow(
			discordButton("plan_approve:"+reviewID, "Approve", buttonStyleSuccess),
			discordButton("plan_reject:"+reviewID, "Reject", buttonStyleDanger),
		),
	}
}
