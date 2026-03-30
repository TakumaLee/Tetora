package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// GeminiProvider executes tasks using the Gemini CLI.
type GeminiProvider struct {
	BinaryPath    string
	DockerEnabled bool
	Docker        DockerRunner
}

func (p *GeminiProvider) Name() string { return "gemini-cli" }

func (p *GeminiProvider) Execute(ctx context.Context, req Request) (*Result, error) {
	// For Gemini CLI, we'll always use streaming mode internally to capture output easily,
	// even if the caller didn't request events.
	return p.executeStreaming(ctx, req)
}

func (p *GeminiProvider) executeStreaming(ctx context.Context, req Request) (*Result, error) {
	args := []string{"--prompt", req.Prompt, "--output-format", "stream-json"}
	if req.Model != "" && req.Model != "sonnet" && req.Model != "opus" && req.Model != "haiku" {
		args = append(args, "--model", req.Model)
	}
	// Only try to resume if it looks like a Gemini session index or 'latest'.
	// Tetora's internal UUID session IDs are not compatible with Gemini CLI's session management.
	if req.SessionID == "latest" || (len(req.SessionID) > 0 && len(req.SessionID) < 3) {
		args = append(args, "--resume", req.SessionID)
	}

	var cmd *exec.Cmd
	if p.shouldUseDocker(req) {
		cmd = p.Docker.BuildCmd(ctx, p.BinaryPath, req.Workdir, args, req.AddDirs, req.MCPPath)
	} else {
		cmd = exec.CommandContext(ctx, p.BinaryPath, args...)
		cmd.Dir = req.Workdir
	}

	// Kill entire process group on timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return os.ErrProcessDone
	}

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

	res := &Result{IsError: false}
	scanner := bufio.NewScanner(stdoutPipe)
	for scanner.Scan() {
		line := scanner.Bytes()
		var msg geminiStreamMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "message":
			if msg.Role == "assistant" && msg.Content != "" {
				res.Output += msg.Content
				if req.OnEvent != nil {
					req.OnEvent(Event{
						Type:      EventOutputChunk,
						TaskID:    req.SessionID,
						SessionID: req.SessionID,
						Data:      map[string]any{"chunk": msg.Content},
						Timestamp: time.Now().Format(time.RFC3339),
					})
				}
			}
		case "result":
			res.SessionID = msg.SessionID
			if msg.Status == "error" {
				res.IsError = true
				res.Error = msg.Error
			}
			if msg.Stats != nil {
				res.TokensIn = msg.Stats.InputTokens
				res.TokensOut = msg.Stats.OutputTokens
				res.ProviderMs = msg.Stats.DurationMs
			}
		case "init":
			res.SessionID = msg.SessionID
		}
	}

	runErr := cmd.Wait()
	res.DurationMs = time.Since(start).Milliseconds()

	if ctx.Err() == context.DeadlineExceeded {
		res.IsError = true
		res.Error = "timed out"
	} else if runErr != nil && !res.IsError {
		res.IsError = true
		res.Error = stderr.String()
		if res.Error == "" {
			res.Error = runErr.Error()
		}
	}

	return res, nil
}

func (p *GeminiProvider) shouldUseDocker(req Request) bool {
	if p.Docker == nil {
		return false
	}
	if req.Docker != nil {
		return *req.Docker
	}
	return p.DockerEnabled
}

type geminiStreamMsg struct {
	Type      string       `json:"type"`
	Role      string       `json:"role,omitempty"`
	Content   string       `json:"content,omitempty"`
	Status    string       `json:"status,omitempty"`
	Error     string       `json:"error,omitempty"`
	SessionID string       `json:"session_id,omitempty"`
	Stats     *geminiStats `json:"stats,omitempty"`
}

type geminiStats struct {
	InputTokens  int   `json:"input_tokens"`
	OutputTokens int   `json:"output_tokens"`
	DurationMs   int64 `json:"duration_ms"`
}
