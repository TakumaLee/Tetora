// Package profile implements user profile, channel identity resolution,
// preference learning, and mood tracking.
package profile

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"tetora/internal/life/lifedb"
)

// --- Types ---

// UserProfile represents a cross-channel user identity.
type UserProfile struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	PreferredLanguage string `json:"preferredLanguage"`
	Timezone          string `json:"timezone"`
	PersonalityNotes  string `json:"personalityNotes"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

// ChannelIdentity maps a channel-specific key to a unified user ID.
type ChannelIdentity struct {
	ChannelKey         string `json:"channelKey"`         // "tg:12345", "discord:67890"
	UserID             string `json:"userId"`
	ChannelDisplayName string `json:"channelDisplayName"`
	LastSeen           string `json:"lastSeen"`
	MessageCount       int    `json:"messageCount"`
}

// UserPreference represents a learned preference about a user.
type UserPreference struct {
	ID            int     `json:"id"`
	UserID        string  `json:"userId"`
	Category      string  `json:"category"`      // "food","schedule","communication"
	Key           string  `json:"key"`
	Value         string  `json:"value"`
	Confidence    float64 `json:"confidence"`     // 0-1
	ObservedCount int     `json:"observedCount"`
	FirstObserved string  `json:"firstObserved"`
	LastObserved  string  `json:"lastObserved"`
}

// Config controls user profiling and emotional memory.
type Config struct {
	Enabled          bool `json:"enabled"`
	SentimentEnabled bool `json:"sentimentEnabled"`
}

// SentimentFn analyses text and returns (score, keywords).
type SentimentFn func(text string) (score float64, keywords []string)

// SentimentLabelFn returns a human-readable label for a sentiment score.
type SentimentLabelFn func(score float64) string

// UUIDFn generates a new UUID string.
type UUIDFn func() string

// Service manages user profiles, channel identities, preferences, and mood.
type Service struct {
	mu       sync.RWMutex
	db       lifedb.DB
	dbPath   string
	cfg      Config
	uuidFn   UUIDFn
	sentimentFn    SentimentFn
	sentimentLabel SentimentLabelFn
}

// New creates a new profile Service.
func New(dbPath string, cfg Config, db lifedb.DB, uuidFn UUIDFn, sentimentFn SentimentFn, sentimentLabel SentimentLabelFn) *Service {
	return &Service{
		db:             db,
		dbPath:         dbPath,
		cfg:            cfg,
		uuidFn:         uuidFn,
		sentimentFn:    sentimentFn,
		sentimentLabel: sentimentLabel,
	}
}

// InitDB creates the user profile tables.
func InitDB(dbPath string) error {
	schema := `
CREATE TABLE IF NOT EXISTS user_profiles (
    id TEXT PRIMARY KEY,
    display_name TEXT DEFAULT '',
    preferred_language TEXT DEFAULT '',
    timezone TEXT DEFAULT '',
    personality_notes TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS channel_user_map (
    channel_key TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    channel_display_name TEXT DEFAULT '',
    last_seen TEXT DEFAULT '',
    message_count INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_cum_user ON channel_user_map(user_id);

CREATE TABLE IF NOT EXISTS user_preferences (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    category TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    confidence REAL DEFAULT 0.5,
    observed_count INTEGER DEFAULT 1,
    first_observed TEXT NOT NULL,
    last_observed TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_pref ON user_preferences(user_id, category, key);

CREATE TABLE IF NOT EXISTS user_mood_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    channel TEXT NOT NULL,
    sentiment_score REAL NOT NULL,
    keywords TEXT DEFAULT '',
    message_snippet TEXT DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mood_user ON user_mood_log(user_id, created_at);
`
	cmd := exec.Command("sqlite3", dbPath, schema)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("init user profile db: %w: %s", err, string(out))
	}
	return nil
}

// --- Profile CRUD ---

// GetProfile retrieves a user profile by ID.
func (svc *Service) GetProfile(userID string) (*UserProfile, error) {
	svc.mu.RLock()
	defer svc.mu.RUnlock()

	rows, err := svc.db.Query(svc.dbPath, fmt.Sprintf(
		`SELECT id, display_name, preferred_language, timezone, personality_notes, created_at, updated_at FROM user_profiles WHERE id = '%s' LIMIT 1`,
		svc.db.Escape(userID)))
	if err != nil {
		return nil, fmt.Errorf("get profile: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}

	p := profileFromRow(rows[0])
	return &p, nil
}

// CreateProfile creates a new user profile.
func (svc *Service) CreateProfile(profile UserProfile) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	if profile.ID == "" {
		profile.ID = svc.uuidFn()
	}
	if profile.CreatedAt == "" {
		profile.CreatedAt = now
	}
	if profile.UpdatedAt == "" {
		profile.UpdatedAt = now
	}

	sql := fmt.Sprintf(
		`INSERT OR IGNORE INTO user_profiles (id, display_name, preferred_language, timezone, personality_notes, created_at, updated_at) VALUES ('%s','%s','%s','%s','%s','%s','%s')`,
		svc.db.Escape(profile.ID),
		svc.db.Escape(profile.DisplayName),
		svc.db.Escape(profile.PreferredLanguage),
		svc.db.Escape(profile.Timezone),
		svc.db.Escape(profile.PersonalityNotes),
		svc.db.Escape(profile.CreatedAt),
		svc.db.Escape(profile.UpdatedAt),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create profile: %w: %s", err, string(out))
	}
	return nil
}

// UpdateProfile updates specific fields of a user profile.
func (svc *Service) UpdateProfile(userID string, updates map[string]string) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	if len(updates) == 0 {
		return nil
	}

	var setParts []string
	allowedFields := map[string]string{
		"displayName":       "display_name",
		"preferredLanguage": "preferred_language",
		"timezone":          "timezone",
		"personalityNotes":  "personality_notes",
	}

	for k, v := range updates {
		col, ok := allowedFields[k]
		if !ok {
			continue
		}
		setParts = append(setParts, fmt.Sprintf("%s = '%s'", col, svc.db.Escape(v)))
	}
	if len(setParts) == 0 {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	setParts = append(setParts, fmt.Sprintf("updated_at = '%s'", now))

	sql := fmt.Sprintf("UPDATE user_profiles SET %s WHERE id = '%s'",
		strings.Join(setParts, ", "), svc.db.Escape(userID))
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("update profile: %w: %s", err, string(out))
	}
	return nil
}

// --- Channel Identity Resolution ---

// ResolveUser resolves a channel key to a user ID, creating a new user if needed.
func (svc *Service) ResolveUser(channelKey string) (string, error) {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	rows, err := svc.db.Query(svc.dbPath, fmt.Sprintf(
		`SELECT user_id FROM channel_user_map WHERE channel_key = '%s' LIMIT 1`,
		svc.db.Escape(channelKey)))
	if err != nil {
		return "", fmt.Errorf("resolve user: %w", err)
	}
	if len(rows) > 0 {
		return jsonStr(rows[0]["user_id"]), nil
	}

	userID := svc.uuidFn()
	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT OR IGNORE INTO user_profiles (id, display_name, created_at, updated_at) VALUES ('%s','','%s','%s');
INSERT OR IGNORE INTO channel_user_map (channel_key, user_id, last_seen, message_count) VALUES ('%s','%s','%s',0)`,
		svc.db.Escape(userID), now, now,
		svc.db.Escape(channelKey), svc.db.Escape(userID), now)

	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("create user for channel: %w: %s", err, string(out))
	}

	return userID, nil
}

// LinkChannel links a channel key to an existing user.
func (svc *Service) LinkChannel(userID, channelKey, displayName string) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO channel_user_map (channel_key, user_id, channel_display_name, last_seen, message_count) VALUES ('%s','%s','%s','%s',0)
ON CONFLICT(channel_key) DO UPDATE SET user_id='%s', channel_display_name='%s', last_seen='%s'`,
		svc.db.Escape(channelKey), svc.db.Escape(userID), svc.db.Escape(displayName), now,
		svc.db.Escape(userID), svc.db.Escape(displayName), now)

	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("link channel: %w: %s", err, string(out))
	}
	return nil
}

// GetChannelIdentities returns all channel identities for a user.
func (svc *Service) GetChannelIdentities(userID string) ([]ChannelIdentity, error) {
	svc.mu.RLock()
	defer svc.mu.RUnlock()

	rows, err := svc.db.Query(svc.dbPath, fmt.Sprintf(
		`SELECT channel_key, user_id, channel_display_name, last_seen, message_count FROM channel_user_map WHERE user_id = '%s' ORDER BY last_seen DESC`,
		svc.db.Escape(userID)))
	if err != nil {
		return nil, fmt.Errorf("get channel identities: %w", err)
	}

	var results []ChannelIdentity
	for _, row := range rows {
		results = append(results, ChannelIdentity{
			ChannelKey:         jsonStr(row["channel_key"]),
			UserID:             jsonStr(row["user_id"]),
			ChannelDisplayName: jsonStr(row["channel_display_name"]),
			LastSeen:           jsonStr(row["last_seen"]),
			MessageCount:       jsonInt(row["message_count"]),
		})
	}
	return results, nil
}

// --- Preference Learning ---

// ObservePreference records or updates a user preference.
func (svc *Service) ObservePreference(userID, category, key, value string) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO user_preferences (user_id, category, key, value, confidence, observed_count, first_observed, last_observed)
VALUES ('%s','%s','%s','%s', 0.5, 1, '%s', '%s')
ON CONFLICT(user_id, category, key) DO UPDATE SET
    value = '%s',
    observed_count = observed_count + 1,
    last_observed = '%s',
    confidence = MIN(1.0, 0.5 + (observed_count + 1) * 0.1)`,
		svc.db.Escape(userID), svc.db.Escape(category), svc.db.Escape(key), svc.db.Escape(value),
		now, now,
		svc.db.Escape(value), now)

	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("observe preference: %w: %s", err, string(out))
	}
	return nil
}

// GetPreferences returns preferences for a user, optionally filtered by category.
func (svc *Service) GetPreferences(userID string, category string) ([]UserPreference, error) {
	svc.mu.RLock()
	defer svc.mu.RUnlock()

	sql := fmt.Sprintf(
		`SELECT id, user_id, category, key, value, confidence, observed_count, first_observed, last_observed FROM user_preferences WHERE user_id = '%s'`,
		svc.db.Escape(userID))
	if category != "" {
		sql += fmt.Sprintf(` AND category = '%s'`, svc.db.Escape(category))
	}
	sql += ` ORDER BY confidence DESC, last_observed DESC`

	rows, err := svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("get preferences: %w", err)
	}

	var results []UserPreference
	for _, row := range rows {
		results = append(results, UserPreference{
			ID:            jsonInt(row["id"]),
			UserID:        jsonStr(row["user_id"]),
			Category:      jsonStr(row["category"]),
			Key:           jsonStr(row["key"]),
			Value:         jsonStr(row["value"]),
			Confidence:    jsonFloat(row["confidence"]),
			ObservedCount: jsonInt(row["observed_count"]),
			FirstObserved: jsonStr(row["first_observed"]),
			LastObserved:  jsonStr(row["last_observed"]),
		})
	}
	return results, nil
}

// --- Message Recording ---

// RecordMessage records a user message, updates channel stats, and optionally runs sentiment analysis.
func (svc *Service) RecordMessage(channelKey, displayName, message string) error {
	userID, err := svc.ResolveUser(channelKey)
	if err != nil {
		return fmt.Errorf("record message: resolve user: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	updateSQL := fmt.Sprintf(
		`UPDATE channel_user_map SET last_seen = '%s', message_count = message_count + 1, channel_display_name = '%s' WHERE channel_key = '%s'`,
		now, svc.db.Escape(displayName), svc.db.Escape(channelKey))

	cmd := exec.Command("sqlite3", svc.dbPath, updateSQL)
	if out, err := cmd.CombinedOutput(); err != nil {
		svc.db.LogWarn("update channel stats failed", "error", err, "output", string(out))
	}

	if displayName != "" {
		svc.mu.RLock()
		profile, _ := svc.getProfileUnlocked(userID)
		svc.mu.RUnlock()
		if profile != nil && profile.DisplayName == "" {
			svc.UpdateProfile(userID, map[string]string{"displayName": displayName})
		}
	}

	if svc.cfg.SentimentEnabled && message != "" && svc.sentimentFn != nil {
		score, kws := svc.sentimentFn(message)
		if score != 0 || len(kws) > 0 {
			channel := channelKey
			if idx := strings.Index(channelKey, ":"); idx > 0 {
				channel = channelKey[:idx]
			}

			snippet := message
			if len(snippet) > 100 {
				snippet = snippet[:100]
			}

			keywords := strings.Join(kws, ",")

			logSQL := fmt.Sprintf(
				`INSERT INTO user_mood_log (user_id, channel, sentiment_score, keywords, message_snippet, created_at) VALUES ('%s','%s',%f,'%s','%s','%s')`,
				svc.db.Escape(userID), svc.db.Escape(channel), score,
				svc.db.Escape(keywords), svc.db.Escape(snippet), now)

			cmd2 := exec.Command("sqlite3", svc.dbPath, logSQL)
			if out, err := cmd2.CombinedOutput(); err != nil {
				svc.db.LogWarn("log mood failed", "error", err, "output", string(out))
			}
		}
	}

	return nil
}

// getProfileUnlocked retrieves a profile without acquiring the mutex (caller must hold lock).
func (svc *Service) getProfileUnlocked(userID string) (*UserProfile, error) {
	rows, err := svc.db.Query(svc.dbPath, fmt.Sprintf(
		`SELECT id, display_name, preferred_language, timezone, personality_notes, created_at, updated_at FROM user_profiles WHERE id = '%s' LIMIT 1`,
		svc.db.Escape(userID)))
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	p := profileFromRow(rows[0])
	return &p, nil
}

// --- Mood Tracking ---

// GetMoodTrend returns recent mood entries for a user over the given number of days.
func (svc *Service) GetMoodTrend(userID string, days int) ([]map[string]any, error) {
	svc.mu.RLock()
	defer svc.mu.RUnlock()

	if days <= 0 {
		days = 7
	}
	since := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)

	rows, err := svc.db.Query(svc.dbPath, fmt.Sprintf(
		`SELECT id, user_id, channel, sentiment_score, keywords, message_snippet, created_at FROM user_mood_log WHERE user_id = '%s' AND created_at >= '%s' ORDER BY created_at DESC LIMIT 100`,
		svc.db.Escape(userID), since))
	if err != nil {
		return nil, fmt.Errorf("get mood trend: %w", err)
	}

	var results []map[string]any
	for _, row := range rows {
		results = append(results, map[string]any{
			"sentimentScore": jsonFloat(row["sentiment_score"]),
			"keywords":       jsonStr(row["keywords"]),
			"channel":        jsonStr(row["channel"]),
			"snippet":        jsonStr(row["message_snippet"]),
			"createdAt":      jsonStr(row["created_at"]),
		})
	}
	return results, nil
}

// DBPath returns the database path for external queries.
func (svc *Service) DBPath() string {
	return svc.dbPath
}

// --- Full User Context (for dispatch injection) ---

// GetUserContext returns a complete context map for a user.
func (svc *Service) GetUserContext(channelKey string) (map[string]any, error) {
	userID, err := svc.ResolveUser(channelKey)
	if err != nil {
		return nil, fmt.Errorf("get user context: %w", err)
	}

	result := map[string]any{
		"userId":     userID,
		"channelKey": channelKey,
	}

	profile, err := svc.GetProfile(userID)
	if err != nil {
		return nil, fmt.Errorf("get user context: %w", err)
	}
	if profile != nil {
		result["profile"] = profile
	}

	identities, err := svc.GetChannelIdentities(userID)
	if err == nil && len(identities) > 0 {
		result["channels"] = identities
	}

	prefs, err := svc.GetPreferences(userID, "")
	if err == nil && len(prefs) > 0 {
		result["preferences"] = prefs
	}

	mood, err := svc.GetMoodTrend(userID, 7)
	if err == nil && len(mood) > 0 {
		result["recentMood"] = mood

		var total float64
		for _, m := range mood {
			if s, ok := m["sentimentScore"].(float64); ok {
				total += s
			}
		}
		avg := total / float64(len(mood))
		result["averageMood"] = avg
		if svc.sentimentLabel != nil {
			result["moodLabel"] = svc.sentimentLabel(avg)
		}
	}

	return result, nil
}

// --- Row Parsing Helpers ---

func profileFromRow(row map[string]any) UserProfile {
	return UserProfile{
		ID:                jsonStr(row["id"]),
		DisplayName:       jsonStr(row["display_name"]),
		PreferredLanguage: jsonStr(row["preferred_language"]),
		Timezone:          jsonStr(row["timezone"]),
		PersonalityNotes:  jsonStr(row["personality_notes"]),
		CreatedAt:         jsonStr(row["created_at"]),
		UpdatedAt:         jsonStr(row["updated_at"]),
	}
}

// --- JSON helpers (local to package, same as root) ---

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	case json.Number:
		return s.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func jsonInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case string:
		var i int
		fmt.Sscanf(n, "%d", &i)
		return i
	default:
		return 0
	}
}

func jsonFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	case string:
		var f float64
		fmt.Sscanf(n, "%f", &f)
		return f
	default:
		return 0
	}
}
