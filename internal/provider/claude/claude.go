// Package claude implements the ClaudeProvider: executes tasks using the Claude CLI.
package claude

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
	"syscall"
	"time"

	"tetora/internal/log"
	"tetora/internal/provider"
)

// DockerCmdBuilder constructs a Docker-wrapped exec.Cmd.
// It is injected at construction time to avoid importing package main from this package.
type DockerCmdBuilder func(
	ctx context.Context,
	workdir string,
	binaryPath string,
	args []string,
	addDirs []string,
	mcpPath string,
	envVars []string,
) *exec.Cmd

// EnvFilter returns filtered KEY=VALUE env pairs for the Docker container.
type EnvFilter func() []string

// Provider executes tasks using the Claude CLI.
type Provider struct {
	binaryPath    string
	dockerEnabled bool
	buildDockerCmd DockerCmdBuilder
	envFilter      EnvFilter
}

// New creates a new Claude CLI provider.
// Pass dockerEnabled=false and nil builders when Docker is not used.
func New(binaryPath string, dockerEnabled bool, buildDockerCmd DockerCmdBuilder, envFilter EnvFilter) *Provider {
	return &Provider{
		binaryPath:     binaryPath,
		dockerEnabled:  dockerEnabled,
		buildDockerCmd: buildDockerCmd,
		envFilter:      envFilter,
	}
}

func (p *Provider) Name() string { return "claude" }

// setProcessGroup puts cmd into its own process group and configures
// Cancel to kill the entire group (including child processes) when the
// context expires.  This prevents orphaned claude/node processes from
// running indefinitely after a timeout.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			// Kill the entire process group (negative PID).
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return os.ErrProcessDone
	}
	cmd.WaitDelay = 5 * time.Second
}

func (p *Provider) Execute(ctx context.Context, req provider.Request) (*provider.Result, error) {
	pr, err := p.executeOnce(ctx, req)
	if err != nil {
		return nil, err
	}

	// Retry once without --resume if the session file is stale (e.g. copied from
	// another machine with a different project path). The existing sessionFileExists
	// check in BuildArgs handles missing files; this handles files that exist but
	// belong to the wrong project directory.
	if req.Resume && isStaleSessionError(pr) {
		log.Warn("session resume failed with error_during_execution, retrying with new session",
			"sessionId", req.SessionID, "error", pr.Error)
		req.Resume = false
		pr, err = p.executeOnce(ctx, req)
		if err != nil {
			return nil, err
		}
	}

	return pr, nil
}

func (p *Provider) executeOnce(ctx context.Context, req provider.Request) (*provider.Result, error) {
	args := BuildArgs(req, req.EventCh != nil)

	var cmd *exec.Cmd
	if p.shouldUseDocker(req) && p.buildDockerCmd != nil {
		dockerArgs := rewriteDockerArgs(args, req.AddDirs, req.MCPPath)
		var envVars []string
		if p.envFilter != nil {
			envVars = p.envFilter()
		}
		envVars = append(envVars, "TETORA_SOURCE=agent_dispatch")
		cmd = p.buildDockerCmd(ctx, req.Workdir, p.binaryPath, dockerArgs, req.AddDirs, req.MCPPath, envVars)
	} else {
		cmd = exec.CommandContext(ctx, p.binaryPath, args...)
		cmd.Dir = req.Workdir
		// Filter out Claude Code session env vars so Claude Code doesn't refuse to start
		// when Tetora is invoked from within a Claude Code session.
		// Also filter TETORA_SOURCE to avoid duplicate values in nested dispatches.
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
	setProcessGroup(cmd)

	// Pipe prompt via stdin to avoid OS ARG_MAX limits on long prompts.
	if req.Prompt != "" {
		cmd.Stdin = strings.NewReader(req.Prompt)
	}

	// Streaming mode: pipe stdout line-by-line, emitting SSE events.
	if req.EventCh != nil {
		return p.executeStreaming(ctx, cmd, req)
	}

	// Non-streaming mode: collect all output then parse.
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

	result := ParseOutput(stdout.Bytes(), stderr.Bytes(), exitCode)

	pr := &provider.Result{
		Output:     result.Output,
		CostUSD:    result.CostUSD,
		DurationMs: elapsed.Milliseconds(),
		SessionID:  result.SessionID,
		IsError:    result.Status == "error",
		Error:      result.Error,
		TokensIn:   result.TokensIn,
		TokensOut:  result.TokensOut,
		ProviderMs: result.ProviderMs,
	}

	// Budget soft-limit: log when cost exceeds per-task budget without stopping.
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
	} else if runErr != nil {
		if !pr.IsError {
			pr.IsError = true
			pr.Error = runErr.Error()
		} else if pr.Error == "" {
			pr.Error = runErr.Error()
		}
	}

	// Final guard: never return IsError with an empty Error string.
	if pr.IsError && pr.Error == "" {
		pr.Error = fmt.Sprintf("unknown error (exit=%d, stdout=%d bytes, stderr=%d bytes)",
			exitCode, stdout.Len(), stderr.Len())
	}

	return pr, nil
}

// isStaleSessionError returns true when a resume attempt failed because the
// session file is stale or belongs to a different project path. This is
// detected by: is_error with subtype "error_during_execution" and zero tokens
// consumed (meaning Claude never actually started processing).
func isStaleSessionError(pr *provider.Result) bool {
	return pr != nil &&
		pr.IsError &&
		pr.Error == "error_during_execution" &&
		pr.TokensIn == 0 && pr.TokensOut == 0
}

// executeStreaming runs the command and parses stream-json output in real time.
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

	var resultMsg *streamMsg
	toolNameByID := make(map[string]string)
	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	lineCount := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		lineCount++

		var msg streamMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			if req.EventCh != nil {
				req.EventCh <- provider.Event{
					Type:      provider.EventOutputChunk,
					TaskID:    req.SessionID,
					SessionID: req.SessionID,
					Data:      map[string]string{"chunk": string(line)},
					Timestamp: time.Now().Format(time.RFC3339),
				}
			}
			continue
		}

		switch msg.Type {
		case "assistant":
			if msg.Message != nil {
				for _, block := range msg.Message.contentBlocks() {
					switch block.Type {
					case "text":
						if req.EventCh != nil && block.Text != "" {
							req.EventCh <- provider.Event{
								Type:      provider.EventOutputChunk,
								TaskID:    req.SessionID,
								SessionID: req.SessionID,
								Data: map[string]any{
									"chunk":     block.Text,
									"chunkType": "text",
								},
								Timestamp: time.Now().Format(time.RFC3339),
							}
						}
					case "tool_use":
						if block.ID != "" && block.Name != "" {
							toolNameByID[block.ID] = block.Name
						}
						if req.EventCh != nil {
							req.EventCh <- provider.Event{
								Type:      provider.EventToolCall,
								TaskID:    req.SessionID,
								SessionID: req.SessionID,
								Data: map[string]any{
									"name":  block.Name,
									"id":    block.ID,
									"input": string(block.Input),
								},
								Timestamp: time.Now().Format(time.RFC3339),
							}
						}
					}
				}
			}
		case "user":
			if msg.Message != nil {
				for _, block := range msg.Message.contentBlocks() {
					if block.Type == "tool_result" && req.EventCh != nil {
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
						req.EventCh <- provider.Event{
							Type:      provider.EventToolResult,
							TaskID:    req.SessionID,
							SessionID: req.SessionID,
							Data: map[string]any{
								"toolUseId": block.ToolUseID,
								"name":      toolNameByID[block.ToolUseID],
								"content":   contentStr,
							},
							Timestamp: time.Now().Format(time.RFC3339),
						}
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
	} else if runErr != nil {
		if !pr.IsError {
			pr.IsError = true
			pr.Error = runErr.Error()
		} else if pr.Error == "" {
			pr.Error = runErr.Error()
		}
	}

	if pr.IsError && pr.Error == "" {
		pr.Error = fmt.Sprintf("unknown streaming error (exit=%d)", exitCode)
	}

	return pr, nil
}

func buildResultFromStream(resultMsg *streamMsg, stderr []byte, exitCode int) *provider.Result {
	if resultMsg == nil {
		result := ParseOutput(nil, stderr, exitCode)
		return &provider.Result{
			Output:  result.Output,
			CostUSD: result.CostUSD,
			IsError: result.Status == "error",
			Error:   result.Error,
		}
	}

	pr := &provider.Result{
		Output:    resultMsg.Result,
		CostUSD:   resultMsg.CostUSD,
		SessionID: resultMsg.SessionID,
		IsError:   resultMsg.IsError,
	}
	if resultMsg.Usage != nil {
		pr.TokensIn = resultMsg.Usage.totalInputTokens()
		pr.TokensOut = resultMsg.Usage.OutputTokens
	}
	pr.ProviderMs = resultMsg.DurationMs
	if resultMsg.IsError {
		pr.Error = resultMsg.Subtype
	}
	if !pr.IsError && pr.TokensIn == 0 && pr.TokensOut == 0 && pr.CostUSD == 0 && strings.TrimSpace(pr.Output) == "" {
		pr.IsError = true
		pr.Error = "empty run: CLI returned success but no tokens were consumed"
	}
	return pr
}

// shouldUseDocker determines if this request should run in a Docker sandbox.
func (p *Provider) shouldUseDocker(req provider.Request) bool {
	if req.Docker != nil {
		return *req.Docker
	}
	return p.dockerEnabled
}

// BuildArgs constructs the claude CLI argument list from a Request.
func BuildArgs(req provider.Request, streaming bool) []string {
	outputFormat := "json"
	if streaming {
		outputFormat = "stream-json"
	}
	args := []string{
		"--print",
		"--verbose",
		"--output-format", outputFormat,
		"--model", req.Model,
		"--permission-mode", cmp.Or(req.PermissionMode, "acceptEdits"),
	}

	resume := req.Resume
	if resume && req.SessionID != "" && !sessionFileExists(req.SessionID) {
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

	if len(req.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(req.AllowedTools, ","))
	}

	if req.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", req.SystemPrompt)
	}

	return args
}

// sessionFileExists checks whether a Claude Code session file (.jsonl) exists.
func sessionFileExists(sessionID string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	pattern := home + "/.claude/projects/*/" + sessionID + ".jsonl"
	matches, err := filepath.Glob(pattern)
	return err == nil && len(matches) > 0
}

// rewriteDockerArgs adjusts claude CLI arguments for Docker context.
// - Rewrites --add-dir paths to /mnt/<basename>
// - Rewrites --mcp-config path to /tmp/mcp.json
func rewriteDockerArgs(claudeArgs []string, addDirs []string, mcpPath string) []string {
	rewritten := make([]string, len(claudeArgs))
	copy(rewritten, claudeArgs)

	// Build mapping of host addDir → container path.
	dirMap := make(map[string]string)
	for _, dir := range addDirs {
		base := filepath.Base(dir)
		dirMap[dir] = "/mnt/" + base
	}

	for i := 0; i < len(rewritten); i++ {
		if rewritten[i] == "--add-dir" && i+1 < len(rewritten) {
			if mapped, ok := dirMap[rewritten[i+1]]; ok {
				rewritten[i+1] = mapped
			}
		}
		if rewritten[i] == "--mcp-config" && i+1 < len(rewritten) && mcpPath != "" {
			rewritten[i+1] = "/tmp/mcp.json"
		}
	}

	return rewritten
}

// --- Claude Output Parsing ---

// output is the JSON from `claude --print --output-format json`.
type output struct {
	Type       string `json:"type"`
	Subtype    string `json:"subtype"`
	Result     string `json:"result"`
	IsError    bool   `json:"is_error"`
	DurationMs int64  `json:"duration_ms"`
	CostUSD    float64 `json:"total_cost_usd"`
	SessionID  string  `json:"session_id"`
	NumTurns   int     `json:"num_turns"`
	Usage      *usage  `json:"usage,omitempty"`
}

type usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

func (u *usage) totalInputTokens() int {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

type streamMsg struct {
	Type       string   `json:"type"`
	Subtype    string   `json:"subtype,omitempty"`
	Message    *msg     `json:"message,omitempty"`
	Result     string   `json:"result,omitempty"`
	IsError    bool     `json:"is_error,omitempty"`
	DurationMs int64    `json:"duration_ms,omitempty"`
	CostUSD    float64  `json:"total_cost_usd,omitempty"`
	SessionID  string   `json:"session_id,omitempty"`
	NumTurns   int      `json:"num_turns,omitempty"`
	Usage      *usage   `json:"usage,omitempty"`
}

type msg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (m *msg) contentBlocks() []contentBlock {
	if m == nil || len(m.Content) == 0 {
		return nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil
	}
	return blocks
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   any             `json:"content,omitempty"`
}

// ParsedResult holds the intermediate result from parsing claude output.
type ParsedResult struct {
	Output     string
	CostUSD    float64
	SessionID  string
	Status     string
	Error      string
	TokensIn   int
	TokensOut  int
	ProviderMs int64
	ExitCode   int
}

// ParseOutput parses Claude CLI JSON output into a ParsedResult.
func ParseOutput(stdout, stderr []byte, exitCode int) ParsedResult {
	var co output
	result := ParsedResult{ExitCode: exitCode}

	if err := json.Unmarshal(stdout, &co); err == nil && co.Type != "" {
		return buildFromParsed(co, result)
	}

	var msgs []streamMsg
	if err := json.Unmarshal(stdout, &msgs); err == nil && len(msgs) > 0 {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Type == "result" {
				co = output{
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
				return buildFromParsed(co, result)
			}
		}
	}

	result.Output = string(stdout)
	if exitCode != 0 {
		result.Status = "error"
		errStr := string(stderr)
		if len(errStr) > 500 {
			errStr = errStr[:500]
		}
		result.Error = errStr
	} else {
		result.Status = "success"
	}
	return result
}

func buildFromParsed(co output, result ParsedResult) ParsedResult {
	result.Output = co.Result
	result.CostUSD = co.CostUSD
	result.SessionID = co.SessionID
	result.ProviderMs = co.DurationMs
	if co.Usage != nil {
		result.TokensIn = co.Usage.totalInputTokens()
		result.TokensOut = co.Usage.OutputTokens
	}
	if co.IsError && co.Subtype != "success" {
		result.Status = "error"
		result.Error = co.Subtype
		if result.Error == "" {
			result.Error = "CLI error (no subtype)"
			if co.Result != "" {
				// Use first 200 chars of result as error context.
				errCtx := co.Result
				if len(errCtx) > 200 {
					errCtx = errCtx[:200]
				}
				result.Error = "CLI error: " + errCtx
			}
		}
	} else if result.TokensIn == 0 && result.TokensOut == 0 && co.CostUSD == 0 && strings.TrimSpace(co.Result) == "" {
		result.Status = "error"
		result.Error = "empty run: CLI returned success but no tokens were consumed"
	} else {
		result.Status = "success"
	}
	return result
}
