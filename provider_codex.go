package main

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
)

// CodexProvider executes tasks using the Codex CLI (codex exec --json).
type CodexProvider struct {
	binaryPath string
	cfg        *Config
}

func (p *CodexProvider) Name() string { return "codex" }

func (p *CodexProvider) Execute(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	args := buildCodexArgs(req, req.EventCh != nil)

	cmd := exec.CommandContext(ctx, p.binaryPath, args...)
	cmd.Dir = req.Workdir
	cmd.Env = os.Environ()

	// Codex exec takes prompt as positional arg, not stdin.
	// The prompt is already included in args via buildCodexArgs.

	if req.EventCh != nil {
		return p.executeStreaming(ctx, cmd, req)
	}

	// Non-streaming mode.
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

	pr := parseCodexOutput(stdout.Bytes(), stderr.Bytes(), exitCode)
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
func (p *CodexProvider) executeStreaming(ctx context.Context, cmd *exec.Cmd, req ProviderRequest) (*ProviderResult, error) {
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

	var finalResult *ProviderResult
	var outputParts []string

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var ev codexEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// Non-JSON line — emit as raw chunk.
			if req.EventCh != nil {
				req.EventCh <- SSEEvent{
					Type:      SSEOutputChunk,
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
					req.EventCh <- SSEEvent{
						Type:      SSEOutputChunk,
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
				req.EventCh <- SSEEvent{
					Type:      SSEToolCall,
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
				req.EventCh <- SSEEvent{
					Type:      SSEToolResult,
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
			pr := &ProviderResult{
				Output: strings.Join(outputParts, ""),
			}
			if ev.Usage != nil {
				pr.TokensIn = ev.Usage.InputTokens
				pr.TokensOut = ev.Usage.OutputTokens
			}
			// Codex Pro is $200/month flat — no per-token cost.
			pr.CostUSD = 0
			finalResult = pr

		case "turn.failed":
			finalResult = &ProviderResult{
				Output:  strings.Join(outputParts, ""),
				IsError: true,
				Error:   ev.Error,
			}
		}
	}

	runErr := cmd.Wait()
	elapsed := time.Since(start)

	if finalResult == nil {
		finalResult = &ProviderResult{
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

type codexEvent struct {
	Type     string      `json:"type"`
	Content  string      `json:"content,omitempty"`
	Command  string      `json:"command,omitempty"`
	ExitCode *int        `json:"exit_code,omitempty"`
	Output   string      `json:"output,omitempty"`
	Usage    *codexUsage `json:"usage,omitempty"`
	Error    string      `json:"error,omitempty"`
}

type codexUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// --- Arg Building ---

// buildCodexArgs constructs the codex CLI argument list from a ProviderRequest.
func buildCodexArgs(req ProviderRequest, streaming bool) []string {
	args := []string{"exec"}

	if streaming {
		args = append(args, "--json")
	}

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	// Permission mode mapping.
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

	// Session handling.
	if req.Resume && req.SessionID != "" {
		args = append(args, "resume", req.SessionID)
	} else if req.Prompt != "" {
		// Codex exec takes prompt as positional arg.
		if len(req.Prompt) > 200*1024 {
			logWarn("codex prompt exceeds 200KB, may cause issues", "len", len(req.Prompt))
		}
		args = append(args, req.Prompt)
	}

	return args
}

// --- Non-streaming Output Parsing ---

// parseCodexOutput parses the collected output from codex exec --json.
func parseCodexOutput(stdout, stderr []byte, exitCode int) *ProviderResult {
	pr := &ProviderResult{}

	// Try to parse JSONL lines and find the final state.
	var outputParts []string
	lines := bytes.Split(stdout, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev codexEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// Not JSON — accumulate as raw output.
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

	return pr
}

// --- Codex Quota Status ---

type codexQuota struct {
	HourlyPct  float64 `json:"hourlyPct"`
	WeeklyPct  float64 `json:"weeklyPct"`
	HourlyText string  `json:"hourlyText"`
	WeeklyText string  `json:"weeklyText"`
	FetchedAt  string  `json:"fetchedAt"`
}

var (
	codexQuotaCache     *codexQuota
	codexQuotaCacheTime time.Time
	codexQuotaMu        sync.Mutex
)

// fetchCodexQuota runs `codex status` and parses the output for quota info.
// Results are cached for 5 minutes.
func fetchCodexQuota(binaryPath string) (*codexQuota, error) {
	codexQuotaMu.Lock()
	defer codexQuotaMu.Unlock()

	if codexQuotaCache != nil && time.Since(codexQuotaCacheTime) < 5*time.Minute {
		return codexQuotaCache, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "status")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("codex status: %w", err)
	}

	q := parseCodexStatusOutput(string(out))
	q.FetchedAt = time.Now().Format(time.RFC3339)
	codexQuotaCache = q
	codexQuotaCacheTime = time.Now()
	return q, nil
}

var (
	// Match patterns like: "5h limit:  ████████████ 85% ..." or percentage patterns.
	codexPctRe   = regexp.MustCompile(`(\d+(?:\.\d+)?)%`)
	codexResetRe = regexp.MustCompile(`resets?\s+(.+?)(?:\)|$)`)
)

func parseCodexStatusOutput(output string) *codexQuota {
	q := &codexQuota{}
	lines := strings.Split(output, "\n")

	for i, line := range lines {
		lower := strings.ToLower(line)

		if strings.Contains(lower, "5h") || strings.Contains(lower, "hourly") || strings.Contains(lower, "5-hour") {
			if m := codexPctRe.FindStringSubmatch(line); len(m) > 1 {
				fmt.Sscanf(m[1], "%f", &q.HourlyPct)
			}
			// Check this line and the next for reset text.
			searchText := line
			if i+1 < len(lines) {
				searchText += " " + lines[i+1]
			}
			if m := codexResetRe.FindStringSubmatch(searchText); len(m) > 1 {
				q.HourlyText = "resets " + strings.TrimSpace(m[1])
			}
		}

		if strings.Contains(lower, "weekly") || strings.Contains(lower, "week") {
			if m := codexPctRe.FindStringSubmatch(line); len(m) > 1 {
				fmt.Sscanf(m[1], "%f", &q.WeeklyPct)
			}
			searchText := line
			if i+1 < len(lines) {
				searchText += " " + lines[i+1]
			}
			if m := codexResetRe.FindStringSubmatch(searchText); len(m) > 1 {
				q.WeeklyText = "resets " + strings.TrimSpace(m[1])
			}
		}
	}

	return q
}
