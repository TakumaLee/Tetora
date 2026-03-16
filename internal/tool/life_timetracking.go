package tool

import (
	"encoding/json"
	"fmt"

	"tetora/internal/life/timetracking"
)

// TimeStart handles the time_start tool.
func TimeStart(svc *timetracking.Service, uuidFn func() string, input json.RawMessage) (string, error) {
	var args struct {
		Project  string   `json:"project"`
		Activity string   `json:"activity"`
		Tags     []string `json:"tags"`
		UserID   string   `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	entry, err := svc.StartTimer(args.UserID, args.Project, args.Activity, args.Tags, uuidFn)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(entry, "", "  ")
	return string(out), nil
}

// TimeStop handles the time_stop tool.
func TimeStop(svc *timetracking.Service, input json.RawMessage) (string, error) {
	var args struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	entry, err := svc.StopTimer(args.UserID)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(entry, "", "  ")
	return fmt.Sprintf("Timer stopped. Duration: %d minutes\n%s", entry.DurationMinutes, string(out)), nil
}

// TimeLog handles the time_log tool.
func TimeLog(svc *timetracking.Service, uuidFn func() string, input json.RawMessage) (string, error) {
	var args struct {
		Project  string   `json:"project"`
		Activity string   `json:"activity"`
		Duration int      `json:"duration"`
		Date     string   `json:"date"`
		Note     string   `json:"note"`
		Tags     []string `json:"tags"`
		UserID   string   `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Duration <= 0 {
		return "", fmt.Errorf("duration (minutes) is required and must be positive")
	}
	entry, err := svc.LogEntry(args.UserID, args.Project, args.Activity, args.Duration, args.Date, args.Note, args.Tags, uuidFn)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(entry, "", "  ")
	return string(out), nil
}

// TimeReport handles the time_report tool.
func TimeReport(svc *timetracking.Service, input json.RawMessage) (string, error) {
	var args struct {
		Period  string `json:"period"`
		Project string `json:"project"`
		UserID  string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	report, err := svc.Report(args.UserID, args.Period, args.Project)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(report, "", "  ")
	return string(out), nil
}
