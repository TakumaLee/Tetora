package main

import (
	"os"
	"path/filepath"
	"testing"
)

// testProfileDB creates a temp DB file and initializes schema.
func testProfileDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_profile.db")
	if err := initUserProfileDB(dbPath); err != nil {
		t.Fatalf("initUserProfileDB: %v", err)
	}
	return dbPath
}

func TestInitUserProfileDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "init_test.db")
	err := initUserProfileDB(dbPath)
	if err != nil {
		t.Fatalf("initUserProfileDB failed: %v", err)
	}
	// DB file should exist.
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db file not created: %v", err)
	}

	// Idempotent: run again should not fail.
	err = initUserProfileDB(dbPath)
	if err != nil {
		t.Fatalf("initUserProfileDB second call failed: %v", err)
	}
}

func TestCreateProfile(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := &UserProfileService{dbPath: dbPath, cfg: &Config{}}

	profile := UserProfile{
		ID:                "user-001",
		DisplayName:       "Alice",
		PreferredLanguage: "en",
		Timezone:          "America/New_York",
	}
	err := svc.CreateProfile(profile)
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	// Duplicate insert should not fail (INSERT OR IGNORE).
	err = svc.CreateProfile(profile)
	if err != nil {
		t.Fatalf("CreateProfile duplicate: %v", err)
	}
}

func TestGetProfile(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := &UserProfileService{dbPath: dbPath, cfg: &Config{}}

	// Non-existent.
	p, err := svc.GetProfile("nonexistent")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p != nil {
		t.Fatalf("expected nil for nonexistent, got %+v", p)
	}

	// Create and retrieve.
	svc.CreateProfile(UserProfile{ID: "user-002", DisplayName: "Bob", PreferredLanguage: "ja"})
	p, err = svc.GetProfile("user-002")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p == nil {
		t.Fatal("expected profile, got nil")
	}
	if p.DisplayName != "Bob" {
		t.Errorf("DisplayName = %q, want 'Bob'", p.DisplayName)
	}
	if p.PreferredLanguage != "ja" {
		t.Errorf("PreferredLanguage = %q, want 'ja'", p.PreferredLanguage)
	}
}

func TestUpdateProfile(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := &UserProfileService{dbPath: dbPath, cfg: &Config{}}

	svc.CreateProfile(UserProfile{ID: "user-003", DisplayName: "Charlie"})

	err := svc.UpdateProfile("user-003", map[string]string{
		"displayName":       "Charles",
		"preferredLanguage": "fr",
		"timezone":          "Europe/Paris",
	})
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	p, _ := svc.GetProfile("user-003")
	if p == nil {
		t.Fatal("expected profile, got nil")
	}
	if p.DisplayName != "Charles" {
		t.Errorf("DisplayName = %q, want 'Charles'", p.DisplayName)
	}
	if p.PreferredLanguage != "fr" {
		t.Errorf("PreferredLanguage = %q, want 'fr'", p.PreferredLanguage)
	}
	if p.Timezone != "Europe/Paris" {
		t.Errorf("Timezone = %q, want 'Europe/Paris'", p.Timezone)
	}
}

func TestResolveUser(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := &UserProfileService{dbPath: dbPath, cfg: &Config{}}

	// First resolution creates a new user.
	uid1, err := svc.ResolveUser("tg:12345")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	if uid1 == "" {
		t.Fatal("expected non-empty userID")
	}

	// Second resolution returns same user.
	uid2, err := svc.ResolveUser("tg:12345")
	if err != nil {
		t.Fatalf("ResolveUser second call: %v", err)
	}
	if uid1 != uid2 {
		t.Errorf("ResolveUser returned different IDs: %q vs %q", uid1, uid2)
	}

	// Different channel key creates different user.
	uid3, err := svc.ResolveUser("discord:67890")
	if err != nil {
		t.Fatalf("ResolveUser different channel: %v", err)
	}
	if uid3 == uid1 {
		t.Error("different channel keys should create different users")
	}
}

func TestLinkChannel(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := &UserProfileService{dbPath: dbPath, cfg: &Config{}}

	// Create a user.
	svc.CreateProfile(UserProfile{ID: "user-link-001", DisplayName: "LinkTest"})

	// Link a channel.
	err := svc.LinkChannel("user-link-001", "slack:abc", "LinkTest Slack")
	if err != nil {
		t.Fatalf("LinkChannel: %v", err)
	}

	// Resolve the channel should return same user.
	uid, err := svc.ResolveUser("slack:abc")
	if err != nil {
		t.Fatalf("ResolveUser after link: %v", err)
	}
	if uid != "user-link-001" {
		t.Errorf("ResolveUser = %q, want 'user-link-001'", uid)
	}

	// Link another channel to same user.
	err = svc.LinkChannel("user-link-001", "tg:99999", "LinkTest TG")
	if err != nil {
		t.Fatalf("LinkChannel second: %v", err)
	}
}

func TestGetChannelIdentities(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := &UserProfileService{dbPath: dbPath, cfg: &Config{}}

	svc.CreateProfile(UserProfile{ID: "user-ci-001"})
	svc.LinkChannel("user-ci-001", "tg:111", "TG User")
	svc.LinkChannel("user-ci-001", "discord:222", "Discord User")
	svc.LinkChannel("user-ci-001", "slack:333", "Slack User")

	ids, err := svc.GetChannelIdentities("user-ci-001")
	if err != nil {
		t.Fatalf("GetChannelIdentities: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 identities, got %d", len(ids))
	}

	// Verify all channel keys present.
	keys := map[string]bool{}
	for _, id := range ids {
		keys[id.ChannelKey] = true
	}
	for _, k := range []string{"tg:111", "discord:222", "slack:333"} {
		if !keys[k] {
			t.Errorf("missing channel key %q", k)
		}
	}
}

func TestObservePreference(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := &UserProfileService{dbPath: dbPath, cfg: &Config{}}

	svc.CreateProfile(UserProfile{ID: "user-pref-001"})

	// First observation.
	err := svc.ObservePreference("user-pref-001", "food", "favorite_cuisine", "japanese")
	if err != nil {
		t.Fatalf("ObservePreference: %v", err)
	}

	prefs, err := svc.GetPreferences("user-pref-001", "food")
	if err != nil {
		t.Fatalf("GetPreferences: %v", err)
	}
	if len(prefs) != 1 {
		t.Fatalf("expected 1 preference, got %d", len(prefs))
	}
	if prefs[0].Key != "favorite_cuisine" {
		t.Errorf("Key = %q, want 'favorite_cuisine'", prefs[0].Key)
	}
	if prefs[0].Value != "japanese" {
		t.Errorf("Value = %q, want 'japanese'", prefs[0].Value)
	}
	if prefs[0].ObservedCount != 1 {
		t.Errorf("ObservedCount = %d, want 1", prefs[0].ObservedCount)
	}
	if prefs[0].Confidence != 0.5 {
		t.Errorf("Confidence = %f, want 0.5", prefs[0].Confidence)
	}
}

func TestGetPreferences_ConfidenceGrows(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := &UserProfileService{dbPath: dbPath, cfg: &Config{}}

	svc.CreateProfile(UserProfile{ID: "user-conf-001"})

	// Observe same preference multiple times.
	for i := 0; i < 5; i++ {
		err := svc.ObservePreference("user-conf-001", "schedule", "morning_person", "true")
		if err != nil {
			t.Fatalf("ObservePreference iteration %d: %v", i, err)
		}
	}

	prefs, err := svc.GetPreferences("user-conf-001", "schedule")
	if err != nil {
		t.Fatalf("GetPreferences: %v", err)
	}
	if len(prefs) != 1 {
		t.Fatalf("expected 1 preference, got %d", len(prefs))
	}
	if prefs[0].ObservedCount != 5 {
		t.Errorf("ObservedCount = %d, want 5", prefs[0].ObservedCount)
	}
	// Confidence should have grown: min(1.0, 0.5 + 5*0.1) = 1.0
	if prefs[0].Confidence != 1.0 {
		t.Errorf("Confidence = %f, want 1.0", prefs[0].Confidence)
	}
}

func TestRecordMessage(t *testing.T) {
	dbPath := testProfileDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		UserProfile: UserProfileConfig{
			Enabled:          true,
			SentimentEnabled: true,
		},
	}
	svc := &UserProfileService{dbPath: dbPath, cfg: cfg}

	// Record a positive message.
	err := svc.RecordMessage("tg:msg-001", "TestUser", "I'm so happy today!")
	if err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}

	// Verify user was created.
	uid, err := svc.ResolveUser("tg:msg-001")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}

	// Check mood log.
	mood, err := svc.GetMoodTrend(uid, 7)
	if err != nil {
		t.Fatalf("GetMoodTrend: %v", err)
	}
	if len(mood) == 0 {
		t.Fatal("expected mood entries after positive message")
	}
	score, ok := mood[0]["sentimentScore"].(float64)
	if !ok || score <= 0 {
		t.Errorf("expected positive sentiment score, got %v", mood[0]["sentimentScore"])
	}
}

func TestRecordMessage_NoSentiment(t *testing.T) {
	dbPath := testProfileDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		UserProfile: UserProfileConfig{
			Enabled:          true,
			SentimentEnabled: false, // Disabled
		},
	}
	svc := &UserProfileService{dbPath: dbPath, cfg: cfg}

	// Record a message -- should not log mood.
	err := svc.RecordMessage("tg:nosenti-001", "TestUser", "I'm so happy today!")
	if err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}

	uid, _ := svc.ResolveUser("tg:nosenti-001")
	mood, _ := svc.GetMoodTrend(uid, 7)
	if len(mood) != 0 {
		t.Errorf("expected no mood entries with sentiment disabled, got %d", len(mood))
	}
}

func TestGetMoodTrend(t *testing.T) {
	dbPath := testProfileDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		UserProfile: UserProfileConfig{
			Enabled:          true,
			SentimentEnabled: true,
		},
	}
	svc := &UserProfileService{dbPath: dbPath, cfg: cfg}

	// Record multiple messages with different sentiments.
	svc.RecordMessage("tg:mood-001", "MoodUser", "I'm so happy and love this!")
	svc.RecordMessage("tg:mood-001", "MoodUser", "This is terrible and awful")
	svc.RecordMessage("tg:mood-001", "MoodUser", "Thanks, great work!")

	uid, _ := svc.ResolveUser("tg:mood-001")
	mood, err := svc.GetMoodTrend(uid, 7)
	if err != nil {
		t.Fatalf("GetMoodTrend: %v", err)
	}
	// Should have at least some entries (neutral messages won't be logged).
	if len(mood) < 2 {
		t.Errorf("expected at least 2 mood entries, got %d", len(mood))
	}
}

func TestGetUserContext(t *testing.T) {
	dbPath := testProfileDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		UserProfile: UserProfileConfig{
			Enabled:          true,
			SentimentEnabled: true,
		},
	}
	svc := &UserProfileService{dbPath: dbPath, cfg: cfg}

	// Set up a user with data.
	svc.RecordMessage("tg:ctx-001", "ContextUser", "I love sushi!")
	uid, _ := svc.ResolveUser("tg:ctx-001")
	svc.UpdateProfile(uid, map[string]string{
		"displayName":       "ContextUser",
		"preferredLanguage": "ja",
		"timezone":          "Asia/Tokyo",
	})
	svc.ObservePreference(uid, "food", "favorite", "sushi")

	// Get full context.
	ctx, err := svc.GetUserContext("tg:ctx-001")
	if err != nil {
		t.Fatalf("GetUserContext: %v", err)
	}
	if ctx["userId"] != uid {
		t.Errorf("userId = %v, want %s", ctx["userId"], uid)
	}
	if ctx["profile"] == nil {
		t.Error("expected profile in context")
	}
	if ctx["preferences"] == nil {
		t.Error("expected preferences in context")
	}
}

func TestGetPreferences_FilterByCategory(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := &UserProfileService{dbPath: dbPath, cfg: &Config{}}

	svc.CreateProfile(UserProfile{ID: "user-cat-001"})
	svc.ObservePreference("user-cat-001", "food", "favorite", "sushi")
	svc.ObservePreference("user-cat-001", "schedule", "wake_time", "7am")

	// Filter by food.
	prefs, _ := svc.GetPreferences("user-cat-001", "food")
	if len(prefs) != 1 {
		t.Errorf("expected 1 food preference, got %d", len(prefs))
	}

	// Filter by schedule.
	prefs, _ = svc.GetPreferences("user-cat-001", "schedule")
	if len(prefs) != 1 {
		t.Errorf("expected 1 schedule preference, got %d", len(prefs))
	}

	// All.
	prefs, _ = svc.GetPreferences("user-cat-001", "")
	if len(prefs) != 2 {
		t.Errorf("expected 2 total preferences, got %d", len(prefs))
	}
}
