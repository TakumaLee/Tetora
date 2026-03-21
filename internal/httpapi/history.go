package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"tetora/internal/history"
)

// RegisterHistoryRoutes registers HTTP routes for the history API.
// resolveDB receives the request so it can resolve per-client DB paths.
func RegisterHistoryRoutes(mux *http.ServeMux, resolveDB func(r *http.Request) string) {
	h := &historyHandler{resolveDB: resolveDB}
	mux.HandleFunc("/history", h.handleHistoryList)
	mux.HandleFunc("/history/subtask-counts", h.handleSubtaskCounts)
	mux.HandleFunc("/history/", h.handleHistoryByID)
}

type historyHandler struct {
	resolveDB func(r *http.Request) string
}

func (h *historyHandler) handleHistoryList(w http.ResponseWriter, r *http.Request) {
	db := h.resolveDB(r)
	if db == "" {
		http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	q := history.HistoryQuery{
		JobID:    r.URL.Query().Get("job_id"),
		Status:   r.URL.Query().Get("status"),
		From:     r.URL.Query().Get("from"),
		To:       r.URL.Query().Get("to"),
		Limit:    20,
		ParentID: r.URL.Query().Get("parent_id"),
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			q.Limit = n
		}
	}
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 1 {
			q.Offset = (n - 1) * q.Limit
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			q.Offset = n
		}
	}

	runs, total, err := history.QueryFiltered(db, q)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}
	if runs == nil {
		runs = []history.JobRun{}
	}

	page := (q.Offset / q.Limit) + 1
	json.NewEncoder(w).Encode(map[string]any{
		"runs":  runs,
		"total": total,
		"page":  page,
		"limit": q.Limit,
	})
}

func (h *historyHandler) handleSubtaskCounts(w http.ResponseWriter, r *http.Request) {
	db := h.resolveDB(r)
	if db == "" {
		http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	parentsParam := r.URL.Query().Get("parents")
	if parentsParam == "" {
		json.NewEncoder(w).Encode(map[string]any{})
		return
	}
	ids := strings.Split(parentsParam, ",")
	counts, err := history.QueryParentSubtaskCounts(db, ids)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}
	if counts == nil {
		counts = map[string]history.SubtaskCount{}
	}
	json.NewEncoder(w).Encode(counts)
}

func (h *historyHandler) handleHistoryByID(w http.ResponseWriter, r *http.Request) {
	db := h.resolveDB(r)
	if db == "" {
		http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	idStr := strings.TrimPrefix(r.URL.Path, "/history/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}

	run, err := history.QueryByID(db, id)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}
	if run == nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(run)
}
