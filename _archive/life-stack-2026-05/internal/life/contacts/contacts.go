// Package contacts implements the contact and social graph service.
package contacts

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"tetora/internal/life/lifedb"
)

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

// EncryptFn encrypts a string field. Returns value unchanged if encryption is disabled.
type EncryptFn func(value string) string

// DecryptFn decrypts a string field. Returns value unchanged if decryption fails.
type DecryptFn func(value string) string

// Service manages the contact database and social graph.
type Service struct {
	db      lifedb.DB
	dbPath  string
	encrypt EncryptFn
	decrypt DecryptFn
}

// New creates a new contacts Service.
// dbPath is the SQLite database path (contacts.db, separate from history.db).
// encrypt/decrypt are optional field-level encryption helpers (pass nil to disable).
func New(dbPath string, db lifedb.DB, encrypt EncryptFn, decrypt DecryptFn) *Service {
	if encrypt == nil {
		encrypt = func(v string) string { return v }
	}
	if decrypt == nil {
		decrypt = func(v string) string { return v }
	}
	return &Service{
		db:      db,
		dbPath:  dbPath,
		encrypt: encrypt,
		decrypt: decrypt,
	}
}

// DBPath returns the database file path.
func (cs *Service) DBPath() string { return cs.dbPath }

// InitDB creates the contacts database tables.
func InitDB(dbPath string) error {
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

// AddContact inserts a new contact. The contact's fields are pre-populated by the caller.
func (cs *Service) AddContact(c *Contact) error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("contact name is required")
	}

	tagsJSON, _ := json.Marshal(c.Tags)
	if c.Tags == nil {
		tagsJSON = []byte("[]")
	}
	channelJSON, _ := json.Marshal(c.ChannelIDs)
	if c.ChannelIDs == nil {
		channelJSON = []byte("{}")
	}

	email := cs.encrypt(c.Email)
	phone := cs.encrypt(c.Phone)
	notes := cs.encrypt(c.Notes)

	sqlStmt := fmt.Sprintf(
		`INSERT INTO contacts (id, name, nickname, email, phone, birthday, anniversary, notes, tags, channel_ids, relationship, created_at, updated_at) VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s')`,
		cs.db.Escape(c.ID),
		cs.db.Escape(c.Name),
		cs.db.Escape(c.Nickname),
		cs.db.Escape(email),
		cs.db.Escape(phone),
		cs.db.Escape(c.Birthday),
		cs.db.Escape(c.Anniversary),
		cs.db.Escape(notes),
		cs.db.Escape(string(tagsJSON)),
		cs.db.Escape(string(channelJSON)),
		cs.db.Escape(c.Relationship),
		cs.db.Escape(c.CreatedAt),
		cs.db.Escape(c.UpdatedAt),
	)

	return cs.db.Exec(cs.dbPath, sqlStmt)
}

// UpdateContact selectively updates a contact's fields.
func (cs *Service) UpdateContact(id string, fields map[string]any) (*Contact, error) {
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
			sets = append(sets, fmt.Sprintf("name = '%s'", cs.db.Escape(s)))
		case "nickname":
			sets = append(sets, fmt.Sprintf("nickname = '%s'", cs.db.Escape(fmt.Sprintf("%v", val))))
		case "email":
			sets = append(sets, fmt.Sprintf("email = '%s'", cs.db.Escape(fmt.Sprintf("%v", val))))
		case "phone":
			sets = append(sets, fmt.Sprintf("phone = '%s'", cs.db.Escape(fmt.Sprintf("%v", val))))
		case "birthday":
			sets = append(sets, fmt.Sprintf("birthday = '%s'", cs.db.Escape(fmt.Sprintf("%v", val))))
		case "anniversary":
			sets = append(sets, fmt.Sprintf("anniversary = '%s'", cs.db.Escape(fmt.Sprintf("%v", val))))
		case "notes":
			sets = append(sets, fmt.Sprintf("notes = '%s'", cs.db.Escape(fmt.Sprintf("%v", val))))
		case "relationship":
			sets = append(sets, fmt.Sprintf("relationship = '%s'", cs.db.Escape(fmt.Sprintf("%v", val))))
		case "tags":
			tagsJSON, _ := json.Marshal(val)
			sets = append(sets, fmt.Sprintf("tags = '%s'", cs.db.Escape(string(tagsJSON))))
		case "channel_ids":
			chJSON, _ := json.Marshal(val)
			sets = append(sets, fmt.Sprintf("channel_ids = '%s'", cs.db.Escape(string(chJSON))))
		default:
			return nil, fmt.Errorf("unknown field: %s", key)
		}
	}

	if len(sets) == 0 {
		return cs.GetContact(id)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sets = append(sets, fmt.Sprintf("updated_at = '%s'", cs.db.Escape(now)))

	sqlStmt := fmt.Sprintf(
		`UPDATE contacts SET %s WHERE id = '%s'`,
		strings.Join(sets, ", "), cs.db.Escape(id))

	if err := cs.db.Exec(cs.dbPath, sqlStmt); err != nil {
		return nil, fmt.Errorf("update contact: %w", err)
	}

	return cs.GetContact(id)
}

// GetContact retrieves a contact by ID.
func (cs *Service) GetContact(id string) (*Contact, error) {
	if id == "" {
		return nil, fmt.Errorf("contact ID is required")
	}
	sqlStmt := fmt.Sprintf(
		`SELECT id, name, nickname, email, phone, birthday, anniversary, notes, tags, channel_ids, relationship, created_at, updated_at FROM contacts WHERE id = '%s'`,
		cs.db.Escape(id))
	rows, err := cs.db.Query(cs.dbPath, sqlStmt)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("contact not found: %s", id)
	}
	return cs.rowToContact(rows[0]), nil
}

// SearchContacts searches contacts by name, nickname, email, notes, or tags.
func (cs *Service) SearchContacts(query string, limit int) ([]Contact, error) {
	if limit <= 0 {
		limit = 20
	}
	q := cs.db.Escape(query)
	sqlStmt := fmt.Sprintf(
		`SELECT id, name, nickname, email, phone, birthday, anniversary, notes, tags, channel_ids, relationship, created_at, updated_at FROM contacts WHERE name LIKE '%%%s%%' OR nickname LIKE '%%%s%%' OR email LIKE '%%%s%%' OR notes LIKE '%%%s%%' OR tags LIKE '%%%s%%' ORDER BY name LIMIT %d`,
		q, q, q, q, q, limit)
	rows, err := cs.db.Query(cs.dbPath, sqlStmt)
	if err != nil {
		return nil, err
	}
	contacts := make([]Contact, 0, len(rows))
	for _, row := range rows {
		contacts = append(contacts, *cs.rowToContact(row))
	}
	return contacts, nil
}

// ListContacts lists contacts with optional relationship filter.
func (cs *Service) ListContacts(relationship string, limit int) ([]Contact, error) {
	if limit <= 0 {
		limit = 50
	}
	var sqlStmt string
	if relationship != "" {
		sqlStmt = fmt.Sprintf(
			`SELECT id, name, nickname, email, phone, birthday, anniversary, notes, tags, channel_ids, relationship, created_at, updated_at FROM contacts WHERE relationship = '%s' ORDER BY name LIMIT %d`,
			cs.db.Escape(relationship), limit)
	} else {
		sqlStmt = fmt.Sprintf(
			`SELECT id, name, nickname, email, phone, birthday, anniversary, notes, tags, channel_ids, relationship, created_at, updated_at FROM contacts ORDER BY name LIMIT %d`,
			limit)
	}
	rows, err := cs.db.Query(cs.dbPath, sqlStmt)
	if err != nil {
		return nil, err
	}
	contacts := make([]Contact, 0, len(rows))
	for _, row := range rows {
		contacts = append(contacts, *cs.rowToContact(row))
	}
	return contacts, nil
}

// LogInteraction records an interaction with a contact.
func (cs *Service) LogInteraction(id, contactID, channel, interactionType, summary, sentiment string) error {
	if contactID == "" {
		return fmt.Errorf("contact ID is required")
	}
	if interactionType == "" {
		return fmt.Errorf("interaction type is required")
	}

	_, err := cs.GetContact(contactID)
	if err != nil {
		return fmt.Errorf("contact not found: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	sqlStmt := fmt.Sprintf(
		`INSERT INTO contact_interactions (id, contact_id, channel, interaction_type, summary, sentiment, created_at) VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s')`,
		cs.db.Escape(id),
		cs.db.Escape(contactID),
		cs.db.Escape(channel),
		cs.db.Escape(interactionType),
		cs.db.Escape(summary),
		cs.db.Escape(sentiment),
		cs.db.Escape(now),
	)

	return cs.db.Exec(cs.dbPath, sqlStmt)
}

// GetContactInteractions retrieves recent interactions for a contact.
func (cs *Service) GetContactInteractions(contactID string, limit int) ([]ContactInteraction, error) {
	if contactID == "" {
		return nil, fmt.Errorf("contact ID is required")
	}
	if limit <= 0 {
		limit = 20
	}
	sqlStmt := fmt.Sprintf(
		`SELECT id, contact_id, channel, interaction_type, summary, sentiment, created_at FROM contact_interactions WHERE contact_id = '%s' ORDER BY created_at DESC LIMIT %d`,
		cs.db.Escape(contactID), limit)
	rows, err := cs.db.Query(cs.dbPath, sqlStmt)
	if err != nil {
		return nil, err
	}
	interactions := make([]ContactInteraction, 0, len(rows))
	for _, row := range rows {
		interactions = append(interactions, *rowToContactInteraction(row))
	}
	return interactions, nil
}

// GetUpcomingEvents returns birthdays and anniversaries within the next N days.
func (cs *Service) GetUpcomingEvents(days int) ([]map[string]any, error) {
	if days <= 0 {
		days = 30
	}

	sqlStmt := `SELECT id, name, nickname, birthday, anniversary FROM contacts WHERE birthday != '' OR anniversary != ''`
	rows, err := cs.db.Query(cs.dbPath, sqlStmt)
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

// GetInactiveContacts returns contacts with no interaction in the last N days.
func (cs *Service) GetInactiveContacts(days int) ([]Contact, error) {
	if days <= 0 {
		days = 30
	}

	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)

	sqlStmt := fmt.Sprintf(
		`SELECT c.id, c.name, c.nickname, c.email, c.phone, c.birthday, c.anniversary, c.notes, c.tags, c.channel_ids, c.relationship, c.created_at, c.updated_at FROM contacts c LEFT JOIN (SELECT contact_id, MAX(created_at) as last_interaction FROM contact_interactions GROUP BY contact_id) ci ON c.id = ci.contact_id WHERE ci.last_interaction IS NULL OR ci.last_interaction < '%s' ORDER BY c.name`,
		cs.db.Escape(cutoff))

	rows, err := cs.db.Query(cs.dbPath, sqlStmt)
	if err != nil {
		return nil, err
	}
	contacts := make([]Contact, 0, len(rows))
	for _, row := range rows {
		contacts = append(contacts, *cs.rowToContact(row))
	}
	return contacts, nil
}

// --- Row Converters ---

func (cs *Service) rowToContact(row map[string]any) *Contact {
	email := cs.decrypt(jsonStr(row["email"]))
	phone := cs.decrypt(jsonStr(row["phone"]))
	notes := cs.decrypt(jsonStr(row["notes"]))

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

	tagsStr := jsonStr(row["tags"])
	if tagsStr != "" && tagsStr != "[]" {
		var tags []string
		if json.Unmarshal([]byte(tagsStr), &tags) == nil {
			c.Tags = tags
		}
	}

	chStr := jsonStr(row["channel_ids"])
	if chStr != "" && chStr != "{}" {
		var chMap map[string]string
		if json.Unmarshal([]byte(chStr), &chMap) == nil {
			c.ChannelIDs = chMap
		}
	}

	return c
}

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

// --- Helpers ---

// DaysUntilEvent is an exported alias for use in tests.
func DaysUntilEvent(dateStr string, today, endDate time.Time) (int, bool) {
	return daysUntilEvent(dateStr, today, endDate)
}

func daysUntilEvent(dateStr string, today, endDate time.Time) (int, bool) {
	if len(dateStr) < 10 {
		return 0, false
	}
	mmdd := dateStr[5:]
	parts := strings.SplitN(mmdd, "-", 2)
	if len(parts) != 2 {
		return 0, false
	}

	thisYear := today.Year()
	candidate := fmt.Sprintf("%04d-%s-%s", thisYear, parts[0], parts[1])
	t, err := time.Parse("2006-01-02", candidate)
	if err != nil {
		return 0, false
	}

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

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
