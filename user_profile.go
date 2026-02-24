package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// --- P23.1: User Profile & Emotional Memory ---

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

// UserProfileService manages user profiles, channel identities, preferences, and mood.
type UserProfileService struct {
	mu     sync.RWMutex
	cfg    *Config
	dbPath string
}

// --- Global Singleton ---

var globalUserProfileService *UserProfileService

// --- DB Schema ---

func initUserProfileDB(dbPath string) error {
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

// --- Constructor ---

func newUserProfileService(cfg *Config) *UserProfileService {
	return &UserProfileService{
		cfg:    cfg,
		dbPath: cfg.HistoryDB,
	}
}

// --- Profile CRUD ---

// GetProfile retrieves a user profile by ID.
func (svc *UserProfileService) GetProfile(userID string) (*UserProfile, error) {
	svc.mu.RLock()
	defer svc.mu.RUnlock()

	rows, err := queryDB(svc.dbPath, fmt.Sprintf(
		`SELECT id, display_name, preferred_language, timezone, personality_notes, created_at, updated_at FROM user_profiles WHERE id = '%s' LIMIT 1`,
		escapeSQLite(userID)))
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
func (svc *UserProfileService) CreateProfile(profile UserProfile) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	if profile.ID == "" {
		profile.ID = umNewID()
	}
	if profile.CreatedAt == "" {
		profile.CreatedAt = now
	}
	if profile.UpdatedAt == "" {
		profile.UpdatedAt = now
	}

	sql := fmt.Sprintf(
		`INSERT OR IGNORE INTO user_profiles (id, display_name, preferred_language, timezone, personality_notes, created_at, updated_at) VALUES ('%s','%s','%s','%s','%s','%s','%s')`,
		escapeSQLite(profile.ID),
		escapeSQLite(profile.DisplayName),
		escapeSQLite(profile.PreferredLanguage),
		escapeSQLite(profile.Timezone),
		escapeSQLite(profile.PersonalityNotes),
		escapeSQLite(profile.CreatedAt),
		escapeSQLite(profile.UpdatedAt),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create profile: %w: %s", err, string(out))
	}
	return nil
}

// UpdateProfile updates specific fields of a user profile.
func (svc *UserProfileService) UpdateProfile(userID string, updates map[string]string) error {
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
		setParts = append(setParts, fmt.Sprintf("%s = '%s'", col, escapeSQLite(v)))
	}
	if len(setParts) == 0 {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	setParts = append(setParts, fmt.Sprintf("updated_at = '%s'", now))

	sql := fmt.Sprintf("UPDATE user_profiles SET %s WHERE id = '%s'",
		strings.Join(setParts, ", "), escapeSQLite(userID))
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("update profile: %w: %s", err, string(out))
	}
	return nil
}

// --- Channel Identity Resolution ---

// ResolveUser resolves a channel key to a user ID, creating a new user if needed.
func (svc *UserProfileService) ResolveUser(channelKey string) (string, error) {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	// Check if channel key already mapped.
	rows, err := queryDB(svc.dbPath, fmt.Sprintf(
		`SELECT user_id FROM channel_user_map WHERE channel_key = '%s' LIMIT 1`,
		escapeSQLite(channelKey)))
	if err != nil {
		return "", fmt.Errorf("resolve user: %w", err)
	}
	if len(rows) > 0 {
		return jsonStr(rows[0]["user_id"]), nil
	}

	// Create a new user profile and link.
	userID := umNewID()
	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT OR IGNORE INTO user_profiles (id, display_name, created_at, updated_at) VALUES ('%s','','%s','%s');
INSERT OR IGNORE INTO channel_user_map (channel_key, user_id, last_seen, message_count) VALUES ('%s','%s','%s',0)`,
		escapeSQLite(userID), now, now,
		escapeSQLite(channelKey), escapeSQLite(userID), now)

	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("create user for channel: %w: %s", err, string(out))
	}

	return userID, nil
}

// LinkChannel links a channel key to an existing user.
func (svc *UserProfileService) LinkChannel(userID, channelKey, displayName string) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO channel_user_map (channel_key, user_id, channel_display_name, last_seen, message_count) VALUES ('%s','%s','%s','%s',0)
ON CONFLICT(channel_key) DO UPDATE SET user_id='%s', channel_display_name='%s', last_seen='%s'`,
		escapeSQLite(channelKey), escapeSQLite(userID), escapeSQLite(displayName), now,
		escapeSQLite(userID), escapeSQLite(displayName), now)

	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("link channel: %w: %s", err, string(out))
	}
	return nil
}

// GetChannelIdentities returns all channel identities for a user.
func (svc *UserProfileService) GetChannelIdentities(userID string) ([]ChannelIdentity, error) {
	svc.mu.RLock()
	defer svc.mu.RUnlock()

	rows, err := queryDB(svc.dbPath, fmt.Sprintf(
		`SELECT channel_key, user_id, channel_display_name, last_seen, message_count FROM channel_user_map WHERE user_id = '%s' ORDER BY last_seen DESC`,
		escapeSQLite(userID)))
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
// Uses UPSERT: if exists, increments observed_count and recalculates confidence.
func (svc *UserProfileService) ObservePreference(userID, category, key, value string) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)

	// Use INSERT ... ON CONFLICT for upsert.
	// On conflict: increment observed_count, update value/last_observed,
	// recalculate confidence = min(1.0, 0.5 + observed_count * 0.1).
	sql := fmt.Sprintf(
		`INSERT INTO user_preferences (user_id, category, key, value, confidence, observed_count, first_observed, last_observed)
VALUES ('%s','%s','%s','%s', 0.5, 1, '%s', '%s')
ON CONFLICT(user_id, category, key) DO UPDATE SET
    value = '%s',
    observed_count = observed_count + 1,
    last_observed = '%s',
    confidence = MIN(1.0, 0.5 + (observed_count + 1) * 0.1)`,
		escapeSQLite(userID), escapeSQLite(category), escapeSQLite(key), escapeSQLite(value),
		now, now,
		escapeSQLite(value), now)

	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("observe preference: %w: %s", err, string(out))
	}
	return nil
}

// GetPreferences returns preferences for a user, optionally filtered by category.
func (svc *UserProfileService) GetPreferences(userID string, category string) ([]UserPreference, error) {
	svc.mu.RLock()
	defer svc.mu.RUnlock()

	sql := fmt.Sprintf(
		`SELECT id, user_id, category, key, value, confidence, observed_count, first_observed, last_observed FROM user_preferences WHERE user_id = '%s'`,
		escapeSQLite(userID))
	if category != "" {
		sql += fmt.Sprintf(` AND category = '%s'`, escapeSQLite(category))
	}
	sql += ` ORDER BY confidence DESC, last_observed DESC`

	rows, err := queryDB(svc.dbPath, sql)
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
func (svc *UserProfileService) RecordMessage(channelKey, displayName, message string) error {
	// Step 1: Resolve user (creates if needed).
	userID, err := svc.ResolveUser(channelKey)
	if err != nil {
		return fmt.Errorf("record message: resolve user: %w", err)
	}

	// Step 2: Update channel_user_map (last_seen + message_count++).
	now := time.Now().UTC().Format(time.RFC3339)
	updateSQL := fmt.Sprintf(
		`UPDATE channel_user_map SET last_seen = '%s', message_count = message_count + 1, channel_display_name = '%s' WHERE channel_key = '%s'`,
		now, escapeSQLite(displayName), escapeSQLite(channelKey))

	cmd := exec.Command("sqlite3", svc.dbPath, updateSQL)
	if out, err := cmd.CombinedOutput(); err != nil {
		logWarn("update channel stats failed", "error", err, "output", string(out))
	}

	// Update display name on user profile if it's empty.
	if displayName != "" {
		svc.mu.RLock()
		profile, _ := svc.getProfileUnlocked(userID)
		svc.mu.RUnlock()
		if profile != nil && profile.DisplayName == "" {
			svc.UpdateProfile(userID, map[string]string{"displayName": displayName})
		}
	}

	// Step 3: Sentiment analysis (if enabled).
	if svc.cfg.UserProfile.SentimentEnabled && message != "" {
		result := analyzeSentiment(message)
		if result.Score != 0 || len(result.Keywords) > 0 {
			// Extract channel name from key (e.g., "tg:12345" -> "tg").
			channel := channelKey
			if idx := strings.Index(channelKey, ":"); idx > 0 {
				channel = channelKey[:idx]
			}

			// Truncate message snippet.
			snippet := message
			if len(snippet) > 100 {
				snippet = snippet[:100]
			}

			keywords := strings.Join(result.Keywords, ",")

			logSQL := fmt.Sprintf(
				`INSERT INTO user_mood_log (user_id, channel, sentiment_score, keywords, message_snippet, created_at) VALUES ('%s','%s',%f,'%s','%s','%s')`,
				escapeSQLite(userID), escapeSQLite(channel), result.Score,
				escapeSQLite(keywords), escapeSQLite(snippet), now)

			cmd2 := exec.Command("sqlite3", svc.dbPath, logSQL)
			if out, err := cmd2.CombinedOutput(); err != nil {
				logWarn("log mood failed", "error", err, "output", string(out))
			}
		}
	}

	return nil
}

// getProfileUnlocked retrieves a profile without acquiring the mutex (caller must hold lock).
func (svc *UserProfileService) getProfileUnlocked(userID string) (*UserProfile, error) {
	rows, err := queryDB(svc.dbPath, fmt.Sprintf(
		`SELECT id, display_name, preferred_language, timezone, personality_notes, created_at, updated_at FROM user_profiles WHERE id = '%s' LIMIT 1`,
		escapeSQLite(userID)))
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
func (svc *UserProfileService) GetMoodTrend(userID string, days int) ([]map[string]any, error) {
	svc.mu.RLock()
	defer svc.mu.RUnlock()

	if days <= 0 {
		days = 7
	}
	since := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)

	rows, err := queryDB(svc.dbPath, fmt.Sprintf(
		`SELECT id, user_id, channel, sentiment_score, keywords, message_snippet, created_at FROM user_mood_log WHERE user_id = '%s' AND created_at >= '%s' ORDER BY created_at DESC LIMIT 100`,
		escapeSQLite(userID), since))
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

// --- Full User Context (for dispatch injection) ---

// GetUserContext returns a complete context map for a user, including profile, preferences, and mood.
func (svc *UserProfileService) GetUserContext(channelKey string) (map[string]any, error) {
	// Resolve user.
	userID, err := svc.ResolveUser(channelKey)
	if err != nil {
		return nil, fmt.Errorf("get user context: %w", err)
	}

	result := map[string]any{
		"userId":     userID,
		"channelKey": channelKey,
	}

	// Profile.
	profile, err := svc.GetProfile(userID)
	if err != nil {
		return nil, fmt.Errorf("get user context: %w", err)
	}
	if profile != nil {
		result["profile"] = profile
	}

	// Channel identities.
	identities, err := svc.GetChannelIdentities(userID)
	if err == nil && len(identities) > 0 {
		result["channels"] = identities
	}

	// Preferences (all categories).
	prefs, err := svc.GetPreferences(userID, "")
	if err == nil && len(prefs) > 0 {
		result["preferences"] = prefs
	}

	// Recent mood (7 days).
	mood, err := svc.GetMoodTrend(userID, 7)
	if err == nil && len(mood) > 0 {
		result["recentMood"] = mood

		// Calculate average mood.
		var total float64
		for _, m := range mood {
			if s, ok := m["sentimentScore"].(float64); ok {
				total += s
			}
		}
		avg := total / float64(len(mood))
		result["averageMood"] = avg
		result["moodLabel"] = sentimentLabel(avg)
	}

	return result, nil
}

// --- Tool Handlers ---

func toolUserProfileGet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		UserID     string `json:"userId"`
		ChannelKey string `json:"channelKey"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if globalUserProfileService == nil {
		return "", fmt.Errorf("user profile service not initialized")
	}

	// Resolve channel key to user ID if needed.
	if args.UserID == "" && args.ChannelKey != "" {
		uid, err := globalUserProfileService.ResolveUser(args.ChannelKey)
		if err != nil {
			return "", fmt.Errorf("resolve user: %w", err)
		}
		args.UserID = uid
	}
	if args.UserID == "" {
		return "", fmt.Errorf("userId or channelKey is required")
	}

	userCtx, err := globalUserProfileService.GetUserContext(args.ChannelKey)
	if err != nil {
		// Fallback: try just the profile.
		profile, err2 := globalUserProfileService.GetProfile(args.UserID)
		if err2 != nil {
			return "", fmt.Errorf("get profile: %w", err2)
		}
		if profile == nil {
			return "", fmt.Errorf("user not found")
		}
		b, _ := json.Marshal(profile)
		return string(b), nil
	}

	b, _ := json.Marshal(userCtx)
	return string(b), nil
}

func toolUserProfileSet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		UserID      string `json:"userId"`
		DisplayName string `json:"displayName"`
		Language    string `json:"language"`
		Timezone    string `json:"timezone"`
		ChannelKey  string `json:"channelKey"`
		ChannelName string `json:"channelName"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		return "", fmt.Errorf("userId is required")
	}

	if globalUserProfileService == nil {
		return "", fmt.Errorf("user profile service not initialized")
	}

	// Ensure profile exists.
	profile, _ := globalUserProfileService.GetProfile(args.UserID)
	if profile == nil {
		err := globalUserProfileService.CreateProfile(UserProfile{ID: args.UserID})
		if err != nil {
			return "", fmt.Errorf("create profile: %w", err)
		}
	}

	// Update profile fields.
	updates := make(map[string]string)
	if args.DisplayName != "" {
		updates["displayName"] = args.DisplayName
	}
	if args.Language != "" {
		updates["preferredLanguage"] = args.Language
	}
	if args.Timezone != "" {
		updates["timezone"] = args.Timezone
	}
	if len(updates) > 0 {
		if err := globalUserProfileService.UpdateProfile(args.UserID, updates); err != nil {
			return "", fmt.Errorf("update profile: %w", err)
		}
	}

	// Link channel if provided.
	if args.ChannelKey != "" {
		if err := globalUserProfileService.LinkChannel(args.UserID, args.ChannelKey, args.ChannelName); err != nil {
			return "", fmt.Errorf("link channel: %w", err)
		}
	}

	return fmt.Sprintf(`{"status":"ok","userId":"%s"}`, args.UserID), nil
}

func toolMoodCheck(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		UserID     string `json:"userId"`
		ChannelKey string `json:"channelKey"`
		Days       int    `json:"days"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if globalUserProfileService == nil {
		return "", fmt.Errorf("user profile service not initialized")
	}

	// Resolve.
	if args.UserID == "" && args.ChannelKey != "" {
		uid, err := globalUserProfileService.ResolveUser(args.ChannelKey)
		if err != nil {
			return "", fmt.Errorf("resolve user: %w", err)
		}
		args.UserID = uid
	}
	if args.UserID == "" {
		return "", fmt.Errorf("userId or channelKey is required")
	}

	if args.Days <= 0 {
		args.Days = 7
	}

	mood, err := globalUserProfileService.GetMoodTrend(args.UserID, args.Days)
	if err != nil {
		return "", fmt.Errorf("get mood: %w", err)
	}

	// Calculate summary.
	var totalScore float64
	for _, m := range mood {
		if s, ok := m["sentimentScore"].(float64); ok {
			totalScore += s
		}
	}
	avg := 0.0
	if len(mood) > 0 {
		avg = totalScore / float64(len(mood))
	}

	result := map[string]any{
		"userId":       args.UserID,
		"days":         args.Days,
		"entries":      len(mood),
		"averageScore": avg,
		"label":        sentimentLabel(avg),
		"trend":        mood,
	}

	b, _ := json.Marshal(result)
	return string(b), nil
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
