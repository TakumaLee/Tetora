package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// --- Global Singleton ---

var globalUserProfileService *UserProfileService

// --- Tool Handlers ---

func toolUserProfileGet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		UserID     string `json:"userId"`
		ChannelKey string `json:"channelKey"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	app := appFromCtx(ctx)
	if app == nil || app.UserProfile == nil {
		return "", fmt.Errorf("user profile service not initialized")
	}

	// Resolve channel key to user ID if needed.
	if args.UserID == "" && args.ChannelKey != "" {
		uid, err := app.UserProfile.ResolveUser(args.ChannelKey)
		if err != nil {
			return "", fmt.Errorf("resolve user: %w", err)
		}
		args.UserID = uid
	}
	if args.UserID == "" {
		return "", fmt.Errorf("userId or channelKey is required")
	}

	userCtx, err := app.UserProfile.GetUserContext(args.ChannelKey)
	if err != nil {
		// Fallback: try just the profile.
		profile, err2 := app.UserProfile.GetProfile(args.UserID)
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

	app := appFromCtx(ctx)
	if app == nil || app.UserProfile == nil {
		return "", fmt.Errorf("user profile service not initialized")
	}

	// Ensure profile exists.
	p, _ := app.UserProfile.GetProfile(args.UserID)
	if p == nil {
		err := app.UserProfile.CreateProfile(UserProfile{ID: args.UserID})
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
		if err := app.UserProfile.UpdateProfile(args.UserID, updates); err != nil {
			return "", fmt.Errorf("update profile: %w", err)
		}
	}

	// Link channel if provided.
	if args.ChannelKey != "" {
		if err := app.UserProfile.LinkChannel(args.UserID, args.ChannelKey, args.ChannelName); err != nil {
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

	app := appFromCtx(ctx)
	if app == nil || app.UserProfile == nil {
		return "", fmt.Errorf("user profile service not initialized")
	}

	// Resolve.
	if args.UserID == "" && args.ChannelKey != "" {
		uid, err := app.UserProfile.ResolveUser(args.ChannelKey)
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

	mood, err := app.UserProfile.GetMoodTrend(args.UserID, args.Days)
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
