package main

import (
	"context"
	"encoding/json"
	"fmt"

	"tetora/internal/tool"
)

// --- Contacts ---
// Service struct, types, and method implementations are in internal/life/contacts/.
// Tool handler logic is in internal/tool/life_contacts.go.
// This file keeps adapter closures and the global singleton.

// globalContactsService is the singleton contacts service.
var globalContactsService *ContactsService

// --- Tool Handlers (adapter closures) ---

func toolContactAdd(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactAdd(app.Contacts, newUUID, input)
}

func toolContactSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactSearch(app.Contacts, input)
}

func toolContactList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactList(app.Contacts, input)
}

func toolContactUpcoming(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactUpcoming(app.Contacts, input)
}

func toolContactLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactLog(app.Contacts, newUUID, input)
}
