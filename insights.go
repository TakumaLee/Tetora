package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"tetora/internal/automation/insights"
	"tetora/internal/db"
)

// --- P24.3: Life Insights Engine ---

var globalInsightsEngine *insights.Engine

// newInsightsEngine constructs an insights.Engine from Config + globals.
func newInsightsEngine(cfg *Config) *insights.Engine {
	deps := insights.Deps{
		Query:   db.Query,
		Escape:  db.Escape,
		LogWarn: logWarn,
		UUID:    newUUID,
	}
	if globalFinanceService != nil {
		deps.FinanceDBPath = globalFinanceService.DBPath()
	}
	if globalTaskManager != nil {
		deps.TasksDBPath = globalTaskManager.DBPath()
	}
	if globalUserProfileService != nil {
		deps.ProfileDBPath = globalUserProfileService.DBPath()
	}
	if globalContactsService != nil {
		deps.ContactsDBPath = globalContactsService.DBPath()
	}
	if globalHabitsService != nil {
		deps.HabitsDBPath = globalHabitsService.DBPath()
		deps.GetHabitStreak = globalHabitsService.GetStreak
	}
	return insights.New(cfg.HistoryDB, deps)
}

func initInsightsDB(dbPath string) error {
	return insights.InitDB(dbPath)
}

// --- Tool Handlers ---

// toolLifeReport handles the life_report tool.
func toolLifeReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Insights == nil {
		return "", fmt.Errorf("insights engine not initialized")
	}

	var args struct {
		Period string `json:"period"`
		Date   string `json:"date"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	period := args.Period
	if period == "" {
		period = "weekly"
	}
	if period != "daily" && period != "weekly" && period != "monthly" {
		return "", fmt.Errorf("invalid period %q (use: daily, weekly, monthly)", period)
	}

	targetDate := time.Now().UTC()
	if args.Date != "" {
		parsed, err := time.Parse("2006-01-02", args.Date)
		if err != nil {
			return "", fmt.Errorf("invalid date format (expected YYYY-MM-DD): %w", err)
		}
		targetDate = parsed
	}

	report, err := app.Insights.GenerateReport(period, targetDate)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(report, "", "  ")
	return string(out), nil
}

// toolLifeInsights handles the life_insights tool.
func toolLifeInsights(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Insights == nil {
		return "", fmt.Errorf("insights engine not initialized")
	}

	var args struct {
		Action    string `json:"action"`
		Days      int    `json:"days"`
		InsightID string `json:"insight_id"`
		Month     string `json:"month"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	switch args.Action {
	case "detect":
		days := args.Days
		if days <= 0 {
			days = 7
		}
		insights, err := app.Insights.DetectAnomalies(days)
		if err != nil {
			return "", err
		}
		if len(insights) == 0 {
			return `{"message":"No anomalies detected","insights":[]}`, nil
		}
		out, _ := json.MarshalIndent(map[string]any{
			"insights": insights,
			"count":    len(insights),
		}, "", "  ")
		return string(out), nil

	case "list":
		insights, err := app.Insights.GetInsights(20, false)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(insights, "", "  ")
		return string(out), nil

	case "acknowledge":
		if args.InsightID == "" {
			return "", fmt.Errorf("insight_id is required for acknowledge action")
		}
		if err := app.Insights.AcknowledgeInsight(args.InsightID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Insight %s acknowledged.", args.InsightID), nil

	case "forecast":
		result, err := app.Insights.SpendingForecast(args.Month)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return string(out), nil

	default:
		return "", fmt.Errorf("unknown action %q (use: detect, list, acknowledge, forecast)", args.Action)
	}
}

// --- Helpers ---

// insightsDBPath returns the database path for insights.
func insightsDBPath(cfg *Config) string {
	if cfg.HistoryDB != "" {
		return cfg.HistoryDB
	}
	return filepath.Join(cfg.BaseDir, "history.db")
}
