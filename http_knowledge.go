package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

func (s *Server) registerKnowledgeRoutes(mux *http.ServeMux) {
	cfg := s.cfg

	// --- Knowledge Search ---
	mux.HandleFunc("/knowledge/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("q")
		if q == "" {
			json.NewEncoder(w).Encode([]SearchResult{})
			return
		}
		limit := 10
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		idx, err := buildKnowledgeIndex(cfg.KnowledgeDir)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		results := idx.search(q, limit)
		if results == nil {
			results = []SearchResult{}
		}
		json.NewEncoder(w).Encode(results)
	})

	// --- Reflections ---
	mux.HandleFunc("/reflections", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		role := r.URL.Query().Get("role")
		limit := 20
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		refs, err := queryReflections(cfg.HistoryDB, role, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if refs == nil {
			refs = []ReflectionResult{}
		}
		json.NewEncoder(w).Encode(refs)
	})
}
