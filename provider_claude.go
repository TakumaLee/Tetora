package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ClaudeProvider executes tasks using the Claude CLI.
type ClaudeProvider struct {
	binaryPath string
	cfg        *Config
}

func (p *ClaudeProvider) Name() string { return "claude" }

func (p *ClaudeProvider) Execute(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	args := buildClaudeArgs(req, req.EventCh != nil)

	var cmd *exec.Cmd
	if p.shouldUseDocker(req) {
		// Rewrite args for Docker context (path remapping).
		dockerArgs := rewriteDockerArgs(args, req.AddDirs, req.MCPPath)
		envVars := dockerEnvFilter(p.cfg.Docker)
		cmd = buildDockerCmd(ctx, p.cfg.Docker, req.Workdir, p.binaryPath, dockerArgs, req.AddDirs, req.MCPPath, envVars)
	} else {
		cmd = exec.CommandContext(ctx, p.binaryPath, args...)
		cmd.Dir = req.Workdir
		// Filter out Claude Code session env vars so Claude Code doesn't refuse to start
		// when Tetora is invoked from within a Claude Code session. Claude Code checks
		// CLAUDECODE, CLAUDE_CODE_ENTRYPOINT, and CLAUDE_CODE_TEAM_MODE.
		rawEnv := os.Environ()
		filteredEnv := make([]string, 0, len(rawEnv))
		for _, e := range rawEnv {
			if !strings.HasPrefix(e, "CLAUDECODE=") &&
				!strings.HasPrefix(e, "CLAUDE_CODE_ENTRYPOINT=") &&
				!strings.HasPrefix(e, "CLAUDE_CODE_TEAM_MODE=") {
				filteredEnv = append(filteredEnv, e)
			}
		}
		cmd.Env = filteredEnv
	}

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

	result := parseClaudeOutput(stdout.Bytes(), stderr.Bytes(), exitCode)

	pr := &ProviderResult{
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

	// Handle timeout/cancellation.
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

// executeStreaming runs the command and parses stream-json output in real time.
// Each line of stdout is a JSON object. Typed SSE events are emitted for
// assistant text, tool_use, and tool_result blocks. The final "result" message
// is used to build the ProviderResult.
func (p *ClaudeProvider) executeStreaming(ctx context.Context, cmd *exec.Cmd, req ProviderRequest) (*ProviderResult, error) {
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

	// Read stream-json lines: each line is a JSON object.
	var resultMsg *claudeStreamMsg
	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024) // 1MB max line
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var msg claudeStreamMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			// JSON parse failure -- emit raw chunk (backward compat).
			if req.EventCh != nil {
				req.EventCh <- SSEEvent{
					Type:      SSEOutputChunk,
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
				for _, block := range msg.Message.Content {
					switch block.Type {
					case "text":
						if req.EventCh != nil && block.Text != "" {
							req.EventCh <- SSEEvent{
								Type:      SSEOutputChunk,
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
						if req.EventCh != nil {
							req.EventCh <- SSEEvent{
								Type:      SSEToolCall,
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
				for _, block := range msg.Message.Content {
					if block.Type == "tool_result" && req.EventCh != nil {
						// Truncate tool result content for SSE.
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
						req.EventCh <- SSEEvent{
							Type:      SSEToolResult,
							TaskID:    req.SessionID,
							SessionID: req.SessionID,
							Data: map[string]any{
								"toolUseId": block.ToolUseID,
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

	// Drain any remaining data from pipe.
	remaining, _ := io.ReadAll(stdoutPipe)
	_ = remaining // already parsed line by line

	runErr := cmd.Wait()
	elapsed := time.Since(start)

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	pr := buildResultFromStream(resultMsg, stderr.Bytes(), exitCode)
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

// buildResultFromStream extracts ProviderResult from the final stream-json "result" message.
// Falls back to parseClaudeOutput if no result message was captured.
func buildResultFromStream(resultMsg *claudeStreamMsg, stderr []byte, exitCode int) *ProviderResult {
	if resultMsg == nil {
		// Fallback: no result line found (shouldn't happen normally).
		result := parseClaudeOutput(nil, stderr, exitCode)
		return &ProviderResult{
			Output:  result.Output,
			CostUSD: result.CostUSD,
			IsError: result.Status == "error",
			Error:   result.Error,
		}
	}

	pr := &ProviderResult{
		Output:    resultMsg.Result,
		CostUSD:   resultMsg.CostUSD,
		SessionID: resultMsg.SessionID,
		IsError:   resultMsg.IsError,
	}
	if resultMsg.Usage != nil {
		pr.TokensIn = resultMsg.Usage.InputTokens
		pr.TokensOut = resultMsg.Usage.OutputTokens
	}
	pr.ProviderMs = resultMsg.DurationMs
	if resultMsg.IsError {
		pr.Error = resultMsg.Subtype
	}
	return pr
}

// shouldUseDocker determines if this request should run in a Docker sandbox.
// Chain: req.Docker (task override) → config.Docker.Enabled → false.
func (p *ClaudeProvider) shouldUseDocker(req ProviderRequest) bool {
	if req.Docker != nil {
		return *req.Docker
	}
	return p.cfg.Docker.Enabled
}

// buildClaudeArgs constructs the claude CLI argument list from a ProviderRequest.
func buildClaudeArgs(req ProviderRequest, streaming bool) []string {
	outputFormat := "json"
	if streaming {
		outputFormat = "stream-json"
	}
	args := []string{
		"--print",
		"--verbose",
		"--output-format", outputFormat,
		"--model", req.Model,
		"--session-id", req.SessionID,
		"--permission-mode", req.PermissionMode,
		"--no-session-persistence",
	}

	if req.Budget > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", req.Budget))
	}

	for _, dir := range req.AddDirs {
		args = append(args, "--add-dir", dir)
	}

	// MCP injection via temp config file.
	if req.MCPPath != "" {
		args = append(args, "--mcp-config", req.MCPPath)
	}

	if req.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", req.SystemPrompt)
	}

	// Prompt is NOT appended as a positional arg; it is piped via stdin
	// in Execute() to avoid OS ARG_MAX limits and shell escaping issues.
	return args
}

// --- Claude Output Parsing ---

// claudeOutput is the JSON from `claude --print --output-format json`.
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

// claudeUsage holds token usage reported by the Claude CLI.
type claudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// claudeStreamMsg represents a single line from `--output-format stream-json`.
type claudeStreamMsg struct {
	Type       string       `json:"type"`              // "system", "assistant", "user", "result"
	Subtype    string       `json:"subtype,omitempty"`
	Message    *claudeMsg   `json:"message,omitempty"`
	// result fields (same as claudeOutput):
	Result     string       `json:"result,omitempty"`
	IsError    bool         `json:"is_error,omitempty"`
	DurationMs int64        `json:"duration_ms,omitempty"`
	CostUSD    float64      `json:"total_cost_usd,omitempty"`
	SessionID  string       `json:"session_id,omitempty"`
	NumTurns   int          `json:"num_turns,omitempty"`
	Usage      *claudeUsage `json:"usage,omitempty"`
}

type claudeMsg struct {
	Role    string               `json:"role"`
	Content []claudeContentBlock `json:"content"`
}

type claudeContentBlock struct {
	Type      string          `json:"type"` // "text", "tool_use", "tool_result"
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   any             `json:"content,omitempty"` // tool_result content
}

// parseClaudeOutput parses Claude CLI JSON output into a TaskResult.
func parseClaudeOutput(stdout, stderr []byte, exitCode int) TaskResult {
	var co claudeOutput
	result := TaskResult{ExitCode: exitCode}

	if err := json.Unmarshal(stdout, &co); err == nil {
		result.Output = co.Result
		result.CostUSD = co.CostUSD
		result.SessionID = co.SessionID
		result.ProviderMs = co.DurationMs
		if co.Usage != nil {
			result.TokensIn = co.Usage.InputTokens
			result.TokensOut = co.Usage.OutputTokens
		}
		if co.IsError {
			result.Status = "error"
			result.Error = co.Subtype
		} else {
			result.Status = "success"
		}
		return result
	}

	// Fallback: treat raw output as text.
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
