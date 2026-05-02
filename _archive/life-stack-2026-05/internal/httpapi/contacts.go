package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"tetora/internal/audit"
	"tetora/internal/httputil"
	"tetora/internal/life/contacts"
	"tetora/internal/trace"
)

// RegisterContactsRoutes registers HTTP routes for the contacts API.
func RegisterContactsRoutes(mux *http.ServeMux, svc *contacts.Service, dbPath func() string) {
	if svc == nil {
		return
	}
	h := &contactsHandler{svc: svc, dbPath: dbPath}
	mux.HandleFunc("GET /api/contacts", h.handleContactsList)
	mux.HandleFunc("POST /api/contacts", h.handleContactsAdd)
	mux.HandleFunc("GET /api/contacts/search", h.handleContactsSearch)
	mux.HandleFunc("GET /api/contacts/upcoming", h.handleContactsUpcoming)
	mux.HandleFunc("POST /api/contacts/interaction", h.handleContactsLogInteraction)
}

type contactsHandler struct {
	svc    *contacts.Service
	dbPath func() string
}

func (h *contactsHandler) handleContactsList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	relationship := r.URL.Query().Get("relationship")
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limit = v
		}
	}

	result, err := h.svc.ListContacts(relationship, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"contacts": result, "count": len(result)})
}

func (h *contactsHandler) handleContactsAdd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var body struct {
		Name         string            `json:"name"`
		Nickname     string            `json:"nickname"`
		Email        string            `json:"email"`
		Phone        string            `json:"phone"`
		Birthday     string            `json:"birthday"`
		Anniversary  string            `json:"anniversary"`
		Notes        string            `json:"notes"`
		Tags         []string          `json:"tags"`
		ChannelIDs   map[string]string `json:"channel_ids"`
		Relationship string            `json:"relationship"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	contact := &contacts.Contact{
		ID:           trace.NewUUID(),
		Name:         body.Name,
		Nickname:     body.Nickname,
		Email:        body.Email,
		Phone:        body.Phone,
		Birthday:     body.Birthday,
		Anniversary:  body.Anniversary,
		Notes:        body.Notes,
		Tags:         body.Tags,
		ChannelIDs:   body.ChannelIDs,
		Relationship: body.Relationship,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.svc.AddContact(contact); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}

	audit.Log(h.dbPath(), "contacts.add", "http", body.Name, httputil.ClientIP(r))

	json.NewEncoder(w).Encode(map[string]any{"status": "added", "contact": contact})
}

func (h *contactsHandler) handleContactsSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, `{"error":"q query parameter required"}`, http.StatusBadRequest)
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limit = v
		}
	}

	result, err := h.svc.SearchContacts(query, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"contacts": result, "count": len(result)})
}

func (h *contactsHandler) handleContactsUpcoming(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	daysStr := r.URL.Query().Get("days")
	days := 30
	if daysStr != "" {
		if v, err := strconv.Atoi(daysStr); err == nil && v > 0 {
			days = v
		}
	}

	events, err := h.svc.GetUpcomingEvents(days)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"events": events, "count": len(events)})
}

func (h *contactsHandler) handleContactsLogInteraction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var body struct {
		ContactID string `json:"contact_id"`
		Channel   string `json:"channel"`
		Type      string `json:"type"`
		Summary   string `json:"summary"`
		Sentiment string `json:"sentiment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if body.ContactID == "" {
		http.Error(w, `{"error":"contact_id is required"}`, http.StatusBadRequest)
		return
	}
	if body.Type == "" {
		body.Type = "message"
	}

	if err := h.svc.LogInteraction(trace.NewUUID(), body.ContactID, body.Channel, body.Type, body.Summary, body.Sentiment); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}

	audit.Log(h.dbPath(), "contacts.interaction", "http", body.ContactID, httputil.ClientIP(r))

	json.NewEncoder(w).Encode(map[string]any{"status": "logged", "contact_id": body.ContactID, "type": body.Type})
}
