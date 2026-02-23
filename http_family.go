package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// registerFamilyRoutes registers HTTP routes for the family/multi-user API.
func (s *Server) registerFamilyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/family/users", s.handleFamilyUsers)
	mux.HandleFunc("/api/family/lists", s.handleFamilyLists)
	mux.HandleFunc("/api/family/lists/items", s.handleFamilyListItems)
}

// handleFamilyUsers handles CRUD operations for family users.
func (s *Server) handleFamilyUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if globalFamilyService == nil {
		http.Error(w, `{"error":"family mode not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		users, err := globalFamilyService.ListUsers()
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"users": users})

	case http.MethodPost:
		var body struct {
			UserID      string `json:"userId"`
			DisplayName string `json:"displayName"`
			Role        string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if body.UserID == "" {
			http.Error(w, `{"error":"userId is required"}`, http.StatusBadRequest)
			return
		}
		if body.Role == "" {
			body.Role = "member"
		}
		if err := globalFamilyService.AddUser(body.UserID, body.DisplayName, body.Role); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}
		user, _ := globalFamilyService.GetUser(body.UserID)
		s.cfgMu.RLock()
		cfg := s.cfg
		s.cfgMu.RUnlock()
		auditLog(cfg.HistoryDB, "family.user.add", "http", body.UserID, clientIP(r))
		json.NewEncoder(w).Encode(map[string]any{"status": "added", "user": user})

	case http.MethodDelete:
		userID := r.URL.Query().Get("userId")
		if userID == "" {
			http.Error(w, `{"error":"userId query parameter required"}`, http.StatusBadRequest)
			return
		}
		if err := globalFamilyService.RemoveUser(userID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}
		s.cfgMu.RLock()
		cfg := s.cfg
		s.cfgMu.RUnlock()
		auditLog(cfg.HistoryDB, "family.user.remove", "http", userID, clientIP(r))
		json.NewEncoder(w).Encode(map[string]any{"status": "removed", "userId": userID})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleFamilyLists handles CRUD operations for shared lists.
func (s *Server) handleFamilyLists(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if globalFamilyService == nil {
		http.Error(w, `{"error":"family mode not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		lists, err := globalFamilyService.ListLists()
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"lists": lists})

	case http.MethodPost:
		var body struct {
			Name      string `json:"name"`
			ListType  string `json:"listType"`
			CreatedBy string `json:"createdBy"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if body.Name == "" {
			http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
			return
		}
		list, err := globalFamilyService.CreateList(body.Name, body.ListType, body.CreatedBy)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		s.cfgMu.RLock()
		cfg := s.cfg
		s.cfgMu.RUnlock()
		auditLog(cfg.HistoryDB, "family.list.create", "http", body.Name, clientIP(r))
		json.NewEncoder(w).Encode(map[string]any{"status": "created", "list": list})

	case http.MethodDelete:
		listID := r.URL.Query().Get("listId")
		if listID == "" {
			http.Error(w, `{"error":"listId query parameter required"}`, http.StatusBadRequest)
			return
		}
		if err := globalFamilyService.DeleteList(listID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}
		s.cfgMu.RLock()
		cfg := s.cfg
		s.cfgMu.RUnlock()
		auditLog(cfg.HistoryDB, "family.list.delete", "http", listID, clientIP(r))
		json.NewEncoder(w).Encode(map[string]any{"status": "deleted", "listId": listID})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleFamilyListItems handles CRUD operations for shared list items.
func (s *Server) handleFamilyListItems(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if globalFamilyService == nil {
		http.Error(w, `{"error":"family mode not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		listID := r.URL.Query().Get("listId")
		if listID == "" {
			http.Error(w, `{"error":"listId query parameter required"}`, http.StatusBadRequest)
			return
		}
		items, err := globalFamilyService.GetListItems(listID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items})

	case http.MethodPost:
		var body struct {
			ListID   string `json:"listId"`
			Text     string `json:"text"`
			Quantity string `json:"quantity"`
			AddedBy  string `json:"addedBy"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if body.ListID == "" || body.Text == "" {
			http.Error(w, `{"error":"listId and text are required"}`, http.StatusBadRequest)
			return
		}
		item, err := globalFamilyService.AddListItem(body.ListID, body.Text, body.Quantity, body.AddedBy)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "added", "item": item})

	case http.MethodPut:
		itemIDStr := r.URL.Query().Get("itemId")
		checkedStr := r.URL.Query().Get("checked")
		if itemIDStr == "" {
			http.Error(w, `{"error":"itemId query parameter required"}`, http.StatusBadRequest)
			return
		}
		itemID, err := strconv.Atoi(itemIDStr)
		if err != nil {
			http.Error(w, `{"error":"invalid itemId"}`, http.StatusBadRequest)
			return
		}
		checked := checkedStr == "true" || checkedStr == "1"
		if err := globalFamilyService.CheckItem(itemID, checked); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "updated", "itemId": itemID, "checked": checked})

	case http.MethodDelete:
		itemIDStr := r.URL.Query().Get("itemId")
		if itemIDStr == "" {
			http.Error(w, `{"error":"itemId query parameter required"}`, http.StatusBadRequest)
			return
		}
		itemID, err := strconv.Atoi(itemIDStr)
		if err != nil {
			http.Error(w, `{"error":"invalid itemId"}`, http.StatusBadRequest)
			return
		}
		if err := globalFamilyService.RemoveListItem(itemID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "removed", "itemId": itemID})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}
