package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestContactsService creates a ContactsService with a temp DB for testing.
func newTestContactsService(t *testing.T) *ContactsService {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "contacts_test.db")
	if err := initContactsDB(dbPath); err != nil {
		t.Fatalf("initContactsDB: %v", err)
	}
	return &ContactsService{
		dbPath: dbPath,
		cfg:    &Config{},
	}
}

func TestInitContactsDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "contacts_init.db")
	if err := initContactsDB(dbPath); err != nil {
		t.Fatalf("initContactsDB failed: %v", err)
	}
	// Calling again should be idempotent.
	if err := initContactsDB(dbPath); err != nil {
		t.Fatalf("initContactsDB idempotent failed: %v", err)
	}

	// Verify tables exist.
	rows, err := queryDB(dbPath, `SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatalf("queryDB: %v", err)
	}
	tableNames := make(map[string]bool)
	for _, row := range rows {
		tableNames[jsonStr(row["name"])] = true
	}
	for _, expected := range []string{"contacts", "contact_interactions"} {
		if !tableNames[expected] {
			t.Errorf("expected table %s to exist", expected)
		}
	}
}

func TestAddContact(t *testing.T) {
	cs := newTestContactsService(t)

	c, err := cs.AddContact("Alice Smith", map[string]any{
		"email":        "alice@example.com",
		"phone":        "+1-555-0100",
		"birthday":     "1990-03-15",
		"relationship": "friend",
		"tags":         []string{"work", "tennis"},
		"channel_ids":  map[string]string{"discord": "12345"},
	})
	if err != nil {
		t.Fatalf("AddContact: %v", err)
	}
	if c.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if c.Name != "Alice Smith" {
		t.Errorf("name = %q, want %q", c.Name, "Alice Smith")
	}
	if c.Email != "alice@example.com" {
		t.Errorf("email = %q, want %q", c.Email, "alice@example.com")
	}
	if c.Birthday != "1990-03-15" {
		t.Errorf("birthday = %q, want %q", c.Birthday, "1990-03-15")
	}
	if c.Relationship != "friend" {
		t.Errorf("relationship = %q, want %q", c.Relationship, "friend")
	}
	if len(c.Tags) != 2 || c.Tags[0] != "work" || c.Tags[1] != "tennis" {
		t.Errorf("tags = %v, want [work tennis]", c.Tags)
	}
	if c.ChannelIDs["discord"] != "12345" {
		t.Errorf("channel_ids = %v, want discord=12345", c.ChannelIDs)
	}

	// Verify it can be retrieved.
	fetched, err := cs.GetContact(c.ID)
	if err != nil {
		t.Fatalf("GetContact: %v", err)
	}
	if fetched.Name != "Alice Smith" {
		t.Errorf("fetched name = %q, want %q", fetched.Name, "Alice Smith")
	}
	if fetched.Email != "alice@example.com" {
		t.Errorf("fetched email = %q, want %q", fetched.Email, "alice@example.com")
	}
	if len(fetched.Tags) != 2 {
		t.Errorf("fetched tags = %v, want 2 items", fetched.Tags)
	}
}

func TestAddContact_EmptyName(t *testing.T) {
	cs := newTestContactsService(t)

	_, err := cs.AddContact("", nil)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("unexpected error: %v", err)
	}

	// Whitespace-only name should also fail.
	_, err = cs.AddContact("   ", nil)
	if err == nil {
		t.Fatal("expected error for whitespace-only name")
	}
}

func TestAddContact_AnyTags(t *testing.T) {
	cs := newTestContactsService(t)

	// Test with []any tags (as would come from JSON unmarshaling).
	c, err := cs.AddContact("Bob", map[string]any{
		"tags": []any{"a", "b"},
	})
	if err != nil {
		t.Fatalf("AddContact: %v", err)
	}
	if len(c.Tags) != 2 {
		t.Errorf("tags = %v, want 2 items", c.Tags)
	}
}

func TestAddContact_AnyChannelIDs(t *testing.T) {
	cs := newTestContactsService(t)

	// Test with map[string]any channel_ids.
	c, err := cs.AddContact("Charlie", map[string]any{
		"channel_ids": map[string]any{"telegram": "99999"},
	})
	if err != nil {
		t.Fatalf("AddContact: %v", err)
	}
	if c.ChannelIDs["telegram"] != "99999" {
		t.Errorf("channel_ids = %v", c.ChannelIDs)
	}
}

func TestUpdateContact(t *testing.T) {
	cs := newTestContactsService(t)

	c, err := cs.AddContact("Alice", map[string]any{
		"email": "alice@old.com",
	})
	if err != nil {
		t.Fatalf("AddContact: %v", err)
	}

	// Update email and add nickname.
	updated, err := cs.UpdateContact(c.ID, map[string]any{
		"email":    "alice@new.com",
		"nickname": "Ali",
	})
	if err != nil {
		t.Fatalf("UpdateContact: %v", err)
	}
	if updated.Email != "alice@new.com" {
		t.Errorf("email = %q, want %q", updated.Email, "alice@new.com")
	}
	if updated.Nickname != "Ali" {
		t.Errorf("nickname = %q, want %q", updated.Nickname, "Ali")
	}

	// Update with empty fields returns contact as-is.
	same, err := cs.UpdateContact(c.ID, map[string]any{})
	if err != nil {
		t.Fatalf("UpdateContact empty: %v", err)
	}
	if same.Email != "alice@new.com" {
		t.Errorf("email = %q, want %q", same.Email, "alice@new.com")
	}

	// Update name to empty should fail.
	_, err = cs.UpdateContact(c.ID, map[string]any{"name": ""})
	if err == nil {
		t.Fatal("expected error for empty name update")
	}

	// Unknown field.
	_, err = cs.UpdateContact(c.ID, map[string]any{"unknown_field": "val"})
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestUpdateContact_Tags(t *testing.T) {
	cs := newTestContactsService(t)

	c, err := cs.AddContact("Dora", map[string]any{
		"tags": []string{"old"},
	})
	if err != nil {
		t.Fatalf("AddContact: %v", err)
	}

	updated, err := cs.UpdateContact(c.ID, map[string]any{
		"tags": []string{"new", "updated"},
	})
	if err != nil {
		t.Fatalf("UpdateContact tags: %v", err)
	}
	if len(updated.Tags) != 2 || updated.Tags[0] != "new" {
		t.Errorf("tags = %v, want [new updated]", updated.Tags)
	}
}

func TestSearchContacts(t *testing.T) {
	cs := newTestContactsService(t)

	cs.AddContact("Alice Smith", map[string]any{"email": "alice@example.com", "notes": "tennis player"})
	cs.AddContact("Bob Jones", map[string]any{"email": "bob@example.com"})
	cs.AddContact("Charlie Smith", map[string]any{"nickname": "Chuck"})

	// Search by name.
	results, err := cs.SearchContacts("Smith", 10)
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("got %d results, want 2", len(results))
	}

	// Search by email.
	results, err = cs.SearchContacts("bob@", 10)
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1", len(results))
	}

	// Search by nickname.
	results, err = cs.SearchContacts("Chuck", 10)
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1", len(results))
	}

	// Search by notes.
	results, err = cs.SearchContacts("tennis", 10)
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results for 'tennis', want 1", len(results))
	}

	// No match.
	results, err = cs.SearchContacts("zzzzz", 10)
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestGetContact(t *testing.T) {
	cs := newTestContactsService(t)

	c, _ := cs.AddContact("Eve", nil)
	fetched, err := cs.GetContact(c.ID)
	if err != nil {
		t.Fatalf("GetContact: %v", err)
	}
	if fetched.Name != "Eve" {
		t.Errorf("name = %q, want %q", fetched.Name, "Eve")
	}
}

func TestGetContact_NotFound(t *testing.T) {
	cs := newTestContactsService(t)

	_, err := cs.GetContact("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}

	// Empty ID.
	_, err = cs.GetContact("")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestListContacts(t *testing.T) {
	cs := newTestContactsService(t)

	cs.AddContact("Alice", map[string]any{"relationship": "friend"})
	cs.AddContact("Bob", map[string]any{"relationship": "colleague"})
	cs.AddContact("Charlie", map[string]any{"relationship": "friend"})

	// List all.
	all, err := cs.ListContacts("", 50)
	if err != nil {
		t.Fatalf("ListContacts: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("got %d contacts, want 3", len(all))
	}
}

func TestListContacts_FilterRelationship(t *testing.T) {
	cs := newTestContactsService(t)

	cs.AddContact("Alice", map[string]any{"relationship": "friend"})
	cs.AddContact("Bob", map[string]any{"relationship": "colleague"})
	cs.AddContact("Charlie", map[string]any{"relationship": "friend"})

	// Filter by relationship.
	friends, err := cs.ListContacts("friend", 50)
	if err != nil {
		t.Fatalf("ListContacts friends: %v", err)
	}
	if len(friends) != 2 {
		t.Errorf("got %d friends, want 2", len(friends))
	}

	colleagues, err := cs.ListContacts("colleague", 50)
	if err != nil {
		t.Fatalf("ListContacts colleagues: %v", err)
	}
	if len(colleagues) != 1 {
		t.Errorf("got %d colleagues, want 1", len(colleagues))
	}

	// Empty filter.
	none, err := cs.ListContacts("acquaintance", 50)
	if err != nil {
		t.Fatalf("ListContacts acquaintance: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("got %d acquaintances, want 0", len(none))
	}
}

func TestLogInteraction(t *testing.T) {
	cs := newTestContactsService(t)

	c, _ := cs.AddContact("Frank", nil)

	err := cs.LogInteraction(c.ID, "discord", "message", "Chatted about project", "positive")
	if err != nil {
		t.Fatalf("LogInteraction: %v", err)
	}

	err = cs.LogInteraction(c.ID, "email", "email", "Sent proposal", "neutral")
	if err != nil {
		t.Fatalf("LogInteraction 2: %v", err)
	}

	// Logging to nonexistent contact should fail.
	err = cs.LogInteraction("nonexistent", "discord", "message", "test", "")
	if err == nil {
		t.Fatal("expected error for nonexistent contact")
	}

	// Missing contact ID.
	err = cs.LogInteraction("", "discord", "message", "test", "")
	if err == nil {
		t.Fatal("expected error for empty contact ID")
	}

	// Missing interaction type.
	err = cs.LogInteraction(c.ID, "discord", "", "test", "")
	if err == nil {
		t.Fatal("expected error for empty interaction type")
	}
}

func TestGetContactInteractions(t *testing.T) {
	cs := newTestContactsService(t)

	c, _ := cs.AddContact("Grace", nil)
	cs.LogInteraction(c.ID, "discord", "message", "hello", "positive")
	cs.LogInteraction(c.ID, "telegram", "call", "video call", "neutral")

	interactions, err := cs.GetContactInteractions(c.ID, 10)
	if err != nil {
		t.Fatalf("GetContactInteractions: %v", err)
	}
	if len(interactions) != 2 {
		t.Errorf("got %d interactions, want 2", len(interactions))
	}
	// Verify both interaction types are present.
	types := make(map[string]bool)
	for _, i := range interactions {
		types[i.InteractionType] = true
	}
	if !types["message"] || !types["call"] {
		t.Errorf("expected message and call types, got %v", types)
	}

	// Empty contact ID.
	_, err = cs.GetContactInteractions("", 10)
	if err == nil {
		t.Fatal("expected error for empty contact ID")
	}
}

func TestGetUpcomingEvents_Birthday(t *testing.T) {
	cs := newTestContactsService(t)

	// Calculate a birthday that is 5 days from now (current year).
	upcoming := time.Now().UTC().Add(5 * 24 * time.Hour).Format("2006-01-02")
	// Use a fixed year (past) so it tests the "this year's occurrence" logic.
	upcomingPastYear := "1990" + upcoming[4:]

	cs.AddContact("Hank", map[string]any{"birthday": upcomingPastYear})

	// Also add someone whose birthday was yesterday (should not show for 7-day window,
	// unless we are within 365 days which it always is).
	yesterday := time.Now().UTC().Add(-1 * 24 * time.Hour).Format("2006-01-02")
	yesterdayPast := "1985" + yesterday[4:]
	cs.AddContact("Ivy", map[string]any{"birthday": yesterdayPast})

	events, err := cs.GetUpcomingEvents(7)
	if err != nil {
		t.Fatalf("GetUpcomingEvents: %v", err)
	}

	// Should find Hank's birthday (5 days away), not Ivy's (yesterday wraps to next year).
	found := false
	for _, ev := range events {
		if ev["contact_name"] == "Hank" && ev["event_type"] == "birthday" {
			found = true
			daysUntil, ok := ev["days_until"].(int)
			if !ok {
				t.Errorf("days_until not int: %v", ev["days_until"])
			}
			if daysUntil < 4 || daysUntil > 6 {
				t.Errorf("days_until = %d, expected ~5", daysUntil)
			}
		}
	}
	if !found {
		t.Errorf("Hank's birthday not found in events: %v", events)
	}
}

func TestGetUpcomingEvents_NoBirthdays(t *testing.T) {
	cs := newTestContactsService(t)

	// Contact with no birthday/anniversary.
	cs.AddContact("Jake", nil)

	events, err := cs.GetUpcomingEvents(30)
	if err != nil {
		t.Fatalf("GetUpcomingEvents: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
}

func TestGetUpcomingEvents_Anniversary(t *testing.T) {
	cs := newTestContactsService(t)

	// Anniversary 10 days from now.
	upcoming := time.Now().UTC().Add(10 * 24 * time.Hour).Format("2006-01-02")
	annivPast := "2015" + upcoming[4:]

	cs.AddContact("Kate", map[string]any{"anniversary": annivPast})

	events, err := cs.GetUpcomingEvents(15)
	if err != nil {
		t.Fatalf("GetUpcomingEvents: %v", err)
	}

	found := false
	for _, ev := range events {
		if ev["contact_name"] == "Kate" && ev["event_type"] == "anniversary" {
			found = true
		}
	}
	if !found {
		t.Errorf("Kate's anniversary not found in events: %v", events)
	}
}

func TestGetInactiveContacts(t *testing.T) {
	cs := newTestContactsService(t)

	c1, _ := cs.AddContact("Larry", nil)
	_, _ = cs.AddContact("Mona", nil)

	// Log a recent interaction for Larry.
	cs.LogInteraction(c1.ID, "discord", "message", "recent chat", "positive")

	// Mona has no interaction, so she should be inactive.
	inactive, err := cs.GetInactiveContacts(7)
	if err != nil {
		t.Fatalf("GetInactiveContacts: %v", err)
	}

	found := false
	for _, c := range inactive {
		if c.Name == "Mona" {
			found = true
		}
		if c.Name == "Larry" {
			t.Error("Larry should NOT be inactive (has recent interaction)")
		}
	}
	if !found {
		t.Error("Mona should be in inactive list")
	}
}

func TestGetInactiveContacts_AllActive(t *testing.T) {
	cs := newTestContactsService(t)

	c1, _ := cs.AddContact("Ned", nil)
	cs.LogInteraction(c1.ID, "discord", "message", "chat", "positive")

	inactive, err := cs.GetInactiveContacts(7)
	if err != nil {
		t.Fatalf("GetInactiveContacts: %v", err)
	}
	if len(inactive) != 0 {
		t.Errorf("got %d inactive, want 0", len(inactive))
	}
}

// --- Tool Handler Tests ---

func TestToolContactAdd(t *testing.T) {
	cs := newTestContactsService(t)
	oldGlobal := globalContactsService
	globalContactsService = cs
	defer func() { globalContactsService = oldGlobal }()

	input := `{"name":"Oscar","email":"oscar@test.com","relationship":"colleague","tags":["dev"]}`
	result, err := toolContactAdd(context.Background(), &Config{}, json.RawMessage(input))
	if err != nil {
		t.Fatalf("toolContactAdd: %v", err)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	if resp["status"] != "added" {
		t.Errorf("status = %v, want added", resp["status"])
	}

	// Verify the contact was saved.
	contacts, _ := cs.ListContacts("", 10)
	if len(contacts) != 1 {
		t.Fatalf("got %d contacts, want 1", len(contacts))
	}
	if contacts[0].Name != "Oscar" {
		t.Errorf("name = %q, want Oscar", contacts[0].Name)
	}
}

func TestToolContactAdd_EmptyName(t *testing.T) {
	cs := newTestContactsService(t)
	oldGlobal := globalContactsService
	globalContactsService = cs
	defer func() { globalContactsService = oldGlobal }()

	input := `{"name":""}`
	_, err := toolContactAdd(context.Background(), &Config{}, json.RawMessage(input))
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestToolContactSearch(t *testing.T) {
	cs := newTestContactsService(t)
	oldGlobal := globalContactsService
	globalContactsService = cs
	defer func() { globalContactsService = oldGlobal }()

	cs.AddContact("Patricia", map[string]any{"email": "pat@test.com"})
	cs.AddContact("Paul", map[string]any{"email": "paul@test.com"})

	input := `{"query":"Pa","limit":10}`
	result, err := toolContactSearch(context.Background(), &Config{}, json.RawMessage(input))
	if err != nil {
		t.Fatalf("toolContactSearch: %v", err)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	count := int(resp["count"].(float64))
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestToolContactSearch_NoQuery(t *testing.T) {
	cs := newTestContactsService(t)
	oldGlobal := globalContactsService
	globalContactsService = cs
	defer func() { globalContactsService = oldGlobal }()

	input := `{"query":""}`
	_, err := toolContactSearch(context.Background(), &Config{}, json.RawMessage(input))
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestToolContactLog(t *testing.T) {
	cs := newTestContactsService(t)
	oldGlobal := globalContactsService
	globalContactsService = cs
	defer func() { globalContactsService = oldGlobal }()

	c, _ := cs.AddContact("Quinn", nil)

	input := `{"contact_id":"` + c.ID + `","type":"message","summary":"had lunch","sentiment":"positive"}`
	result, err := toolContactLog(context.Background(), &Config{}, json.RawMessage(input))
	if err != nil {
		t.Fatalf("toolContactLog: %v", err)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	if resp["status"] != "logged" {
		t.Errorf("status = %v, want logged", resp["status"])
	}

	// Verify interaction was saved.
	interactions, _ := cs.GetContactInteractions(c.ID, 10)
	if len(interactions) != 1 {
		t.Fatalf("got %d interactions, want 1", len(interactions))
	}
	if interactions[0].Summary != "had lunch" {
		t.Errorf("summary = %q, want 'had lunch'", interactions[0].Summary)
	}
}

func TestToolContactLog_NoContactID(t *testing.T) {
	cs := newTestContactsService(t)
	oldGlobal := globalContactsService
	globalContactsService = cs
	defer func() { globalContactsService = oldGlobal }()

	input := `{"contact_id":"","type":"message"}`
	_, err := toolContactLog(context.Background(), &Config{}, json.RawMessage(input))
	if err == nil {
		t.Fatal("expected error for empty contact_id")
	}
}

func TestToolContactList(t *testing.T) {
	cs := newTestContactsService(t)
	oldGlobal := globalContactsService
	globalContactsService = cs
	defer func() { globalContactsService = oldGlobal }()

	cs.AddContact("Ruth", map[string]any{"relationship": "family"})
	cs.AddContact("Sam", map[string]any{"relationship": "friend"})

	input := `{"relationship":"family","limit":10}`
	result, err := toolContactList(context.Background(), &Config{}, json.RawMessage(input))
	if err != nil {
		t.Fatalf("toolContactList: %v", err)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	count := int(resp["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestToolContactUpcoming(t *testing.T) {
	cs := newTestContactsService(t)
	oldGlobal := globalContactsService
	globalContactsService = cs
	defer func() { globalContactsService = oldGlobal }()

	upcoming := time.Now().UTC().Add(3 * 24 * time.Hour).Format("2006-01-02")
	bdayPast := "1992" + upcoming[4:]
	cs.AddContact("Tina", map[string]any{"birthday": bdayPast})

	input := `{"days":7}`
	result, err := toolContactUpcoming(context.Background(), &Config{}, json.RawMessage(input))
	if err != nil {
		t.Fatalf("toolContactUpcoming: %v", err)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	count := int(resp["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestToolContactServiceNil(t *testing.T) {
	oldGlobal := globalContactsService
	globalContactsService = nil
	defer func() { globalContactsService = oldGlobal }()

	_, err := toolContactAdd(context.Background(), &Config{}, json.RawMessage(`{"name":"test"}`))
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
	_, err = toolContactSearch(context.Background(), &Config{}, json.RawMessage(`{"query":"test"}`))
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
	_, err = toolContactList(context.Background(), &Config{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
	_, err = toolContactUpcoming(context.Background(), &Config{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
	_, err = toolContactLog(context.Background(), &Config{}, json.RawMessage(`{"contact_id":"x","type":"message"}`))
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
}

func TestDaysUntilEvent(t *testing.T) {
	today := time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC)
	endDate := today.Add(30 * 24 * time.Hour) // 30 days out

	// Event in 5 days (Feb 28).
	d, ok := daysUntilEvent("1990-02-28", today, endDate)
	if !ok {
		t.Fatal("expected to find event")
	}
	if d != 5 {
		t.Errorf("days_until = %d, want 5", d)
	}

	// Event today (Feb 23).
	d, ok = daysUntilEvent("1990-02-23", today, endDate)
	if !ok {
		t.Fatal("expected to find event for today")
	}
	if d != 0 {
		t.Errorf("days_until = %d, want 0", d)
	}

	// Event far in future (next January) — outside 30 day window.
	_, ok = daysUntilEvent("1990-01-01", today, endDate)
	// Jan 1 is ~312 days away from Feb 23 (next year), so outside 30-day window.
	if ok {
		t.Error("expected NOT to find event outside window")
	}

	// Invalid date.
	_, ok = daysUntilEvent("bad", today, endDate)
	if ok {
		t.Error("expected false for invalid date")
	}
}
