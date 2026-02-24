package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func (s *Server) registerPushRoutes(mux *http.ServeMux) {
	cfg := s.cfg

	// --- Web Push ---
	var pushManager *PushManager
	if cfg.Push.Enabled {
		pushManager = newPushManager(cfg)
		logInfo("push: web push notifications enabled")
	}

	mux.HandleFunc("/api/push/vapid-key", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if pushManager == nil {
			http.Error(w, `{"error":"push notifications not enabled"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"publicKey": cfg.Push.VAPIDPublicKey,
		})
	})

	mux.HandleFunc("/api/push/subscribe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if pushManager == nil {
			http.Error(w, `{"error":"push notifications not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		var sub PushSubscription
		if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}

		// Set user agent from request.
		sub.UserAgent = r.Header.Get("User-Agent")

		if err := pushManager.Subscribe(sub); err != nil {
			logErrorCtx(r.Context(), "push subscribe failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"subscribed"}`))
	})

	mux.HandleFunc("/api/push/unsubscribe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if pushManager == nil {
			http.Error(w, `{"error":"push notifications not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		var req struct {
			Endpoint string `json:"endpoint"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Endpoint == "" {
			http.Error(w, `{"error":"endpoint required"}`, http.StatusBadRequest)
			return
		}

		if err := pushManager.Unsubscribe(req.Endpoint); err != nil {
			logErrorCtx(r.Context(), "push unsubscribe failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"unsubscribed"}`))
	})

	mux.HandleFunc("/api/push/test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if pushManager == nil {
			http.Error(w, `{"error":"push notifications not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		var notif PushNotification
		if err := json.NewDecoder(r.Body).Decode(&notif); err != nil {
			// Use default test notification if no body provided.
			notif = PushNotification{
				Title: "Tetora Test Notification",
				Body:  "This is a test push notification from Tetora",
				Icon:  "/dashboard/icon-192.png",
			}
		}

		if err := pushManager.SendNotification(notif); err != nil {
			logErrorCtx(r.Context(), "push test failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"sent"}`))
	})

	mux.HandleFunc("/api/push/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if pushManager == nil {
			http.Error(w, `{"error":"push notifications not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		subs := pushManager.ListSubscriptions()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"subscriptions": subs,
			"count":         len(subs),
		})
	})

	// --- Pairing ---
	pairingManager := newPairingManager(cfg)

	mux.HandleFunc("/api/pairing/pending", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		pending := pairingManager.ListPending()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"pending": pending,
			"count":   len(pending),
		})
	})

	mux.HandleFunc("/api/pairing/approve", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Code == "" {
			http.Error(w, `{"error":"code required"}`, http.StatusBadRequest)
			return
		}

		approved, err := pairingManager.Approve(req.Code)
		if err != nil {
			logErrorCtx(r.Context(), "pairing approve failed", "code", req.Code, "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":  "approved",
			"channel": approved.Channel,
			"userId":  approved.UserID,
		})
	})

	mux.HandleFunc("/api/pairing/reject", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Code == "" {
			http.Error(w, `{"error":"code required"}`, http.StatusBadRequest)
			return
		}

		if err := pairingManager.Reject(req.Code); err != nil {
			logErrorCtx(r.Context(), "pairing reject failed", "code", req.Code, "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"rejected"}`))
	})

	mux.HandleFunc("/api/pairing/approved", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		approved, err := pairingManager.ListApproved()
		if err != nil {
			logErrorCtx(r.Context(), "list approved failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"approved": approved,
			"count":    len(approved),
		})
	})

	mux.HandleFunc("/api/pairing/revoke", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Channel string `json:"channel"`
			UserID  string `json:"userId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Channel == "" || req.UserID == "" {
			http.Error(w, `{"error":"channel and userId required"}`, http.StatusBadRequest)
			return
		}

		if err := pairingManager.Revoke(req.Channel, req.UserID); err != nil {
			logErrorCtx(r.Context(), "pairing revoke failed", "channel", req.Channel, "userId", req.UserID, "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"revoked"}`))
	})
}
