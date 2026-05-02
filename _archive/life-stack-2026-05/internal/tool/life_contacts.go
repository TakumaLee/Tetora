package tool

import (
	"encoding/json"
	"fmt"
	"time"

	"tetora/internal/life/contacts"
)

// ContactAdd handles the contact_add tool.
func ContactAdd(svc *contacts.Service, uuidFn func() string, input json.RawMessage) (string, error) {
	var args struct {
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
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	c := &contacts.Contact{
		ID:           uuidFn(),
		Name:         args.Name,
		Nickname:     args.Nickname,
		Email:        args.Email,
		Phone:        args.Phone,
		Birthday:     args.Birthday,
		Anniversary:  args.Anniversary,
		Notes:        args.Notes,
		Tags:         args.Tags,
		ChannelIDs:   args.ChannelIDs,
		Relationship: args.Relationship,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := svc.AddContact(c); err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"status": "added", "contact": c})
	return string(b), nil
}

// ContactSearch handles the contact_search tool.
func ContactSearch(svc *contacts.Service, input json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if args.Limit <= 0 {
		args.Limit = 10
	}

	contacts, err := svc.SearchContacts(args.Query, args.Limit)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"contacts": contacts, "count": len(contacts)})
	return string(b), nil
}

// ContactList handles the contact_list tool.
func ContactList(svc *contacts.Service, input json.RawMessage) (string, error) {
	var args struct {
		Relationship string `json:"relationship"`
		Limit        int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Limit <= 0 {
		args.Limit = 20
	}

	contacts, err := svc.ListContacts(args.Relationship, args.Limit)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"contacts": contacts, "count": len(contacts)})
	return string(b), nil
}

// ContactUpcoming handles the contact_upcoming tool.
func ContactUpcoming(svc *contacts.Service, input json.RawMessage) (string, error) {
	var args struct {
		Days int `json:"days"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Days <= 0 {
		args.Days = 30
	}

	events, err := svc.GetUpcomingEvents(args.Days)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"events": events, "count": len(events)})
	return string(b), nil
}

// ContactLog handles the contact_log tool.
func ContactLog(svc *contacts.Service, uuidFn func() string, input json.RawMessage) (string, error) {
	var args struct {
		ContactID string `json:"contact_id"`
		Type      string `json:"type"`
		Summary   string `json:"summary"`
		Sentiment string `json:"sentiment"`
		Channel   string `json:"channel"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.ContactID == "" {
		return "", fmt.Errorf("contact_id is required")
	}
	if args.Type == "" {
		args.Type = "message"
	}

	id := uuidFn()
	if err := svc.LogInteraction(id, args.ContactID, args.Channel, args.Type, args.Summary, args.Sentiment); err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"status": "logged", "contact_id": args.ContactID, "type": args.Type})
	return string(b), nil
}
