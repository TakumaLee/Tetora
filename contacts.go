package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// --- Types ---

// Contact represents a person in the social graph.
type Contact struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Nickname     string            `json:"nickname,omitempty"`
	Email        string            `json:"email,omitempty"`
	Phone        string            `json:"phone,omitempty"`
	Birthday     string            `json:"birthday,omitempty"`
	Anniversary  string            `json:"anniversary,omitempty"`
	Notes        string            `json:"notes,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
	ChannelIDs   map[string]string `json:"channel_ids,omitempty"`
	Relationship string            `json:"relationship,omitempty"`
	CreatedAt    string            `json:"created_at"`
	UpdatedAt    string            `json:"updated_at"`
}

// ContactInteraction represents a logged interaction with a contact.
type ContactInteraction struct {
	ID              string `json:"id"`
	ContactID       string `json:"contact_id"`
	Channel         string `json:"channel,omitempty"`
	InteractionType string `json:"interaction_type"`
	Summary         string `json:"summary,omitempty"`
	Sentiment       string `json:"sentiment,omitempty"`
	CreatedAt       string `json:"created_at"`
}

// --- Global singleton ---

var globalContactsService *ContactsService

// --- ContactsService ---

// ContactsService manages the contact database and social graph.
type ContactsService struct {
	dbPath string
	cfg    *Config
}

// newContactsService creates and initializes a ContactsService.
func newContactsService(cfg *Config) *ContactsService {
	dbPath := filepath.Join(filepath.Dir(cfg.HistoryDB), "contacts.db")
	if err := initContactsDB(dbPath); err != nil {
		logError("contacts service init failed", "error", err)
		return nil
	}
	logInfo("contacts service initialized", "db", dbPath)
	return &ContactsService{
		dbPath: dbPath,
		cfg:    cfg,
	}
}

// --- DB Init ---

// initContactsDB creates the contacts database tables.
func initContactsDB(dbPath string) error {
	sql := `
CREATE TABLE IF NOT EXISTS contacts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    nickname TEXT DEFAULT '',
    email TEXT DEFAULT '',
    phone TEXT DEFAULT '',
    birthday TEXT DEFAULT '',
    anniversary TEXT DEFAULT '',
    notes TEXT DEFAULT '',
    tags TEXT DEFAULT '[]',
    channel_ids TEXT DEFAULT '{}',
    relationship TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS contact_interactions (
    id TEXT PRIMARY KEY,
    contact_id TEXT NOT NULL,
    channel TEXT DEFAULT '',
    interaction_type TEXT NOT NULL,
    summary TEXT DEFAULT '',
    sentiment TEXT DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ci_contact ON contact_interactions(contact_id);
CREATE INDEX IF NOT EXISTS idx_ci_created ON contact_interactions(created_at);
`
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3 init: %s: %w", string(out), err)
	}
	return nil
}

// contactsExecSQL runs a non-query SQL statement against the contacts DB.
func contactsExecSQL(dbPath, sql string) error {
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3: %s: %w", string(out), err)
	}
	return nil
}

// --- Contact CRUD ---

// AddContact creates a new contact.
func (cs *ContactsService) AddContact(name string, fields map[string]any) (*Contact, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("contact name is required")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	c := &Contact{
		ID:        newUUID(),
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Apply optional fields.
	if v, ok := fields["nickname"].(string); ok {
		c.Nickname = v
	}
	if v, ok := fields["email"].(string); ok {
		c.Email = v
	}
	if v, ok := fields["phone"].(string); ok {
		c.Phone = v
	}
	if v, ok := fields["birthday"].(string); ok {
		c.Birthday = v
	}
	if v, ok := fields["anniversary"].(string); ok {
		c.Anniversary = v
	}
	if v, ok := fields["notes"].(string); ok {
		c.Notes = v
	}
	if v, ok := fields["relationship"].(string); ok {
		c.Relationship = v
	}
	if v, ok := fields["tags"].([]string); ok {
		c.Tags = v
	} else if v, ok := fields["tags"].([]any); ok {
		for _, t := range v {
			if s, ok := t.(string); ok {
				c.Tags = append(c.Tags, s)
			}
		}
	}
	if v, ok := fields["channel_ids"].(map[string]string); ok {
		c.ChannelIDs = v
	} else if v, ok := fields["channel_ids"].(map[string]any); ok {
		c.ChannelIDs = make(map[string]string)
		for k, val := range v {
			c.ChannelIDs[k] = fmt.Sprintf("%v", val)
		}
	}

	tagsJSON, _ := json.Marshal(c.Tags)
	if c.Tags == nil {
		tagsJSON = []byte("[]")
	}
	channelJSON, _ := json.Marshal(c.ChannelIDs)
	if c.ChannelIDs == nil {
		channelJSON = []byte("{}")
	}

	// P27.2: Encrypt PII fields.
	email := encryptField(cs.cfg, c.Email)
	phone := encryptField(cs.cfg, c.Phone)
	notes := encryptField(cs.cfg, c.Notes)

	sqlStmt := fmt.Sprintf(
		`INSERT INTO contacts (id, name, nickname, email, phone, birthday, anniversary, notes, tags, channel_ids, relationship, created_at, updated_at) VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s')`,
		escapeSQLite(c.ID),
		escapeSQLite(c.Name),
		escapeSQLite(c.Nickname),
		escapeSQLite(email),
		escapeSQLite(phone),
		escapeSQLite(c.Birthday),
		escapeSQLite(c.Anniversary),
		escapeSQLite(notes),
		escapeSQLite(string(tagsJSON)),
		escapeSQLite(string(channelJSON)),
		escapeSQLite(c.Relationship),
		escapeSQLite(c.CreatedAt),
		escapeSQLite(c.UpdatedAt),
	)

	if err := contactsExecSQL(cs.dbPath, sqlStmt); err != nil {
		return nil, fmt.Errorf("insert contact: %w", err)
	}

	logInfo("contact added", "id", c.ID, "name", c.Name)
	return c, nil
}

// UpdateContact selectively updates a contact's fields.
func (cs *ContactsService) UpdateContact(id string, fields map[string]any) (*Contact, error) {
	if id == "" {
		return nil, fmt.Errorf("contact ID is required")
	}
	if len(fields) == 0 {
		return cs.GetContact(id)
	}

	var sets []string
	for key, val := range fields {
		switch key {
		case "name":
			s := fmt.Sprintf("%v", val)
			if strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("contact name cannot be empty")
			}
			sets = append(sets, fmt.Sprintf("name = '%s'", escapeSQLite(s)))
		case "nickname":
			sets = append(sets, fmt.Sprintf("nickname = '%s'", escapeSQLite(fmt.Sprintf("%v", val))))
		case "email":
			sets = append(sets, fmt.Sprintf("email = '%s'", escapeSQLite(fmt.Sprintf("%v", val))))
		case "phone":
			sets = append(sets, fmt.Sprintf("phone = '%s'", escapeSQLite(fmt.Sprintf("%v", val))))
		case "birthday":
			sets = append(sets, fmt.Sprintf("birthday = '%s'", escapeSQLite(fmt.Sprintf("%v", val))))
		case "anniversary":
			sets = append(sets, fmt.Sprintf("anniversary = '%s'", escapeSQLite(fmt.Sprintf("%v", val))))
		case "notes":
			sets = append(sets, fmt.Sprintf("notes = '%s'", escapeSQLite(fmt.Sprintf("%v", val))))
		case "relationship":
			sets = append(sets, fmt.Sprintf("relationship = '%s'", escapeSQLite(fmt.Sprintf("%v", val))))
		case "tags":
			tagsJSON, _ := json.Marshal(val)
			sets = append(sets, fmt.Sprintf("tags = '%s'", escapeSQLite(string(tagsJSON))))
		case "channel_ids":
			chJSON, _ := json.Marshal(val)
			sets = append(sets, fmt.Sprintf("channel_ids = '%s'", escapeSQLite(string(chJSON))))
		default:
			return nil, fmt.Errorf("unknown field: %s", key)
		}
	}

	if len(sets) == 0 {
		return cs.GetContact(id)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sets = append(sets, fmt.Sprintf("updated_at = '%s'", escapeSQLite(now)))

	sqlStmt := fmt.Sprintf(
		`UPDATE contacts SET %s WHERE id = '%s'`,
		strings.Join(sets, ", "), escapeSQLite(id))

	if err := contactsExecSQL(cs.dbPath, sqlStmt); err != nil {
		return nil, fmt.Errorf("update contact: %w", err)
	}

	return cs.GetContact(id)
}

// GetContact retrieves a contact by ID.
func (cs *ContactsService) GetContact(id string) (*Contact, error) {
	if id == "" {
		return nil, fmt.Errorf("contact ID is required")
	}
	sqlStmt := fmt.Sprintf(
		`SELECT id, name, nickname, email, phone, birthday, anniversary, notes, tags, channel_ids, relationship, created_at, updated_at FROM contacts WHERE id = '%s'`,
		escapeSQLite(id))
	rows, err := queryDB(cs.dbPath, sqlStmt)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("contact not found: %s", id)
	}
	return rowToContact(rows[0]), nil
}

// SearchContacts searches contacts by name, nickname, email, notes, or tags.
func (cs *ContactsService) SearchContacts(query string, limit int) ([]Contact, error) {
	if limit <= 0 {
		limit = 20
	}
	q := escapeSQLite(query)
	sqlStmt := fmt.Sprintf(
		`SELECT id, name, nickname, email, phone, birthday, anniversary, notes, tags, channel_ids, relationship, created_at, updated_at FROM contacts WHERE name LIKE '%%%s%%' OR nickname LIKE '%%%s%%' OR email LIKE '%%%s%%' OR notes LIKE '%%%s%%' OR tags LIKE '%%%s%%' ORDER BY name LIMIT %d`,
		q, q, q, q, q, limit)
	rows, err := queryDB(cs.dbPath, sqlStmt)
	if err != nil {
		return nil, err
	}
	contacts := make([]Contact, 0, len(rows))
	for _, row := range rows {
		contacts = append(contacts, *rowToContact(row))
	}
	return contacts, nil
}

// ListContacts lists contacts with optional relationship filter.
func (cs *ContactsService) ListContacts(relationship string, limit int) ([]Contact, error) {
	if limit <= 0 {
		limit = 50
	}
	var sqlStmt string
	if relationship != "" {
		sqlStmt = fmt.Sprintf(
			`SELECT id, name, nickname, email, phone, birthday, anniversary, notes, tags, channel_ids, relationship, created_at, updated_at FROM contacts WHERE relationship = '%s' ORDER BY name LIMIT %d`,
			escapeSQLite(relationship), limit)
	} else {
		sqlStmt = fmt.Sprintf(
			`SELECT id, name, nickname, email, phone, birthday, anniversary, notes, tags, channel_ids, relationship, created_at, updated_at FROM contacts ORDER BY name LIMIT %d`,
			limit)
	}
	rows, err := queryDB(cs.dbPath, sqlStmt)
	if err != nil {
		return nil, err
	}
	contacts := make([]Contact, 0, len(rows))
	for _, row := range rows {
		contacts = append(contacts, *rowToContact(row))
	}
	return contacts, nil
}

// --- Interaction Logging ---

// LogInteraction records an interaction with a contact.
func (cs *ContactsService) LogInteraction(contactID, channel, interactionType, summary, sentiment string) error {
	if contactID == "" {
		return fmt.Errorf("contact ID is required")
	}
	if interactionType == "" {
		return fmt.Errorf("interaction type is required")
	}

	// Validate contact exists.
	_, err := cs.GetContact(contactID)
	if err != nil {
		return fmt.Errorf("contact not found: %w", err)
	}

	id := newUUID()
	now := time.Now().UTC().Format(time.RFC3339)

	sqlStmt := fmt.Sprintf(
		`INSERT INTO contact_interactions (id, contact_id, channel, interaction_type, summary, sentiment, created_at) VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s')`,
		escapeSQLite(id),
		escapeSQLite(contactID),
		escapeSQLite(channel),
		escapeSQLite(interactionType),
		escapeSQLite(summary),
		escapeSQLite(sentiment),
		escapeSQLite(now),
	)

	if err := contactsExecSQL(cs.dbPath, sqlStmt); err != nil {
		return fmt.Errorf("log interaction: %w", err)
	}

	logDebug("contact interaction logged", "contact_id", contactID, "type", interactionType)
	return nil
}

// GetContactInteractions retrieves recent interactions for a contact.
func (cs *ContactsService) GetContactInteractions(contactID string, limit int) ([]ContactInteraction, error) {
	if contactID == "" {
		return nil, fmt.Errorf("contact ID is required")
	}
	if limit <= 0 {
		limit = 20
	}
	sqlStmt := fmt.Sprintf(
		`SELECT id, contact_id, channel, interaction_type, summary, sentiment, created_at FROM contact_interactions WHERE contact_id = '%s' ORDER BY created_at DESC LIMIT %d`,
		escapeSQLite(contactID), limit)
	rows, err := queryDB(cs.dbPath, sqlStmt)
	if err != nil {
		return nil, err
	}
	interactions := make([]ContactInteraction, 0, len(rows))
	for _, row := range rows {
		interactions = append(interactions, *rowToContactInteraction(row))
	}
	return interactions, nil
}

// --- Upcoming Events ---

// GetUpcomingEvents returns birthdays and anniversaries within the next N days.
func (cs *ContactsService) GetUpcomingEvents(days int) ([]map[string]any, error) {
	if days <= 0 {
		days = 30
	}

	// Get all contacts with a birthday or anniversary set.
	sqlStmt := `SELECT id, name, nickname, birthday, anniversary FROM contacts WHERE birthday != '' OR anniversary != ''`
	rows, err := queryDB(cs.dbPath, sqlStmt)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	endDate := today.Add(time.Duration(days) * 24 * time.Hour)

	var events []map[string]any
	for _, row := range rows {
		contactID := jsonStr(row["id"])
		contactName := jsonStr(row["name"])
		nickname := jsonStr(row["nickname"])

		displayName := contactName
		if nickname != "" {
			displayName = contactName + " (" + nickname + ")"
		}

		bday := jsonStr(row["birthday"])
		if bday != "" {
			if daysUntil, ok := daysUntilEvent(bday, today, endDate); ok {
				events = append(events, map[string]any{
					"contact_id":   contactID,
					"contact_name": displayName,
					"event_type":   "birthday",
					"date":         bday,
					"days_until":   daysUntil,
				})
			}
		}

		anniv := jsonStr(row["anniversary"])
		if anniv != "" {
			if daysUntil, ok := daysUntilEvent(anniv, today, endDate); ok {
				events = append(events, map[string]any{
					"contact_id":   contactID,
					"contact_name": displayName,
					"event_type":   "anniversary",
					"date":         anniv,
					"days_until":   daysUntil,
				})
			}
		}
	}

	return events, nil
}

// daysUntilEvent checks if a YYYY-MM-DD date's annual recurrence falls within [today, endDate).
// Returns (daysUntil, true) if it does.
func daysUntilEvent(dateStr string, today, endDate time.Time) (int, bool) {
	// Parse MM-DD from YYYY-MM-DD.
	if len(dateStr) < 10 {
		return 0, false
	}
	mmdd := dateStr[5:] // "MM-DD"
	parts := strings.SplitN(mmdd, "-", 2)
	if len(parts) != 2 {
		return 0, false
	}

	// Build this year's occurrence.
	thisYear := today.Year()
	candidate := fmt.Sprintf("%04d-%s-%s", thisYear, parts[0], parts[1])
	t, err := time.Parse("2006-01-02", candidate)
	if err != nil {
		return 0, false
	}

	// If the date has already passed this year, check next year.
	if t.Before(today) {
		candidate = fmt.Sprintf("%04d-%s-%s", thisYear+1, parts[0], parts[1])
		t, err = time.Parse("2006-01-02", candidate)
		if err != nil {
			return 0, false
		}
	}

	if t.Before(endDate) {
		d := int(t.Sub(today).Hours() / 24)
		return d, true
	}
	return 0, false
}

// --- Inactive Contacts ---

// GetInactiveContacts returns contacts with no interaction in the last N days.
func (cs *ContactsService) GetInactiveContacts(days int) ([]Contact, error) {
	if days <= 0 {
		days = 30
	}

	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)

	// Contacts with no interaction at all, or whose latest interaction is older than cutoff.
	sqlStmt := fmt.Sprintf(
		`SELECT c.id, c.name, c.nickname, c.email, c.phone, c.birthday, c.anniversary, c.notes, c.tags, c.channel_ids, c.relationship, c.created_at, c.updated_at FROM contacts c LEFT JOIN (SELECT contact_id, MAX(created_at) as last_interaction FROM contact_interactions GROUP BY contact_id) ci ON c.id = ci.contact_id WHERE ci.last_interaction IS NULL OR ci.last_interaction < '%s' ORDER BY c.name`,
		escapeSQLite(cutoff))

	rows, err := queryDB(cs.dbPath, sqlStmt)
	if err != nil {
		return nil, err
	}
	contacts := make([]Contact, 0, len(rows))
	for _, row := range rows {
		contacts = append(contacts, *rowToContact(row))
	}
	return contacts, nil
}

// --- Row Converters ---

// rowToContact converts a DB row to a Contact.
func rowToContact(row map[string]any) *Contact {
	email := jsonStr(row["email"])
	phone := jsonStr(row["phone"])
	notes := jsonStr(row["notes"])
	// P27.2: Decrypt PII fields.
	if k := globalEncryptionKey(); k != "" {
		if d, err := decrypt(email, k); err == nil {
			email = d
		}
		if d, err := decrypt(phone, k); err == nil {
			phone = d
		}
		if d, err := decrypt(notes, k); err == nil {
			notes = d
		}
	}
	c := &Contact{
		ID:           jsonStr(row["id"]),
		Name:         jsonStr(row["name"]),
		Nickname:     jsonStr(row["nickname"]),
		Email:        email,
		Phone:        phone,
		Birthday:     jsonStr(row["birthday"]),
		Anniversary:  jsonStr(row["anniversary"]),
		Notes:        notes,
		Relationship: jsonStr(row["relationship"]),
		CreatedAt:    jsonStr(row["created_at"]),
		UpdatedAt:    jsonStr(row["updated_at"]),
	}

	// Parse tags JSON.
	tagsStr := jsonStr(row["tags"])
	if tagsStr != "" && tagsStr != "[]" {
		var tags []string
		if json.Unmarshal([]byte(tagsStr), &tags) == nil {
			c.Tags = tags
		}
	}

	// Parse channel_ids JSON.
	chStr := jsonStr(row["channel_ids"])
	if chStr != "" && chStr != "{}" {
		var chMap map[string]string
		if json.Unmarshal([]byte(chStr), &chMap) == nil {
			c.ChannelIDs = chMap
		}
	}

	return c
}

// rowToContactInteraction converts a DB row to a ContactInteraction.
func rowToContactInteraction(row map[string]any) *ContactInteraction {
	return &ContactInteraction{
		ID:              jsonStr(row["id"]),
		ContactID:       jsonStr(row["contact_id"]),
		Channel:         jsonStr(row["channel"]),
		InteractionType: jsonStr(row["interaction_type"]),
		Summary:         jsonStr(row["summary"]),
		Sentiment:       jsonStr(row["sentiment"]),
		CreatedAt:       jsonStr(row["created_at"]),
	}
}

// --- Tool Handlers ---

// toolContactAdd adds a new contact.
// Input: {"name": "...", "email": "...", "phone": "...", "birthday": "...", "relationship": "...", ...}
func toolContactAdd(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalContactsService == nil {
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

	fields := make(map[string]any)
	if args.Nickname != "" {
		fields["nickname"] = args.Nickname
	}
	if args.Email != "" {
		fields["email"] = args.Email
	}
	if args.Phone != "" {
		fields["phone"] = args.Phone
	}
	if args.Birthday != "" {
		fields["birthday"] = args.Birthday
	}
	if args.Anniversary != "" {
		fields["anniversary"] = args.Anniversary
	}
	if args.Notes != "" {
		fields["notes"] = args.Notes
	}
	if args.Relationship != "" {
		fields["relationship"] = args.Relationship
	}
	if len(args.Tags) > 0 {
		fields["tags"] = args.Tags
	}
	if len(args.ChannelIDs) > 0 {
		fields["channel_ids"] = args.ChannelIDs
	}

	contact, err := globalContactsService.AddContact(args.Name, fields)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"status": "added", "contact": contact})
	return string(b), nil
}

// toolContactSearch searches contacts by query.
// Input: {"query": "...", "limit": 10}
func toolContactSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalContactsService == nil {
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

	contacts, err := globalContactsService.SearchContacts(args.Query, args.Limit)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"contacts": contacts, "count": len(contacts)})
	return string(b), nil
}

// toolContactList lists contacts with optional relationship filter.
// Input: {"relationship": "friend", "limit": 20}
func toolContactList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalContactsService == nil {
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

	contacts, err := globalContactsService.ListContacts(args.Relationship, args.Limit)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"contacts": contacts, "count": len(contacts)})
	return string(b), nil
}

// toolContactUpcoming returns upcoming birthdays and anniversaries.
// Input: {"days": 30}
func toolContactUpcoming(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalContactsService == nil {
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

	events, err := globalContactsService.GetUpcomingEvents(args.Days)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"events": events, "count": len(events)})
	return string(b), nil
}

// toolContactLog logs an interaction with a contact.
// Input: {"contact_id": "...", "type": "message", "summary": "...", "sentiment": "positive", "channel": "discord"}
func toolContactLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalContactsService == nil {
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

	if err := globalContactsService.LogInteraction(args.ContactID, args.Channel, args.Type, args.Summary, args.Sentiment); err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{"status": "logged", "contact_id": args.ContactID, "type": args.Type})
	return string(b), nil
}
