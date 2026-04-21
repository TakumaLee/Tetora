package cli

import (
	"encoding/json"
	"fmt"
	"os"
)

// JobsFile is the top-level structure of jobs.json.
type JobsFile struct {
	Jobs []JobConfig `json:"jobs"`
}

// JobConfig mirrors CronJobConfig for CLI operations.
type JobConfig struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Enabled         bool       `json:"enabled"`
	Schedule        string     `json:"schedule"`
	TZ              string     `json:"tz,omitempty"`
	Agent           string     `json:"agent,omitempty"`
	Task            TaskConfig `json:"task"`
	Notify          bool       `json:"notify,omitempty"`
	NotifyChannel   string     `json:"notifyChannel,omitempty"`
	MaxRetries      int        `json:"maxRetries,omitempty"`
	RetryDelay      string     `json:"retryDelay,omitempty"`
	OnSuccess       string     `json:"onSuccess,omitempty"`
	OnFailure       string     `json:"onFailure,omitempty"`
	RequireApproval bool       `json:"requireApproval,omitempty"`
	ApprovalTimeout string     `json:"approvalTimeout,omitempty"`
	IdleMinHours    float64    `json:"idleMinHours,omitempty"`
	IdleMinMinutes  int        `json:"idleMinMinutes,omitempty"`
	CooldownHours   float64    `json:"cooldownHours,omitempty"`
}

// TaskConfig mirrors CronTaskConfig.
type TaskConfig struct {
	Prompt         string   `json:"prompt"`
	PromptFile     string   `json:"promptFile,omitempty"`
	Workdir        string   `json:"workdir,omitempty"`
	Model          string   `json:"model,omitempty"`
	Provider       string   `json:"provider,omitempty"`
	Docker         *bool    `json:"docker,omitempty"`
	Timeout        string   `json:"timeout,omitempty"`
	Budget         float64  `json:"budget,omitempty"`
	PermissionMode string   `json:"permissionMode,omitempty"`
	MCP            []string `json:"mcp,omitempty"`
	AddDirs        []string `json:"addDirs,omitempty"`
	ScopeBoundary  string   `json:"scopeBoundary,omitempty"` // diagnostic_only | implement_allowed | test_only | review_only
}

// JobStatus mirrors CronJobInfo (API response from /cron).
type JobStatus struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Enabled          bool    `json:"enabled"`
	Schedule         string  `json:"schedule"`
	TZ               string  `json:"tz,omitempty"`
	Agent            string  `json:"agent,omitempty"`
	Running          bool    `json:"running"`
	RunCount         int     `json:"runCount"`
	MaxConcurrentRuns int    `json:"maxConcurrentRuns,omitempty"`
	NextRun          string  `json:"nextRun,omitempty"`
	LastRun          string  `json:"lastRun,omitempty"`
	LastErr          string  `json:"lastErr,omitempty"`
	LastCost         float64 `json:"lastCost"`
	AvgCost          float64 `json:"avgCost"`
	Errors           int     `json:"errors"`
	RunStart         string  `json:"runStart,omitempty"`
	RunElapsed       string  `json:"runElapsed,omitempty"`
	RunTimeout       string  `json:"runTimeout,omitempty"`
	RunModel         string  `json:"runModel,omitempty"`
	RunPrompt        string  `json:"runPrompt,omitempty"`
}

// LoadJobsFile reads and parses the jobs.json file.
func LoadJobsFile(path string) JobsFile {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return JobsFile{}
		}
		fmt.Fprintf(os.Stderr, "Error reading jobs: %v\n", err)
		os.Exit(1)
	}
	var jf JobsFile
	if err := json.Unmarshal(data, &jf); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing jobs: %v\n", err)
		os.Exit(1)
	}
	return jf
}

// SaveJobsFile writes the jobs file to disk.
func SaveJobsFile(path string, jf JobsFile) {
	data, _ := json.MarshalIndent(jf, "", "  ")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing jobs: %v\n", err)
		os.Exit(1)
	}
}
