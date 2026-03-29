package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"tetora/internal/audit"
	"tetora/internal/httputil"
)

// HumanGateDeps holds dependencies for human gate HTTP handlers.
type HumanGateDeps struct {
	HistoryDB func() string

	// QueryHumanGates returns gates filtered by status ("waiting", "completed", etc.).
	// An empty status returns all gates. Results are []map[string]any with camelCase keys.
	QueryHumanGates func(status string) []map[string]any

	// CountHumanGates returns the number of gates with status="waiting".
	CountHumanGates func() int

	// QueryHumanGateByKey returns a single gate by key, or nil if not found.
	// Result is map[string]any with camelCase keys.
	QueryHumanGateByKey func(key string) map[string]any

	// RespondHumanGate delivers a response to a gate. Returns an error if
	// the gate does not exist or is not in "waiting" status.
	RespondHumanGate func(key, action, response, respondedBy string) error
}

// RegisterHumanGateRoutes registers the human gate REST endpoints:
//
//	GET  /api/human-gates           — list gates (optional ?status=waiting)
//	GET  /api/human-gates/count     — pending count (badge polling)
//	GET  /api/human-gates/{key}     — single gate detail
//	POST /api/human-gates/{key}/respond — respond to a gate
func RegisterHumanGateRoutes(mux *http.ServeMux, d HumanGateDeps) {
	// --- List ---
	mux.HandleFunc("/api/human-gates", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		status := r.URL.Query().Get("status")
		if status == "" {
			status = "waiting" // default to pending gates
		}
		gates := d.QueryHumanGates(status)
		if gates == nil {
			gates = []map[string]any{}
		}
		json.NewEncoder(w).Encode(gates)
	})

	// --- Count, Detail, Respond ---
	mux.HandleFunc("/api/human-gates/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/api/human-gates/")
		parts := strings.SplitN(path, "/", 2)
		key := parts[0]
		subaction := ""
		if len(parts) > 1 {
			subaction = parts[1]
		}

		// GET /api/human-gates/count
		if key == "count" && r.Method == http.MethodGet {
			n := d.CountHumanGates()
			json.NewEncoder(w).Encode(map[string]int{"count": n})
			return
		}

		if key == "" {
			http.Error(w, `{"error":"gate key required"}`, http.StatusBadRequest)
			return
		}

		// POST /api/human-gates/{key}/respond
		if subaction == "respond" {
			if r.Method != http.MethodPost {
				http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
				return
			}

			var body struct {
				Action      string `json:"action"`
				Response    string `json:"response"`
				RespondedBy string `json:"respondedBy"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %v"}`, err), http.StatusBadRequest)
				return
			}
			if body.Action == "" {
				http.Error(w, `{"error":"action is required"}`, http.StatusBadRequest)
				return
			}

			// Check gate exists and is waiting before delivering.
			gate := d.QueryHumanGateByKey(key)
			if gate == nil {
				http.Error(w, `{"error":"gate not found"}`, http.StatusBadRequest)
				return
			}
			if status, _ := gate["status"].(string); status != "waiting" {
				http.Error(w, fmt.Sprintf(`{"error":"gate already %s"}`, status), http.StatusBadRequest)
				return
			}

			if err := d.RespondHumanGate(key, body.Action, body.Response, body.RespondedBy); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}

			audit.Log(d.HistoryDB(), "human_gate.respond", "http",
				fmt.Sprintf("key=%s action=%s respondedBy=%s", key, body.Action, body.RespondedBy),
				httputil.ClientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "delivered", "key": key, "action": body.Action})
			return
		}

		// GET /api/human-gates/{key}
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		gate := d.QueryHumanGateByKey(key)
		if gate == nil {
			http.Error(w, `{"error":"gate not found"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(gate)
	})
}
