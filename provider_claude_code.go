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

// ClaudeCodeProvider executes tasks using the Claude Code CLI binary.
// Unlike ClaudeProvider (which wraps the legacy Claude CLI), this provider
// targets the Claude Code CLI (`claude` from Claude Code) and always uses
// stream-json output format for real-time parsing.
//
// It does NOT implement ToolCapableProvider — Claude Code handles tool
// execution internally.
type ClaudeCodeProvider struct {
	binaryPath string
	cfg        *Config
}

func (p *ClaudeCodeProvider) Name() string { return "claude-code" }

func (p *ClaudeCodeProvider) Execute(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	args := buildClaudeCodeArgs(req)

	cmd := exec.CommandContext(ctx, p.binaryPath, args...)
	cmd.Dir = req.Workdir

	// Filter out Claude Code session env vars to allow spawning Claude Code from within
	// a Claude Code session. Claude Code checks CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
	// and CLAUDE_CODE_TEAM_MODE to detect nested sessions.
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

	// Pipe prompt via stdin to avoid OS ARG_MAX limits.
	if req.Prompt != "" {
		cmd.Stdin = strings.NewReader(req.Prompt)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude-code stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude-code start: %w", err)
	}

	// Parse stream-json output line by line.
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
			continue // skip unparseable lines
		}

		switch msg.Type {
		case "assistant":
			// Emit SSE events for live streaming if channel is set.
			if req.EventCh != nil && msg.Message != nil {
				for _, block := range msg.Message.Content {
					switch block.Type {
					case "text":
						if block.Text != "" {
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
		case "result":
			resultMsg = &msg
		}
	}

	// Drain remaining pipe data.
	remaining, _ := io.ReadAll(stdoutPipe)
	_ = remaining

	runErr := cmd.Wait()
	elapsed := time.Since(start)

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	// Reuse the shared stream result builder from provider_claude.go.
	pr := buildResultFromStream(resultMsg, stderr.Bytes(), exitCode)
	pr.DurationMs = elapsed.Milliseconds()
	pr.Provider = "claude-code"

	// Soft-limit: log when cost exceeds per-task budget without stopping.
	if req.Budget > 0 && pr.CostUSD >= req.Budget {
		promptPreview := req.Prompt
		if len(promptPreview) > 120 {
			promptPreview = promptPreview[:120]
		}
		logWarn("claude-code task exceeded budget soft-limit (completed normally)",
			"budget", req.Budget,
			"spent", pr.CostUSD,
			"model", req.Model,
			"prompt_preview", promptPreview,
		)
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
		errStr := stderr.String()
		if len(errStr) > 500 {
			errStr = errStr[:500]
		}
		pr.Error = fmt.Sprintf("claude-code exit: %v; stderr: %s", runErr, errStr)
	}

	return pr, nil
}

// buildClaudeCodeArgs constructs the Claude Code CLI argument list.
// Claude Code natively reads project files, so --add-dir is never added.
func buildClaudeCodeArgs(req ProviderRequest) []string {
	args := []string{
		"--print",
		"--verbose",
		"--output-format", "stream-json",
	}

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	if req.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", req.SystemPrompt)
	}

	// NOTE: --max-budget-usd is intentionally NOT passed.
	// Tetora uses a soft-limit approach: log when budget is exceeded, but don't hard-stop.
	// This allows large tasks to complete while surfacing cost data for optimization.

	if req.SessionID != "" {
		args = append(args, "--session-id", req.SessionID)
	}

	if req.PermissionMode != "" {
		args = append(args, "--permission-mode", req.PermissionMode)
	}

	// Prompt is piped via stdin, not as a positional arg.
	return args
}

