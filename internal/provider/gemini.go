package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// GeminiProvider executes tasks using the Gemini CLI.
type GeminiProvider struct {
	BinaryPath string
}

func (p *GeminiProvider) Name() string { return "gemini" }

func (p *GeminiProvider) Execute(ctx context.Context, req Request) (*Result, error) {
	args := BuildGeminiArgs(req, req.OnEvent != nil)

	cmd := exec.CommandContext(ctx, p.BinaryPath, args...)
	cmd.Dir = req.Workdir
	cmd.Env = os.Environ()
	// Close stdin so gemini doesn't hang.
	if devNull, err := os.Open(os.DevNull); err == nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	}
	// Kill entire process group on timeout.
	SetProcessGroup(cmd)
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

	pr := ParseGeminiOutput(stdout.Bytes(), stderr.Bytes(), exitCode)
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

func (p *GeminiProvider) executeStreaming(ctx context.Context, cmd *exec.Cmd, req Request) (*Result, error) {
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

		// Gemini CLI emits stream-json chunks or final json result.
		var msg struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype,omitempty"`
			Content string `json:"content,omitempty"`
			Result  string `json:"result,omitempty"`
			IsError bool   `json:"is_error,omitempty"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
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

		if msg.Type == "assistant" && msg.Content != "" {
			outputParts = append(outputParts, msg.Content)
			if req.OnEvent != nil {
				req.OnEvent(Event{
					Type:      EventOutputChunk,
					TaskID:    req.SessionID,
					SessionID: req.SessionID,
					Data: map[string]any{
						"chunk":     msg.Content,
						"chunkType": "text",
					},
					Timestamp: time.Now().Format(time.RFC3339),
				})
			}
		} else if msg.Type == "result" {
			finalResult = &Result{
				Output:  msg.Result,
				IsError: msg.IsError,
			}
		}
	}

	if err := cmd.Wait(); err != nil && finalResult != nil && !finalResult.IsError {
		finalResult.IsError = true
		finalResult.Error = err.Error()
	}
	elapsed := time.Since(start)

	if finalResult == nil {
		finalResult = &Result{
			Output: strings.Join(outputParts, ""),
		}
		if len(stderr.Bytes()) > 0 {
			finalResult.IsError = true
			finalResult.Error = strings.TrimSpace(stderr.String())
		}
	}

	finalResult.DurationMs = elapsed.Milliseconds()
	return finalResult, nil
}

// BuildGeminiArgs constructs the gemini CLI argument list.
func BuildGeminiArgs(req Request, streaming bool) []string {
	format := "json"
	if streaming {
		format = "stream-json"
	}
	args := []string{"-o", format}

	if req.Model != "" {
		args = append(args, "-m", req.Model)
	}

	mode := "auto_edit"
	if req.PermissionMode != "" {
		switch req.PermissionMode {
		case "bypassPermissions":
			mode = "yolo"
		case "acceptEdits":
			mode = "auto_edit"
		case "plan":
			mode = "plan"
		}
	}
	args = append(args, "--approval-mode", mode)

	if req.Workdir != "" {
		args = append(args, "--include-directories", req.Workdir)
	}
	for _, dir := range req.AddDirs {
		args = append(args, "--include-directories", dir)
	}

	if req.Resume && req.SessionID != "" {
		args = append(args, "-r", req.SessionID)
	}

	if req.Prompt != "" {
		args = append(args, "-p", req.Prompt)
	}

	return args
}

// ParseGeminiOutput parses the JSON output from gemini CLI.
func ParseGeminiOutput(stdout, stderr []byte, exitCode int) *Result {
	var msg struct {
		Type    string `json:"type"`
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
		Error   string `json:"error"`
	}

	if err := json.Unmarshal(stdout, &msg); err == nil && msg.Type == "result" {
		return &Result{
			Output:  msg.Result,
			IsError: msg.IsError,
			Error:   msg.Error,
		}
	}

	// Fallback to raw text.
	res := &Result{Output: string(stdout)}
	if exitCode != 0 {
		res.IsError = true
		res.Error = strings.TrimSpace(string(stderr))
		if res.Error == "" {
			res.Error = fmt.Sprintf("gemini exited with code %d", exitCode)
		}
	}
	return res
}
