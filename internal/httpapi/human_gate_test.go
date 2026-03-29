package httpapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"tetora/internal/httpapi"
)

// testHumanGateDeps returns a HumanGateDeps backed by an in-memory store.
func testHumanGateDeps() (httpapi.HumanGateDeps, func(key, action, response, respondedBy string)) {
	type gate struct {
		Key          string `json:"key"`
		RunID        string `json:"runId"`
		StepID       string `json:"stepId"`
		WorkflowName string `json:"workflowName"`
		Subtype      string `json:"subtype"`
		Prompt       string `json:"prompt"`
		Assignee     string `json:"assignee"`
		Status       string `json:"status"`
		Decision     string `json:"decision"`
		Response     string `json:"response"`
		RespondedBy  string `json:"respondedBy"`
	}

	store := map[string]*gate{
		"gate-1": {Key: "gate-1", RunID: "run-1", StepID: "step-1", WorkflowName: "wf-alpha", Subtype: "approval", Prompt: "Please approve", Status: "waiting"},
		"gate-2": {Key: "gate-2", RunID: "run-2", StepID: "step-2", WorkflowName: "wf-beta", Subtype: "input", Prompt: "Enter value", Status: "waiting"},
		"gate-3": {Key: "gate-3", RunID: "run-3", StepID: "step-1", WorkflowName: "wf-gamma", Subtype: "action", Prompt: "Do something", Status: "completed", Decision: "done"},
	}

	toMap := func(g *gate) map[string]any {
		b, _ := json.Marshal(g)
		var m map[string]any
		json.Unmarshal(b, &m)
		return m
	}

	var lastResponded struct{ key, action, response, respondedBy string }

	deps := httpapi.HumanGateDeps{
		HistoryDB: func() string { return ":memory:" },
		QueryHumanGates: func(status string) []map[string]any {
			var out []map[string]any
			for _, g := range store {
				if status == "" || g.Status == status {
					out = append(out, toMap(g))
				}
			}
			return out
		},
		CountHumanGates: func() int {
			n := 0
			for _, g := range store {
				if g.Status == "waiting" {
					n++
				}
			}
			return n
		},
		QueryHumanGateByKey: func(key string) map[string]any {
			g, ok := store[key]
			if !ok {
				return nil
			}
			return toMap(g)
		},
		RespondHumanGate: func(key, action, response, respondedBy string) error {
			lastResponded.key = key
			lastResponded.action = action
			lastResponded.response = response
			lastResponded.respondedBy = respondedBy
			if g, ok := store[key]; ok {
				g.Status = "completed"
				g.Decision = action
				g.Response = response
				g.RespondedBy = respondedBy
			}
			return nil
		},
		CancelHumanGate: func(key, reason, cancelledBy string) error {
			g, ok := store[key]
			if !ok {
				return errors.New("gate not found")
			}
			g.Status = "cancelled"
			return nil
		},
	}
	return deps, func(key, action, response, respondedBy string) {
		lastResponded.key = key
		lastResponded.action = action
		lastResponded.response = response
		lastResponded.respondedBy = respondedBy
	}
}

func TestHumanGateListWaiting(t *testing.T) {
	deps, _ := testHumanGateDeps()
	mux := http.NewServeMux()
	httpapi.RegisterHumanGateRoutes(mux, deps)

	t.Run("Given status=waiting, When two waiting gates exist, Then both returned", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/human-gates?status=waiting", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var gates []map[string]any
		if err := json.NewDecoder(w.Body).Decode(&gates); err != nil {
			t.Fatalf("decode error: %v", err)
		}
		if len(gates) != 2 {
			t.Fatalf("expected 2 waiting gates, got %d", len(gates))
		}
	})

	t.Run("Given no status param, When queried, Then defaults to waiting gates", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/human-gates", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var gates []map[string]any
		json.NewDecoder(w.Body).Decode(&gates)
		if len(gates) != 2 {
			t.Fatalf("expected 2 (default=waiting), got %d", len(gates))
		}
	})

	t.Run("Given status=completed, When one completed gate, Then one returned", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/human-gates?status=completed", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		var gates []map[string]any
		json.NewDecoder(w.Body).Decode(&gates)
		if len(gates) != 1 {
			t.Fatalf("expected 1, got %d", len(gates))
		}
	})
}

func TestHumanGateCount(t *testing.T) {
	deps, _ := testHumanGateDeps()
	mux := http.NewServeMux()
	httpapi.RegisterHumanGateRoutes(mux, deps)

	t.Run("Given two waiting gates, When count requested, Then count=2", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/human-gates/count", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var result map[string]int
		if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
			t.Fatalf("decode error: %v", err)
		}
		if result["count"] != 2 {
			t.Fatalf("expected count=2, got %d", result["count"])
		}
	})
}

func TestHumanGateDetail(t *testing.T) {
	deps, _ := testHumanGateDeps()
	mux := http.NewServeMux()
	httpapi.RegisterHumanGateRoutes(mux, deps)

	t.Run("Given existing gate key, When GET, Then gate returned", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/human-gates/gate-1", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var gate map[string]any
		json.NewDecoder(w.Body).Decode(&gate)
		if gate["key"] != "gate-1" {
			t.Errorf("expected key=gate-1, got %v", gate["key"])
		}
	})

	t.Run("Given unknown gate key, When GET, Then 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/human-gates/nonexistent", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})
}

func TestHumanGateRespond(t *testing.T) {
	for _, tt := range []struct {
		name        string
		action      string
		wantStatus  int
	}{
		{"approve", "approve", http.StatusOK},
		{"reject", "reject", http.StatusOK},
		{"complete", "complete", http.StatusOK},
		{"submit", "submit", http.StatusOK},
	} {
		t.Run("Given waiting gate, When action="+tt.name+", Then 200 and delivered", func(t *testing.T) {
			deps, _ := testHumanGateDeps()
			mux := http.NewServeMux()
			httpapi.RegisterHumanGateRoutes(mux, deps)

			body, _ := json.Marshal(map[string]string{
				"action":      tt.action,
				"response":    "LGTM",
				"respondedBy": "takuma",
			})
			req := httptest.NewRequest(http.MethodPost, "/api/human-gates/gate-1/respond", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, w.Code, w.Body.String())
			}
			var result map[string]string
			json.NewDecoder(w.Body).Decode(&result)
			if result["status"] != "delivered" {
				t.Errorf("expected status=delivered, got %v", result["status"])
			}
		})
	}

	t.Run("Given non-waiting gate, When respond, Then 400", func(t *testing.T) {
		deps, _ := testHumanGateDeps()
		mux := http.NewServeMux()
		httpapi.RegisterHumanGateRoutes(mux, deps)

		body, _ := json.Marshal(map[string]string{"action": "approve", "respondedBy": "takuma"})
		req := httptest.NewRequest(http.MethodPost, "/api/human-gates/gate-3/respond", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("Given non-existent gate, When respond, Then 404", func(t *testing.T) {
		deps, _ := testHumanGateDeps()
		mux := http.NewServeMux()
		httpapi.RegisterHumanGateRoutes(mux, deps)

		body, _ := json.Marshal(map[string]string{"action": "approve", "respondedBy": "takuma"})
		req := httptest.NewRequest(http.MethodPost, "/api/human-gates/no-such-gate/respond", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})

	t.Run("Given missing action field, When respond, Then 400", func(t *testing.T) {
		deps, _ := testHumanGateDeps()
		mux := http.NewServeMux()
		httpapi.RegisterHumanGateRoutes(mux, deps)

		body, _ := json.Marshal(map[string]string{"response": "something"})
		req := httptest.NewRequest(http.MethodPost, "/api/human-gates/gate-1/respond", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})
}

func TestHumanGateCancel(t *testing.T) {
	t.Run("Given waiting gate, When cancel, Then 200 and status=cancelled", func(t *testing.T) {
		deps, _ := testHumanGateDeps()
		mux := http.NewServeMux()
		httpapi.RegisterHumanGateRoutes(mux, deps)

		body, _ := json.Marshal(map[string]string{"reason": "no longer needed", "cancelledBy": "takuma"})
		req := httptest.NewRequest(http.MethodPost, "/api/human-gates/gate-1/cancel", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var result map[string]string
		if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
			t.Fatalf("decode error: %v", err)
		}
		if result["status"] != "cancelled" {
			t.Errorf("expected status=cancelled, got %v", result["status"])
		}
		if result["key"] != "gate-1" {
			t.Errorf("expected key=gate-1, got %v", result["key"])
		}
	})

	t.Run("Given non-existent gate, When cancel, Then 404", func(t *testing.T) {
		deps, _ := testHumanGateDeps()
		mux := http.NewServeMux()
		httpapi.RegisterHumanGateRoutes(mux, deps)

		body, _ := json.Marshal(map[string]string{"reason": "oops"})
		req := httptest.NewRequest(http.MethodPost, "/api/human-gates/no-such-gate/cancel", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("Given non-waiting gate, When cancel, Then 400", func(t *testing.T) {
		deps, _ := testHumanGateDeps()
		mux := http.NewServeMux()
		httpapi.RegisterHumanGateRoutes(mux, deps)

		body, _ := json.Marshal(map[string]string{"reason": "too late"})
		req := httptest.NewRequest(http.MethodPost, "/api/human-gates/gate-3/cancel", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})
}
