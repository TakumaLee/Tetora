// Package codex implements the CodexProvider: executes tasks using the Codex CLI (codex exec --json).
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"tetora/internal/provider"
)

var (
	rePercent = regexp.MustCompile(`(\d+(?:\.\d+)?)%`)
	reReset   = regexp.MustCompile(`resets?\s+(.+?)(?:\)|$)`)
)

func pctRegexp() *regexp.Regexp   { return rePercent }
func resetRegexp() *regexp.Regexp { return reReset }

func detectQuotaError(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return ""
	}
	if strings.Contains(lower, "out of extra usage") ||
		(strings.Contains(lower, "extra usage") && strings.Contains(lower, "resets")) ||
		(strings.Contains(lower, "usage limit") && strings.Contains(lower, "resets")) {
		return strings.TrimSpace(text)
	}
	return ""
}

// Provider executes tasks using the Codex CLI (codex exec --json).
type Provider struct {
	binaryPath string
}

// New creates a new Codex CLI provider.
func New(binaryPath string) *Provider {
	return &Provider{binaryPath: binaryPath}
}

func (p *Provider) Name() string { return "codex" }

func (p *Provider) Execute(ctx context.Context, req provider.Request) (*provider.Result, error) {
	args := BuildArgs(req, req.EventCh != nil)

	cmd := exec.CommandContext(ctx, p.binaryPath, args...)
	cmd.Dir = req.Workdir
	cmd.Env = os.Environ()
	// Kill entire process group on timeout to prevent orphaned child processes.
	setProcessGroup(cmd)
	cmd.WaitDelay = 5 * time.Second

	if req.EventCh != nil {
		return p.executeStreaming(ctx, cmd, req)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	pr := ParseOutput(stdout.Bytes(), stderr.Bytes(), exitCode)
	pr.DurationMs = elapsed.Milliseconds()

	if ctx.Err() == context.DeadlineExceeded {
		pr.IsError = true
		pr.Error = fmt.Sprintf("timed out after %v", req.Timeout)
	} else if ctx.Err() != nil {
		pr.IsError = true
		pr.Error = "cancelled"
	} else if runErr != nil && !pr.IsError {
		pr.IsError = true
		pr.Error = runErr.Error()
	}

	return pr, nil
}

// executeStreaming runs codex exec --json and parses JSONL output in real time.
func (p *Provider) executeStreaming(ctx context.Context, cmd *exec.Cmd, req provider.Request) (*provider.Result, error) {
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	var finalResult *provider.Result
	var outputParts []string

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var ev event
		if err := json.Unmarshal(line, &ev); err != nil {
			if req.EventCh != nil {
				req.EventCh <- provider.Event{
					Type:      provider.EventOutputChunk,
					TaskID:    req.SessionID,
					SessionID: req.SessionID,
					Data:      map[string]any{"chunk": string(line)},
					Timestamp: time.Now().Format(time.RFC3339),
				}
			}
			continue
		}

		switch ev.Type {
		case "agent_message":
			if ev.Content != "" {
				outputParts = append(outputParts, ev.Content)
				if req.EventCh != nil {
					req.EventCh <- provider.Event{
						Type:      provider.EventOutputChunk,
						TaskID:    req.SessionID,
						SessionID: req.SessionID,
						Data: map[string]any{
							"chunk":     ev.Content,
							"chunkType": "text",
						},
						Timestamp: time.Now().Format(time.RFC3339),
					}
				}
			}

		case "exec_command_begin":
			if req.EventCh != nil {
				req.EventCh <- provider.Event{
					Type:      provider.EventToolCall,
					TaskID:    req.SessionID,
					SessionID: req.SessionID,
					Data: map[string]any{
						"name":  "exec_command",
						"id":    ev.Command,
						"input": ev.Command,
					},
					Timestamp: time.Now().Format(time.RFC3339),
				}
			}

		case "exec_command_end":
			if req.EventCh != nil {
				output := ev.Output
				if len(output) > 500 {
					output = output[:500] + "..."
				}
				req.EventCh <- provider.Event{
					Type:      provider.EventToolResult,
					TaskID:    req.SessionID,
					SessionID: req.SessionID,
					Data: map[string]any{
						"toolUseId": ev.Command,
						"name":      "exec_command",
						"content":   output,
					},
					Timestamp: time.Now().Format(time.RFC3339),
				}
			}

		case "turn.completed":
			pr := &provider.Result{
				Output: strings.Join(outputParts, ""),
			}
			if ev.Usage != nil {
				pr.TokensIn = ev.Usage.InputTokens
				pr.TokensOut = ev.Usage.OutputTokens
			}
			pr.CostUSD = 0 // Codex Pro is flat-rate
			finalResult = pr

		case "turn.failed":
			finalResult = &provider.Result{
				Output:  strings.Join(outputParts, ""),
				IsError: true,
				Error:   ev.Error,
			}
		}
	}

	runErr := cmd.Wait()
	elapsed := time.Since(start)

	if finalResult == nil {
		finalResult = &provider.Result{
			Output: strings.Join(outputParts, ""),
		}
		if len(stderr.Bytes()) > 0 {
			finalResult.IsError = true
			errStr := stderr.String()
			if len(errStr) > 500 {
				errStr = errStr[:500]
			}
			finalResult.Error = errStr
		}
	}

	finalResult.DurationMs = elapsed.Milliseconds()

	if ctx.Err() == context.DeadlineExceeded {
		finalResult.IsError = true
		finalResult.Error = fmt.Sprintf("timed out after %v", req.Timeout)
	} else if ctx.Err() != nil {
		finalResult.IsError = true
		finalResult.Error = "cancelled"
	} else if runErr != nil && !finalResult.IsError {
		finalResult.IsError = true
		finalResult.Error = runErr.Error()
	}

	return finalResult, nil
}

// --- Codex JSONL Event Types ---

type event struct {
	Type     string  `json:"type"`
	Content  string  `json:"content,omitempty"`
	Command  string  `json:"command,omitempty"`
	ExitCode *int    `json:"exit_code,omitempty"`
	Output   string  `json:"output,omitempty"`
	Usage    *usage  `json:"usage,omitempty"`
	Error    string  `json:"error,omitempty"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// --- Arg Building ---

// BuildArgs constructs the codex CLI argument list from a Request.
func BuildArgs(req provider.Request, streaming bool) []string {
	args := []string{"exec"}

	if streaming {
		args = append(args, "--json")
	}

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	switch req.PermissionMode {
	case "bypassPermissions":
		args = append(args, "--full-auto")
	case "acceptEdits":
		args = append(args, "--full-auto")
	default:
		args = append(args, "--sandbox", "read-only")
	}

	if req.Workdir != "" {
		args = append(args, "--cd", req.Workdir)
	}

	for _, dir := range req.AddDirs {
		args = append(args, "--add-dir", dir)
	}

	if !req.PersistSession {
		args = append(args, "--ephemeral")
	}

	args = append(args, "--skip-git-repo-check")

	if req.Resume && req.SessionID != "" {
		args = append(args, "resume", req.SessionID)
	} else if req.Prompt != "" {
		if len(req.Prompt) > 200*1024 {
			// Log warning handled by caller if needed; just include it.
		}
		args = append(args, req.Prompt)
	}

	return args
}

// --- Non-streaming Output Parsing ---

// ParseOutput parses the collected output from codex exec --json.
func ParseOutput(stdout, stderr []byte, exitCode int) *provider.Result {
	pr := &provider.Result{}

	var outputParts []string
	lines := bytes.Split(stdout, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev event
		if err := json.Unmarshal(line, &ev); err != nil {
			outputParts = append(outputParts, string(line))
			continue
		}
		switch ev.Type {
		case "agent_message":
			if ev.Content != "" {
				outputParts = append(outputParts, ev.Content)
			}
		case "turn.completed":
			if ev.Usage != nil {
				pr.TokensIn = ev.Usage.InputTokens
				pr.TokensOut = ev.Usage.OutputTokens
			}
		case "turn.failed":
			pr.IsError = true
			pr.Error = ev.Error
		}
	}

	pr.Output = strings.Join(outputParts, "")
	if quotaErr := detectQuotaError(pr.Output); quotaErr != "" {
		pr.IsError = true
		pr.Error = quotaErr
		pr.Output = ""
	}

	if !pr.IsError && exitCode != 0 {
		pr.IsError = true
		errStr := string(stderr)
		if len(errStr) > 500 {
			errStr = errStr[:500]
		}
		if errStr == "" {
			errStr = fmt.Sprintf("codex exited with code %d", exitCode)
		}
		pr.Error = errStr
	}
	if !pr.IsError {
		if quotaErr := detectQuotaError(string(stderr)); quotaErr != "" {
			pr.IsError = true
			pr.Error = quotaErr
			pr.Output = ""
		}
	}

	return pr
}

// --- Codex Quota Status ---

// Quota holds codex usage quota information.
type Quota struct {
	HourlyPct  float64 `json:"hourlyPct"`
	WeeklyPct  float64 `json:"weeklyPct"`
	HourlyText string  `json:"hourlyText"`
	WeeklyText string  `json:"weeklyText"`
	FetchedAt  string  `json:"fetchedAt"`
}

var (
	quotaCache     *Quota
	quotaCacheTime time.Time
	quotaMu        sync.Mutex
)

// FetchQuota runs `codex status` and parses the output for quota info.
// Results are cached for 5 minutes.
func FetchQuota(binaryPath string) (*Quota, error) {
	quotaMu.Lock()
	defer quotaMu.Unlock()

	if quotaCache != nil && time.Since(quotaCacheTime) < 5*time.Minute {
		return quotaCache, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "status")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("codex status: %w", err)
	}

	q := parseStatusOutput(string(out))
	q.FetchedAt = time.Now().Format(time.RFC3339)
	quotaCache = q
	quotaCacheTime = time.Now()
	return q, nil
}

func parseStatusOutput(output string) *Quota {
	q := &Quota{}
	lines := strings.Split(output, "\n")

	pctRe := pctRegexp()
	resetRe := resetRegexp()

	for i, line := range lines {
		lower := strings.ToLower(line)

		if strings.Contains(lower, "5h") || strings.Contains(lower, "hourly") || strings.Contains(lower, "5-hour") {
			if m := pctRe.FindStringSubmatch(line); len(m) > 1 {
				fmt.Sscanf(m[1], "%f", &q.HourlyPct)
			}
			searchText := line
			if i+1 < len(lines) {
				searchText += " " + lines[i+1]
			}
			if m := resetRe.FindStringSubmatch(searchText); len(m) > 1 {
				q.HourlyText = "resets " + strings.TrimSpace(m[1])
			}
		}

		if strings.Contains(lower, "weekly") || strings.Contains(lower, "week") {
			if m := pctRe.FindStringSubmatch(line); len(m) > 1 {
				fmt.Sscanf(m[1], "%f", &q.WeeklyPct)
			}
			searchText := line
			if i+1 < len(lines) {
				searchText += " " + lines[i+1]
			}
			if m := resetRe.FindStringSubmatch(searchText); len(m) > 1 {
				q.WeeklyText = "resets " + strings.TrimSpace(m[1])
			}
		}
	}

	return q
}
