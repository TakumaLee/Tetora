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
	args := buildClaudeArgs(req)

	var cmd *exec.Cmd
	if p.shouldUseDocker(req) {
		// Rewrite args for Docker context (path remapping).
		dockerArgs := rewriteDockerArgs(args, req.AddDirs, req.MCPPath)
		envVars := dockerEnvFilter(p.cfg.Docker)
		cmd = buildDockerCmd(ctx, p.cfg.Docker, req.Workdir, p.binaryPath, dockerArgs, req.AddDirs, req.MCPPath, envVars)
	} else {
		cmd = exec.CommandContext(ctx, p.binaryPath, args...)
		cmd.Dir = req.Workdir
		cmd.Env = os.Environ()
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

// executeStreaming runs the command with incremental stdout reading.
// Each line of stdout is published as an output_chunk event via req.EventCh.
// The full output is still collected for final parsing.
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

	// Read stdout line-by-line, accumulate and stream.
	var stdout bytes.Buffer
	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024) // 1MB max line
	for scanner.Scan() {
		line := scanner.Bytes()
		stdout.Write(line)
		stdout.WriteByte('\n')

		// Emit output chunk event.
		if req.EventCh != nil {
			req.EventCh <- SSEEvent{
				Type:      SSEOutputChunk,
				TaskID:    req.SessionID, // will be overridden by dispatch layer
				SessionID: req.SessionID,
				Data: map[string]string{
					"chunk": string(line),
				},
				Timestamp: time.Now().Format(time.RFC3339),
			}
		}
	}

	// Also drain any remaining data from pipe (e.g., if scanner hit size limit).
	remaining, _ := io.ReadAll(stdoutPipe)
	if len(remaining) > 0 {
		stdout.Write(remaining)
	}

	runErr := cmd.Wait()
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

// shouldUseDocker determines if this request should run in a Docker sandbox.
// Chain: req.Docker (task override) → config.Docker.Enabled → false.
func (p *ClaudeProvider) shouldUseDocker(req ProviderRequest) bool {
	if req.Docker != nil {
		return *req.Docker
	}
	return p.cfg.Docker.Enabled
}

// buildClaudeArgs constructs the claude CLI argument list from a ProviderRequest.
func buildClaudeArgs(req ProviderRequest) []string {
	args := []string{
		"--print",
		"--output-format", "json",
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
