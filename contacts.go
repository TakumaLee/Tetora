package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// --- Contacts ---
// Service struct, types, and method implementations are in internal/life/contacts/.
// This file keeps tool handlers and the global singleton.

// globalContactsService is the singleton contacts service.
var globalContactsService *ContactsService

// --- Tool Handlers ---

// toolContactAdd adds a new contact.
func toolContactAdd(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}

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
	c := &Contact{
		ID:           newUUID(),
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

	if err := app.Contacts.AddContact(c); err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"status": "added", "contact": c})
	return string(b), nil
}

// toolContactSearch searches contacts by query.
func toolContactSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}

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

	contacts, err := app.Contacts.SearchContacts(args.Query, args.Limit)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"contacts": contacts, "count": len(contacts)})
	return string(b), nil
}

// toolContactList lists contacts with optional relationship filter.
func toolContactList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}

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

	contacts, err := app.Contacts.ListContacts(args.Relationship, args.Limit)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"contacts": contacts, "count": len(contacts)})
	return string(b), nil
}

// toolContactUpcoming returns upcoming birthdays and anniversaries.
func toolContactUpcoming(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}

	var args struct {
		Days int `json:"days"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Days <= 0 {
		args.Days = 30
	}

	events, err := app.Contacts.GetUpcomingEvents(args.Days)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"events": events, "count": len(events)})
	return string(b), nil
}

// toolContactLog logs an interaction with a contact.
func toolContactLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}

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

	id := newUUID()
	if err := app.Contacts.LogInteraction(id, args.ContactID, args.Channel, args.Type, args.Summary, args.Sentiment); err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"status": "logged", "contact_id": args.ContactID, "type": args.Type})
	return string(b), nil
}
