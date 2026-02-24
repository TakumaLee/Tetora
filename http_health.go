package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) registerHealthRoutes(mux *http.ServeMux) {
	cfg := s.cfg

	// --- Health ---
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		checks := deepHealthCheck(cfg, s.state, s.cron, s.startTime)
		// Report degraded services.
		if len(s.DegradedServices) > 0 {
			checks["degradedServices"] = s.DegradedServices
			if st, ok := checks["status"].(string); ok {
				checks["status"] = degradeStatus(st, "degraded")
			}
		}
		b, _ := json.MarshalIndent(checks, "", "  ")
		w.Write(b)
	})

	// --- Metrics ---
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if metrics == nil {
			http.Error(w, "metrics not initialized", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		metrics.WriteTo(w)
	})

	// --- Circuit Breakers ---
	mux.HandleFunc("/circuits", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var status map[string]any
		if cfg.circuits != nil {
			status = cfg.circuits.status()
		} else {
			status = map[string]any{}
		}
		b, _ := json.MarshalIndent(status, "", "  ")
		w.Write(b)
	})

	mux.HandleFunc("/circuits/", func(w http.ResponseWriter, r *http.Request) {
		// POST /circuits/{provider}/reset
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/circuits/")
		provider := strings.TrimSuffix(path, "/reset")
		if provider == "" || !strings.HasSuffix(path, "/reset") {
			http.Error(w, `{"error":"use POST /circuits/{provider}/reset"}`, http.StatusBadRequest)
			return
		}
		if cfg.circuits == nil {
			http.Error(w, `{"error":"circuit breaker not initialized"}`, http.StatusServiceUnavailable)
			return
		}
		if ok := cfg.circuits.reset(provider); !ok {
			http.Error(w, `{"error":"provider not found"}`, http.StatusNotFound)
			return
		}
		auditLog(cfg.HistoryDB, "circuit.reset", r.RemoteAddr, provider, "")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"provider":%q,"state":"closed"}`, provider)))
	})
}
