package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"tetora/internal/audit"
	"tetora/internal/httputil"
	"tetora/internal/life/family"
	"tetora/internal/trace"
)

// RegisterFamilyRoutes registers HTTP routes for the family/multi-user API.
func RegisterFamilyRoutes(mux *http.ServeMux, svc *family.Service, dbPath func() string) {
	if svc == nil {
		return
	}
	h := &familyHandler{svc: svc, dbPath: dbPath}
	mux.HandleFunc("/api/family/users", h.handleFamilyUsers)
	mux.HandleFunc("/api/family/lists", h.handleFamilyLists)
	mux.HandleFunc("/api/family/lists/items", h.handleFamilyListItems)
}

type familyHandler struct {
	svc    *family.Service
	dbPath func() string
}

func (h *familyHandler) handleFamilyUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		users, err := h.svc.ListUsers()
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
		if err := h.svc.AddUser(body.UserID, body.DisplayName, body.Role); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}
		user, _ := h.svc.GetUser(body.UserID)
		audit.Log(h.dbPath(), "family.user.add", "http", body.UserID, httputil.ClientIP(r))
		json.NewEncoder(w).Encode(map[string]any{"status": "added", "user": user})

	case http.MethodDelete:
		userID := r.URL.Query().Get("userId")
		if userID == "" {
			http.Error(w, `{"error":"userId query parameter required"}`, http.StatusBadRequest)
			return
		}
		if err := h.svc.RemoveUser(userID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}
		audit.Log(h.dbPath(), "family.user.remove", "http", userID, httputil.ClientIP(r))
		json.NewEncoder(w).Encode(map[string]any{"status": "removed", "userId": userID})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *familyHandler) handleFamilyLists(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		lists, err := h.svc.ListLists()
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
		list, err := h.svc.CreateList(body.Name, body.ListType, body.CreatedBy, trace.NewUUID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		audit.Log(h.dbPath(), "family.list.create", "http", body.Name, httputil.ClientIP(r))
		json.NewEncoder(w).Encode(map[string]any{"status": "created", "list": list})

	case http.MethodDelete:
		listID := r.URL.Query().Get("listId")
		if listID == "" {
			http.Error(w, `{"error":"listId query parameter required"}`, http.StatusBadRequest)
			return
		}
		if err := h.svc.DeleteList(listID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}
		audit.Log(h.dbPath(), "family.list.delete", "http", listID, httputil.ClientIP(r))
		json.NewEncoder(w).Encode(map[string]any{"status": "deleted", "listId": listID})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *familyHandler) handleFamilyListItems(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		listID := r.URL.Query().Get("listId")
		if listID == "" {
			http.Error(w, `{"error":"listId query parameter required"}`, http.StatusBadRequest)
			return
		}
		items, err := h.svc.GetListItems(listID)
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
		item, err := h.svc.AddListItem(body.ListID, body.Text, body.Quantity, body.AddedBy)
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
		if err := h.svc.CheckItem(itemID, checked); err != nil {
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
		if err := h.svc.RemoveListItem(itemID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "removed", "itemId": itemID})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}
