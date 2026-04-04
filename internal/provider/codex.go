package provider

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
	"syscall"
	"time"

	"tetora/internal/log"
)

// CodexProvider executes tasks using the Codex CLI (codex exec --json).
type CodexProvider struct {
	BinaryPath string
}

func (p *CodexProvider) Name() string { return "codex" }

func (p *CodexProvider) Execute(ctx context.Context, req Request) (*Result, error) {
	args := BuildCodexArgs(req, req.OnEvent != nil)

	cmd := exec.CommandContext(ctx, p.BinaryPath, args...)
	cmd.Dir = req.Workdir
	cmd.Env = os.Environ()
	// Close stdin so codex doesn't hang on "Reading additional input from stdin...".
	if devNull, err := os.Open(os.DevNull); err == nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	}
	// Kill entire process group on timeout to prevent orphaned child processes.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return os.ErrProcessDone
	}
	cmd.WaitDelay = 5 * time.Second

	if req.OnEvent != nil {
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

	pr := ParseCodexOutput(stdout.Bytes(), stderr.Bytes(), exitCode)
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

func (p *CodexProvider) executeStreaming(ctx context.Context, cmd *exec.Cmd, req Request) (*Result, error) {
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

	var finalResult *Result
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
			if req.OnEvent != nil {
				req.OnEvent(Event{
					Type:      EventOutputChunk,
					TaskID:    req.SessionID,
					SessionID: req.SessionID,
					Data:      map[string]any{"chunk": string(line)},
					Timestamp: time.Now().Format(time.RFC3339),
				})
			}
			continue
		}

		if ev.isAgentMessage() {
			if text := ev.agentText(); text != "" {
				outputParts = append(outputParts, text)
				if req.OnEvent != nil {
					req.OnEvent(Event{
						Type:      EventOutputChunk,
						TaskID:    req.SessionID,
						SessionID: req.SessionID,
						Data: map[string]any{
							"chunk":     text,
							"chunkType": "text",
						},
						Timestamp: time.Now().Format(time.RFC3339),
					})
				}
			}
		} else if ev.isCommandBegin() {
			if req.OnEvent != nil {
				req.OnEvent(Event{
					Type:      EventToolCall,
					TaskID:    req.SessionID,
					SessionID: req.SessionID,
					Data: map[string]any{
						"name":  "exec_command",
						"id":    ev.commandName(),
						"input": ev.commandName(),
					},
					Timestamp: time.Now().Format(time.RFC3339),
				})
			}
		} else if ev.isCommandEnd() {
			if req.OnEvent != nil {
				output := ev.commandOutput()
				if len(output) > 500 {
					output = output[:500] + "..."
				}
				req.OnEvent(Event{
					Type:      EventToolResult,
					TaskID:    req.SessionID,
					SessionID: req.SessionID,
					Data: map[string]any{
						"toolUseId": ev.commandName(),
						"name":      "exec_command",
						"content":   output,
					},
					Timestamp: time.Now().Format(time.RFC3339),
				})
			}
		} else if ev.Type == "turn.completed" {
			pr := &Result{
				Output: strings.Join(outputParts, ""),
			}
			if ev.Usage != nil {
				pr.TokensIn = ev.Usage.InputTokens
				pr.TokensOut = ev.Usage.OutputTokens
			}
			pr.CostUSD = 0
			finalResult = pr
		} else if ev.Type == "turn.failed" {
			finalResult = &Result{
				Output:  strings.Join(outputParts, ""),
				IsError: true,
				Error:   ev.Error,
			}
		}
	}

	runErr := cmd.Wait()
	elapsed := time.Since(start)

	if finalResult == nil {
		finalResult = &Result{
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

// codexEvent handles both legacy (pre-v0.118) and new (v0.118+) JSONL formats.
// Legacy: {"type":"agent_message","content":"..."}
// New:    {"type":"item.completed","item":{"type":"agent_message","text":"..."}}
type codexEvent struct {
	Type  string     `json:"type"`
	Item  *codexItem `json:"item,omitempty"`
	Usage *codexUsage `json:"usage,omitempty"`
	Error string     `json:"error,omitempty"`
	// Legacy flat fields.
	Content string `json:"content,omitempty"`
	Command string `json:"command,omitempty"`
	Output  string `json:"output,omitempty"`
}

type codexItem struct {
	ID     string `json:"id,omitempty"`
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	Cmd    string `json:"command,omitempty"`
	Output string `json:"aggregated_output,omitempty"`
}

type codexUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (ev *codexEvent) agentText() string {
	if ev.Item != nil && ev.Item.Type == "agent_message" {
		return ev.Item.Text
	}
	return ev.Content
}

func (ev *codexEvent) isAgentMessage() bool {
	if ev.Type == "agent_message" {
		return true
	}
	return (ev.Type == "item.completed") && ev.Item != nil && ev.Item.Type == "agent_message"
}

func (ev *codexEvent) isCommandBegin() bool {
	if ev.Type == "exec_command_begin" {
		return true
	}
	return ev.Type == "item.started" && ev.Item != nil && ev.Item.Type == "command_execution"
}

func (ev *codexEvent) isCommandEnd() bool {
	if ev.Type == "exec_command_end" {
		return true
	}
	return ev.Type == "item.completed" && ev.Item != nil && ev.Item.Type == "command_execution"
}

func (ev *codexEvent) commandName() string {
	if ev.Item != nil {
		return ev.Item.Cmd
	}
	return ev.Command
}

func (ev *codexEvent) commandOutput() string {
	if ev.Item != nil {
		return ev.Item.Output
	}
	return ev.Output
}

// BuildCodexArgs constructs the codex CLI argument list.
func BuildCodexArgs(req Request, streaming bool) []string {
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
			log.Warn("codex prompt exceeds 200KB, may cause issues", "len", len(req.Prompt))
		}
		args = append(args, req.Prompt)
	}

	return args
}

// ParseCodexOutput parses the collected output from codex exec --json.
func ParseCodexOutput(stdout, stderr []byte, exitCode int) *Result {
	pr := &Result{}

	var outputParts []string
	lines := bytes.Split(stdout, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev codexEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			outputParts = append(outputParts, string(line))
			continue
		}
		if ev.isAgentMessage() {
			if text := ev.agentText(); text != "" {
				outputParts = append(outputParts, text)
			}
		} else if ev.Type == "turn.completed" {
			if ev.Usage != nil {
				pr.TokensIn = ev.Usage.InputTokens
				pr.TokensOut = ev.Usage.OutputTokens
			}
		} else if ev.Type == "turn.failed" {
			pr.IsError = true
			pr.Error = ev.Error
		}
	}

	pr.Output = strings.Join(outputParts, "")
	if quotaErr := detectCodexQuotaError(pr.Output); quotaErr != "" {
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
		if quotaErr := detectCodexQuotaError(string(stderr)); quotaErr != "" {
			pr.IsError = true
			pr.Error = quotaErr
			pr.Output = ""
		}
	}

	return pr
}

// --- Codex Quota Status ---

// CodexQuota holds Codex quota information.
type CodexQuota struct {
	HourlyPct  float64 `json:"hourlyPct"`
	WeeklyPct  float64 `json:"weeklyPct"`
	HourlyText string  `json:"hourlyText"`
	WeeklyText string  `json:"weeklyText"`
	FetchedAt  string  `json:"fetchedAt"`
}

var (
	codexQuotaCache     *CodexQuota
	codexQuotaCacheTime time.Time
	codexQuotaMu        sync.Mutex
)

// FetchCodexQuota runs `codex status` and parses the output for quota info.
func FetchCodexQuota(binaryPath string) (*CodexQuota, error) {
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

	q := ParseCodexStatusOutput(string(out))
	q.FetchedAt = time.Now().Format(time.RFC3339)
	codexQuotaCache = q
	codexQuotaCacheTime = time.Now()
	return q, nil
}

var (
	codexPctRe   = regexp.MustCompile(`(\d+(?:\.\d+)?)%`)
	codexResetRe = regexp.MustCompile(`resets?\s+(.+?)(?:\)|$)`)
)

func detectCodexQuotaError(text string) string {
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

// ParseCodexStatusOutput parses codex status command output.
func ParseCodexStatusOutput(output string) *CodexQuota {
	q := &CodexQuota{}
	lines := strings.Split(output, "\n")

	for i, line := range lines {
		lower := strings.ToLower(line)

		if strings.Contains(lower, "5h") || strings.Contains(lower, "hourly") || strings.Contains(lower, "5-hour") {
			if m := codexPctRe.FindStringSubmatch(line); len(m) > 1 {
				fmt.Sscanf(m[1], "%f", &q.HourlyPct)
			}
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
