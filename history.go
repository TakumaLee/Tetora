package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"tetora/internal/log"
	"tetora/internal/history"
)

// --- Type aliases ---

type JobRun = history.JobRun
type CostStats = history.CostStats
type HistoryQuery = history.HistoryQuery
type DayStat = history.DayStat
type MetricsResult = history.MetricsResult
type DailyMetrics = history.DailyMetrics
type ProviderMetrics = history.ProviderMetrics
type SubtaskCount = history.SubtaskCount

// --- Init ---

func initHistoryDB(dbPath string) error {
	return history.InitDB(dbPath)
}

// --- Cron Execution Log ---

func insertCronExecLog(dbPath, jobID, scheduledAt, startedAt string, replayed bool) {
	history.InsertCronExecLog(dbPath, jobID, scheduledAt, startedAt, replayed)
}

func cronExecLogExists(dbPath, jobID string, scheduledAt time.Time) bool {
	return history.CronExecLogExists(dbPath, jobID, scheduledAt)
}

func jobRunExistsNear(dbPath, jobID string, near time.Time) bool {
	return history.JobRunExistsNear(dbPath, jobID, near)
}

// --- Insert ---

func insertJobRun(dbPath string, run JobRun) error {
	return history.InsertRun(dbPath, run)
}

// --- Query History ---

func queryHistory(dbPath, jobID string, limit int) ([]JobRun, error) {
	return history.Query(dbPath, jobID, limit)
}

func queryHistoryByID(dbPath string, id int) (*JobRun, error) {
	return history.QueryByID(dbPath, id)
}

func jobRunFromRow(row map[string]any) JobRun {
	return history.RunFromRow(row)
}

// --- Cost Stats ---

func todayTotalTokens(dbPath string) (int, int) {
	return history.TodayTotalTokens(dbPath)
}

func queryCostStats(dbPath string) (CostStats, error) {
	return history.QueryCostStats(dbPath)
}

// --- Filtered Query ---

func queryHistoryFiltered(dbPath string, q HistoryQuery) ([]JobRun, int, error) {
	return history.QueryFiltered(dbPath, q)
}

func queryCostByJobID(dbPath string) (map[string]float64, error) {
	return history.QueryCostByJobID(dbPath)
}

// --- Query Last Finished ---

func queryLastFinished(dbPath string) time.Time {
	return history.QueryLastFinished(dbPath)
}

// --- Query Last Job Run ---

func queryLastJobRun(dbPath, jobID string) *JobRun {
	return history.QueryLastRun(dbPath, jobID)
}

// --- Job Average Cost ---

func queryJobAvgCost(dbPath, jobID string) float64 {
	return history.QueryJobAvgCost(dbPath, jobID)
}

// --- Daily Stats ---

func queryDailyStats(dbPath string, days int) ([]DayStat, error) {
	return history.QueryDailyStats(dbPath, days)
}

func queryDigestStats(dbPath, from, to string) (total, success, fail int, cost float64, failures []JobRun, err error) {
	return history.QueryDigestStats(dbPath, from, to)
}

// --- Cleanup ---

func cleanupHistory(dbPath string, days int) error {
	return history.Cleanup(dbPath, days)
}

// --- Observability Metrics ---

func queryMetrics(dbPath string, days int) (*MetricsResult, error) {
	return history.QueryMetrics(dbPath, days)
}

func queryDailyMetrics(dbPath string, days int) ([]DailyMetrics, error) {
	return history.QueryDailyMetrics(dbPath, days)
}

func queryProviderMetrics(dbPath string, days int) ([]ProviderMetrics, error) {
	return history.QueryProviderMetrics(dbPath, days)
}

func queryParentSubtaskCounts(dbPath string, parentIDs []string) (map[string]SubtaskCount, error) {
	return history.QueryParentSubtaskCounts(dbPath, parentIDs)
}

// --- JSON helpers (stay in root — used widely across codebase) ---

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case json.Number:
		return val.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func jsonFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case json.Number:
		f, _ := val.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	default:
		return 0
	}
}

func jsonInt(v any) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case json.Number:
		i, _ := val.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(val)
		return i
	default:
		return 0
	}
}

// --- Record History Helper ---
// Used by both cron.go and dispatch.go to record task execution.

func recordHistory(dbPath string, jobID, name, source, role string, task Task, result TaskResult, startedAt, finishedAt, outputFile string) {
	if dbPath == "" {
		return
	}
	run := JobRun{
		JobID:         jobID,
		Name:          name,
		Source:        source,
		StartedAt:     startedAt,
		FinishedAt:    finishedAt,
		Status:        result.Status,
		ExitCode:      result.ExitCode,
		CostUSD:       result.CostUSD,
		OutputSummary: truncateStr(result.Output, 1000),
		Error:         result.Error,
		Model:         result.Model,
		SessionID:     result.SessionID,
		OutputFile:    outputFile,
		TokensIn:      result.TokensIn,
		TokensOut:     result.TokensOut,
		Agent:         role,
		ParentID:      task.ParentID,
	}
	if err := insertJobRun(dbPath, run); err != nil {
		// Log but don't fail the task.
		log.Warn("record history failed", "error", err)
	}

	// Record skill completion events for all skills that were injected for this task.
	recordSkillCompletion(dbPath, task, result, role, startedAt, finishedAt)
}

// --- Generic helpers ---

// truncateStr is like truncate() but avoids name collision if truncate is in another file.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// stringSliceContains checks if a string slice contains a value.
func stringSliceContains(ss []string, s string) bool {
	for _, v := range ss {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
