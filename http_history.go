package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) registerHistoryRoutes(mux *http.ServeMux) {
	cfg := s.cfg

	// --- History ---
	mux.HandleFunc("/history", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		q := HistoryQuery{
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

		runs, total, err := queryHistoryFiltered(cfg.HistoryDB, q)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if runs == nil {
			runs = []JobRun{}
		}

		page := (q.Offset / q.Limit) + 1
		json.NewEncoder(w).Encode(map[string]any{
			"runs":  runs,
			"total": total,
			"page":  page,
			"limit": q.Limit,
		})
	})

	// --- Subtask counts for decomposed parents ---
	// GET /history/subtask-counts?parents=id1,id2,id3
	mux.HandleFunc("/history/subtask-counts", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
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
		counts, err := queryParentSubtaskCounts(cfg.HistoryDB, ids)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if counts == nil {
			counts = map[string]SubtaskCount{}
		}
		json.NewEncoder(w).Encode(counts)
	})

	mux.HandleFunc("/history/", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
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

		run, err := queryHistoryByID(cfg.HistoryDB, id)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if run == nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(run)
	})
}
