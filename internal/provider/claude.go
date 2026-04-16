package provider

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/log"
)

// ClaudeProvider executes tasks using the Claude CLI.
type ClaudeProvider struct {
	BinaryPath    string
	DockerEnabled bool         // default Docker setting from config
	Docker        DockerRunner // nil if Docker not available
}

func (p *ClaudeProvider) Name() string { return "claude" }

func (p *ClaudeProvider) Execute(ctx context.Context, req Request) (*Result, error) {
	args := BuildClaudeArgs(req, req.OnEvent != nil)

	var cmd *exec.Cmd
	if p.shouldUseDocker(req) {
		cmd = p.Docker.BuildCmd(ctx, p.BinaryPath, req.Workdir, args, req.AddDirs, req.MCPPath)
	} else {
		cmd = exec.CommandContext(ctx, p.BinaryPath, args...)
		cmd.Dir = req.Workdir
		// Filter out Claude Code session env vars so Claude Code doesn't refuse to start
		// when Tetora is invoked from within a Claude Code session.
		rawEnv := os.Environ()
		filteredEnv := make([]string, 0, len(rawEnv))
		for _, e := range rawEnv {
			if !strings.HasPrefix(e, "CLAUDECODE=") &&
				!strings.HasPrefix(e, "CLAUDE_CODE_ENTRYPOINT=") &&
				!strings.HasPrefix(e, "CLAUDE_CODE_TEAM_MODE=") &&
				!strings.HasPrefix(e, "TETORA_SOURCE=") {
				filteredEnv = append(filteredEnv, e)
			}
		}
		cmd.Env = append(filteredEnv, "TETORA_SOURCE=agent_dispatch")
	}

	// Kill entire process group on timeout to prevent orphaned child processes.
	SetProcessGroup(cmd)
	cmd.WaitDelay = 5 * time.Second

	// Pipe prompt via stdin to avoid OS ARG_MAX limits on long prompts.
	if req.Prompt != "" {
		cmd.Stdin = strings.NewReader(req.Prompt)
	}

	// Streaming mode.
	if req.OnEvent != nil {
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

	pr := ParseClaudeOutput(stdout.Bytes(), stderr.Bytes(), exitCode)
	pr.DurationMs = elapsed.Milliseconds()

	// Budget soft-limit.
	if req.Budget > 0 && pr.CostUSD >= req.Budget {
		promptPreview := req.Prompt
		if len(promptPreview) > 120 {
			promptPreview = promptPreview[:120]
		}
		log.Warn("task exceeded budget soft-limit (completed normally)",
			"budget", req.Budget, "spent", pr.CostUSD,
			"model", req.Model, "prompt_preview", promptPreview,
		)
	}

	// Handle timeout/cancellation.
	if ctx.Err() == context.DeadlineExceeded {
		pr.IsError = true
		pr.Error = fmt.Sprintf("timed out after %v", req.Timeout)
		if s := strings.TrimSpace(stderr.String()); s != "" {
			if len(s) > 2000 {
				s = s[:2000]
			}
			log.Warn("claude CLI timed out with stderr",
				"sessionId", req.SessionID, "timeout", req.Timeout, "stderr", s)
		} else {
			log.Warn("claude CLI timed out with no output",
				"sessionId", req.SessionID, "timeout", req.Timeout,
				"stdout_len", stdout.Len(), "exitCode", exitCode)
		}
	} else if ctx.Err() != nil {
		pr.IsError = true
		pr.Error = "cancelled"
	} else if runErr != nil && !pr.IsError {
		pr.IsError = true
		pr.Error = runErr.Error()
	}

	return pr, nil
}

// executeStreaming runs the command and parses stream-json output in real time.
func (p *ClaudeProvider) executeStreaming(ctx context.Context, cmd *exec.Cmd, req Request) (*Result, error) {
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

	var resultMsg *claudeStreamMsg
	toolNameByID := make(map[string]string)
	var nonJSONLines []string // non-JSON output from CLI (e.g. "api 400" error text)
	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	lineCount := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		lineCount++

		var msg claudeStreamMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			lineStr := strings.TrimSpace(string(line))
			if lineStr != "" && len(nonJSONLines) < 10 {
				nonJSONLines = append(nonJSONLines, lineStr)
			}
			if req.OnEvent != nil {
				req.OnEvent(Event{
					Type:      EventOutputChunk,
					TaskID:    req.SessionID,
					SessionID: req.SessionID,
					Data:      map[string]string{"chunk": string(line)},
					Timestamp: time.Now().Format(time.RFC3339),
				})
			}
			continue
		}

		switch msg.Type {
		case "assistant":
			if msg.Message != nil {
				for _, block := range msg.Message.ContentBlocks() {
					switch block.Type {
					case "text":
						if req.OnEvent != nil && block.Text != "" {
							req.OnEvent(Event{
								Type:      EventOutputChunk,
								TaskID:    req.SessionID,
								SessionID: req.SessionID,
								Data: map[string]any{
									"chunk":     block.Text,
									"chunkType": "text",
								},
								Timestamp: time.Now().Format(time.RFC3339),
							})
						}
					case "tool_use":
						if block.ID != "" && block.Name != "" {
							toolNameByID[block.ID] = block.Name
						}
						if req.OnEvent != nil {
							req.OnEvent(Event{
								Type:      EventToolCall,
								TaskID:    req.SessionID,
								SessionID: req.SessionID,
								Data: map[string]any{
									"name":  block.Name,
									"id":    block.ID,
									"input": string(block.Input),
								},
								Timestamp: time.Now().Format(time.RFC3339),
							})
						}
					}
				}
			}
		case "user":
			if msg.Message != nil {
				for _, block := range msg.Message.ContentBlocks() {
					if block.Type == "tool_result" && req.OnEvent != nil {
						contentStr := ""
						if block.Content != nil {
							if s, ok := block.Content.(string); ok {
								contentStr = s
							} else {
								if b, err := json.Marshal(block.Content); err == nil {
									contentStr = string(b)
								}
							}
						}
						if len(contentStr) > 500 {
							contentStr = contentStr[:500] + "..."
						}
						req.OnEvent(Event{
							Type:      EventToolResult,
							TaskID:    req.SessionID,
							SessionID: req.SessionID,
							Data: map[string]any{
								"toolUseId": block.ToolUseID,
								"name":      toolNameByID[block.ToolUseID],
								"content":   contentStr,
							},
							Timestamp: time.Now().Format(time.RFC3339),
						})
					}
				}
			}
		case "result":
			resultMsg = &msg
		}
	}

	remaining, _ := io.ReadAll(stdoutPipe)
	_ = remaining

	runErr := cmd.Wait()
	elapsed := time.Since(start)

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	// Distinguish between CLI silent success and truly no output to aid diagnosis.
	if exitCode == 0 && ctx.Err() == nil && runErr == nil {
		if lineCount == 0 {
			log.Warn("truly_no_output: CLI exited 0 with empty stdout",
				"sessionId", req.SessionID, "exitCode", exitCode)
		} else if resultMsg == nil {
			log.Warn("output_without_result: CLI produced output lines but no result message",
				"sessionId", req.SessionID, "lineCount", lineCount)
		}
	}

	pr := buildResultFromStream(resultMsg, stderr.Bytes(), exitCode)
	pr.DurationMs = elapsed.Milliseconds()

	// Warn when result message is present but carries no tokens and no output.
	if pr.Error == "empty run: CLI returned success but no tokens were consumed" {
		log.Warn("empty_result_event: CLI returned result message with zero tokens and empty output",
			"sessionId", req.SessionID, "exitCode", exitCode)
	}

	if ctx.Err() == context.DeadlineExceeded {
		pr.IsError = true
		pr.Error = fmt.Sprintf("timed out after %v", req.Timeout)
		if s := strings.TrimSpace(stderr.String()); s != "" {
			if len(s) > 2000 {
				s = s[:2000]
			}
			log.Warn("claude CLI streaming timed out with stderr",
				"sessionId", req.SessionID, "timeout", req.Timeout, "stderr", s)
		} else {
			log.Warn("claude CLI streaming timed out with no output",
				"sessionId", req.SessionID, "timeout", req.Timeout, "exitCode", exitCode)
		}
	} else if ctx.Err() != nil {
		pr.IsError = true
		pr.Error = "cancelled"
	} else if runErr != nil && !pr.IsError {
		pr.IsError = true
		pr.Error = runErr.Error()
	}

	// If the error message is generic/unhelpful, try to surface non-JSON CLI output
	// (e.g. "api 400", "Error: rate limit exceeded") that was emitted before the result.
	if pr.IsError && pr.Error == "error_during_execution" && len(nonJSONLines) > 0 {
		cliText := strings.Join(nonJSONLines, " | ")
		if len(cliText) > 300 {
			cliText = cliText[:300]
		}
		pr.Error = cliText
	}

	return pr, nil
}

func buildResultFromStream(resultMsg *claudeStreamMsg, stderr []byte, exitCode int) *Result {
	if resultMsg == nil {
		return ParseClaudeOutput(nil, stderr, exitCode)
	}

	pr := &Result{
		Output:    resultMsg.Result,
		CostUSD:   resultMsg.CostUSD,
		SessionID: resultMsg.SessionID,
		IsError:   resultMsg.IsError,
	}
	if resultMsg.Usage != nil {
		pr.TokensIn = resultMsg.Usage.TotalInputTokens()
		pr.TokensOut = resultMsg.Usage.OutputTokens
	}
	pr.ProviderMs = resultMsg.DurationMs
	if resultMsg.IsError {
		pr.Error = resultMsg.Subtype
		// CLI sometimes emits subtype="success" with is_error=true on quick init failures.
		// Normalise to avoid surfacing "success" as the error message.
		if pr.Error == "" || pr.Error == "success" {
			pr.Error = "error_during_execution"
		}
	}
	if !pr.IsError && pr.TokensIn == 0 && pr.TokensOut == 0 && pr.CostUSD == 0 && strings.TrimSpace(pr.Output) == "" {
		pr.IsError = true
		pr.Error = "empty run: CLI returned success but no tokens were consumed"
	}
	return pr
}

// shouldUseDocker determines if this request should run in a Docker sandbox.
func (p *ClaudeProvider) shouldUseDocker(req Request) bool {
	if p.Docker == nil {
		return false
	}
	if req.Docker != nil {
		return *req.Docker
	}
	return p.DockerEnabled
}

// BuildClaudeArgs constructs the claude CLI argument list from a Request.
func BuildClaudeArgs(req Request, streaming bool) []string {
	outputFormat := "json"
	if streaming {
		outputFormat = "stream-json"
	}
	permMode := cmp.Or(req.PermissionMode, "acceptEdits")
	args := []string{
		"--print",
		"--verbose",
		"--output-format", outputFormat,
		"--model", req.Model,
		"--permission-mode", permMode,
	}
	// bypassPermissions needs --dangerously-skip-permissions to also skip Bash confirmations.
	if permMode == "bypassPermissions" {
		args = append(args, "--dangerously-skip-permissions")
	}

	resume := req.Resume
	if resume && req.SessionID != "" && !ClaudeSessionFileExists(req.SessionID) {
		log.Warn("claude session file not found, falling back to new session", "sessionId", req.SessionID)
		resume = false
	}
	if resume && req.SessionID != "" {
		args = append(args, "--resume", req.SessionID)
	} else {
		args = append(args, "--session-id", req.SessionID)
		if !req.PersistSession {
			args = append(args, "--no-session-persistence")
		}
	}

	for _, dir := range req.AddDirs {
		args = append(args, "--add-dir", dir)
	}

	if req.MCPPath != "" {
		args = append(args, "--mcp-config", req.MCPPath)
	}

	if req.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", req.SystemPrompt)
	}

	return args
}

// ClaudeSessionFileExists checks whether a Claude Code session file exists.
func ClaudeSessionFileExists(sessionID string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	pattern := home + "/.claude/projects/*/" + sessionID + ".jsonl"
	matches, err := filepath.Glob(pattern)
	return err == nil && len(matches) > 0
}

// --- Claude Output Parsing ---

type claudeOutput struct {
	Type       string       `json:"type"`
	Subtype    string       `json:"subtype"`
	Result     string       `json:"result"`
	IsError    bool         `json:"is_error"`
	DurationMs int64        `json:"duration_ms"`
	CostUSD    float64      `json:"total_cost_usd"`
	SessionID  string       `json:"session_id"`
	NumTurns   int          `json:"num_turns"`
	Usage      *claudeUsage `json:"usage,omitempty"`
}

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

func (u *claudeUsage) TotalInputTokens() int {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

type claudeStreamMsg struct {
	Type       string       `json:"type"`
	Subtype    string       `json:"subtype,omitempty"`
	Message    *claudeMsg   `json:"message,omitempty"`
	Result     string       `json:"result,omitempty"`
	IsError    bool         `json:"is_error,omitempty"`
	DurationMs int64        `json:"duration_ms,omitempty"`
	CostUSD    float64      `json:"total_cost_usd,omitempty"`
	SessionID  string       `json:"session_id,omitempty"`
	NumTurns   int          `json:"num_turns,omitempty"`
	Usage      *claudeUsage `json:"usage,omitempty"`
}

type claudeMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (m *claudeMsg) ContentBlocks() []claudeContentBlock {
	if m == nil || len(m.Content) == 0 {
		return nil
	}
	var blocks []claudeContentBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil
	}
	return blocks
}

type claudeContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   any             `json:"content,omitempty"`
}

// ParseClaudeOutput parses Claude CLI JSON output into a Result.
func ParseClaudeOutput(stdout, stderr []byte, exitCode int) *Result {
	var co claudeOutput

	if err := json.Unmarshal(stdout, &co); err == nil && co.Type != "" {
		return buildResultFromParsed(co)
	}

	var msgs []claudeStreamMsg
	if err := json.Unmarshal(stdout, &msgs); err == nil && len(msgs) > 0 {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Type == "result" {
				co = claudeOutput{
					Type:       msgs[i].Type,
					Subtype:    msgs[i].Subtype,
					Result:     msgs[i].Result,
					IsError:    msgs[i].IsError,
					DurationMs: msgs[i].DurationMs,
					CostUSD:    msgs[i].CostUSD,
					SessionID:  msgs[i].SessionID,
					NumTurns:   msgs[i].NumTurns,
					Usage:      msgs[i].Usage,
				}
				return buildResultFromParsed(co)
			}
		}
	}

	// Fallback: treat raw output as text.
	pr := &Result{Output: string(stdout)}
	if exitCode != 0 {
		pr.IsError = true
		errStr := string(stderr)
		if len(errStr) > 500 {
			errStr = errStr[:500]
		}
		pr.Error = errStr
	}
	return pr
}

func buildResultFromParsed(co claudeOutput) *Result {
	r := &Result{
		Output:     co.Result,
		CostUSD:    co.CostUSD,
		SessionID:  co.SessionID,
		ProviderMs: co.DurationMs,
	}
	if co.Usage != nil {
		r.TokensIn = co.Usage.TotalInputTokens()
		r.TokensOut = co.Usage.OutputTokens
	}
	if co.IsError {
		r.IsError = true
		r.Error = co.Subtype
		// CLI sometimes emits subtype="success" with is_error=true on quick init failures.
		// Normalise to avoid surfacing "success" as the error message.
		if r.Error == "" || r.Error == "success" {
			r.Error = "error_during_execution"
		}
	} else if r.TokensIn == 0 && r.TokensOut == 0 && co.CostUSD == 0 && strings.TrimSpace(co.Result) == "" {
		r.IsError = true
		r.Error = "empty run: CLI returned success but no tokens were consumed"
	}
	return r
}
