package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// registerContactsRoutes registers HTTP routes for the contacts API.
func (s *Server) registerContactsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/contacts", s.handleContactsList)
	mux.HandleFunc("POST /api/contacts", s.handleContactsAdd)
	mux.HandleFunc("GET /api/contacts/search", s.handleContactsSearch)
	mux.HandleFunc("GET /api/contacts/upcoming", s.handleContactsUpcoming)
	mux.HandleFunc("POST /api/contacts/interaction", s.handleContactsLogInteraction)
}

// handleContactsList returns all contacts, optionally filtered by relationship.
func (s *Server) handleContactsList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if globalContactsService == nil {
		http.Error(w, `{"error":"contacts service not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	relationship := r.URL.Query().Get("relationship")
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limit = v
		}
	}

	contacts, err := globalContactsService.ListContacts(relationship, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"contacts": contacts, "count": len(contacts)})
}

// handleContactsAdd creates a new contact.
func (s *Server) handleContactsAdd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if globalContactsService == nil {
		http.Error(w, `{"error":"contacts service not initialized"}`, http.StatusServiceUnavailable)
		return
	}

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

	fields := make(map[string]any)
	if body.Nickname != "" {
		fields["nickname"] = body.Nickname
	}
	if body.Email != "" {
		fields["email"] = body.Email
	}
	if body.Phone != "" {
		fields["phone"] = body.Phone
	}
	if body.Birthday != "" {
		fields["birthday"] = body.Birthday
	}
	if body.Anniversary != "" {
		fields["anniversary"] = body.Anniversary
	}
	if body.Notes != "" {
		fields["notes"] = body.Notes
	}
	if body.Relationship != "" {
		fields["relationship"] = body.Relationship
	}
	if len(body.Tags) > 0 {
		fields["tags"] = body.Tags
	}
	if len(body.ChannelIDs) > 0 {
		fields["channel_ids"] = body.ChannelIDs
	}

	contact, err := globalContactsService.AddContact(body.Name, fields)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}

	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()
	auditLog(cfg.HistoryDB, "contacts.add", "http", body.Name, clientIP(r))

	json.NewEncoder(w).Encode(map[string]any{"status": "added", "contact": contact})
}

// handleContactsSearch searches contacts by query.
func (s *Server) handleContactsSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if globalContactsService == nil {
		http.Error(w, `{"error":"contacts service not initialized"}`, http.StatusServiceUnavailable)
		return
	}

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

	contacts, err := globalContactsService.SearchContacts(query, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"contacts": contacts, "count": len(contacts)})
}

// handleContactsUpcoming returns upcoming birthdays and anniversaries.
func (s *Server) handleContactsUpcoming(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if globalContactsService == nil {
		http.Error(w, `{"error":"contacts service not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	daysStr := r.URL.Query().Get("days")
	days := 30
	if daysStr != "" {
		if v, err := strconv.Atoi(daysStr); err == nil && v > 0 {
			days = v
		}
	}

	events, err := globalContactsService.GetUpcomingEvents(days)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"events": events, "count": len(events)})
}

// handleContactsLogInteraction logs an interaction with a contact.
func (s *Server) handleContactsLogInteraction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if globalContactsService == nil {
		http.Error(w, `{"error":"contacts service not initialized"}`, http.StatusServiceUnavailable)
		return
	}

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

	if err := globalContactsService.LogInteraction(body.ContactID, body.Channel, body.Type, body.Summary, body.Sentiment); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}

	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()
	auditLog(cfg.HistoryDB, "contacts.interaction", "http", body.ContactID, clientIP(r))

	json.NewEncoder(w).Encode(map[string]any{"status": "logged", "contact_id": body.ContactID, "type": body.Type})
}
