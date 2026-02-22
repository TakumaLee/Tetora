package main

// --- P13.2: Sandbox Plugin ---
// tetora-plugin-docker-sandbox — Docker container sandbox plugin for Tetora.
//
// This is a standalone binary that communicates with the Tetora daemon
// via JSON-RPC over stdin/stdout. It manages Docker containers for
// sandboxed task execution.
//
// Build: go build ./cmd/tetora-plugin-docker-sandbox/

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// --- JSON-RPC Types ---

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- Container State ---

type container struct {
	ID        string
	SessionID string
	Image     string
	CreatedAt time.Time
}

type containerStore struct {
	mu         sync.RWMutex
	containers map[string]*container // sandboxId -> container
}

func newContainerStore() *containerStore {
	return &containerStore{
		containers: make(map[string]*container),
	}
}

func (cs *containerStore) get(sandboxID string) (*container, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	c, ok := cs.containers[sandboxID]
	return c, ok
}

func (cs *containerStore) put(sandboxID string, c *container) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.containers[sandboxID] = c
}

func (cs *containerStore) remove(sandboxID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.containers, sandboxID)
}

// --- Default Config ---

var defaultImage = "ubuntu:22.04"

// --- Main ---

func main() {
	// Parse CLI args for default image override.
	for i := 1; i < len(os.Args)-1; i++ {
		if os.Args[i] == "--image" {
			defaultImage = os.Args[i+1]
		}
	}

	store := newContainerStore()
	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer for large requests.
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			writeError(0, -32700, "parse error: "+err.Error())
			continue
		}

		handleRequest(req, store)
	}
}

func handleRequest(req jsonRPCRequest, store *containerStore) {
	switch req.Method {
	case "ping":
		writeResult(req.ID, map[string]any{"pong": true})

	case "sandbox/health":
		handleHealth(req)

	case "sandbox/create":
		handleCreate(req, store)

	case "sandbox/exec":
		handleExec(req, store)

	case "sandbox/copy_in":
		handleCopyIn(req, store)

	case "sandbox/copy_out":
		handleCopyOut(req, store)

	case "sandbox/destroy":
		handleDestroy(req, store)

	default:
		writeError(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// --- Health ---

func handleHealth(req jsonRPCRequest) {
	// Check if docker is available.
	out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").Output()
	if err != nil {
		writeResult(req.ID, map[string]any{
			"available": false,
			"error":     fmt.Sprintf("docker not available: %v", err),
		})
		return
	}
	writeResult(req.ID, map[string]any{
		"available":     true,
		"dockerVersion": strings.TrimSpace(string(out)),
	})
}

// --- Create ---

func handleCreate(req jsonRPCRequest, store *containerStore) {
	var params struct {
		SessionID string `json:"sessionId"`
		Workspace string `json:"workspace"`
		Image     string `json:"image"`
		MemLimit  string `json:"memLimit"`
		CPULimit  string `json:"cpuLimit"`
		Network   string `json:"network"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	if params.SessionID == "" {
		writeError(req.ID, -32602, "sessionId is required")
		return
	}

	image := params.Image
	if image == "" {
		image = defaultImage
	}

	network := params.Network
	if network == "" {
		network = "none"
	}

	// Build docker create args.
	args := []string{"create",
		"--label", "tetora.sessionId=" + params.SessionID,
		"--label", "tetora.managed=true",
		"--network", network,
	}

	if params.MemLimit != "" {
		args = append(args, "--memory", params.MemLimit)
	}
	if params.CPULimit != "" {
		args = append(args, "--cpus", params.CPULimit)
	}

	// Mount workspace if provided.
	if params.Workspace != "" {
		args = append(args, "-v", params.Workspace+":/workspace", "--workdir", "/workspace")
	}

	// Use entrypoint that keeps container alive.
	args = append(args, image, "sleep", "infinity")

	// Create container.
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		writeError(req.ID, -32000, fmt.Sprintf("docker create failed: %v", err))
		return
	}

	containerID := strings.TrimSpace(string(out))
	if len(containerID) > 12 {
		containerID = containerID[:12]
	}

	// Start the container.
	if err := exec.Command("docker", "start", containerID).Run(); err != nil {
		// Cleanup on failure.
		exec.Command("docker", "rm", "-f", containerID).Run()
		writeError(req.ID, -32000, fmt.Sprintf("docker start failed: %v", err))
		return
	}

	store.put(containerID, &container{
		ID:        containerID,
		SessionID: params.SessionID,
		Image:     image,
		CreatedAt: time.Now(),
	})

	writeResult(req.ID, map[string]any{
		"sandboxId": containerID,
		"image":     image,
	})
}

// --- Exec ---

func handleExec(req jsonRPCRequest, store *containerStore) {
	var params struct {
		SandboxID string `json:"sandboxId"`
		Command   string `json:"command"`
		Timeout   int    `json:"timeout"` // seconds
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	if params.SandboxID == "" {
		writeError(req.ID, -32602, "sandboxId is required")
		return
	}
	if params.Command == "" {
		writeError(req.ID, -32602, "command is required")
		return
	}

	if _, ok := store.get(params.SandboxID); !ok {
		writeError(req.ID, -32000, fmt.Sprintf("sandbox %q not found", params.SandboxID))
		return
	}

	timeout := params.Timeout
	if timeout <= 0 {
		timeout = 120
	}

	// Execute command in container using sh -c.
	args := []string{"exec", params.SandboxID, "sh", "-c", params.Command}
	cmd := exec.Command("docker", args...)

	// Set up timeout.
	done := make(chan error, 1)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		writeError(req.ID, -32000, fmt.Sprintf("docker exec start failed: %v", err))
		return
	}

	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				writeError(req.ID, -32000, fmt.Sprintf("docker exec failed: %v", err))
				return
			}
		}
		writeResult(req.ID, map[string]any{
			"stdout":   stdout.String(),
			"stderr":   stderr.String(),
			"exitCode": exitCode,
		})

	case <-time.After(time.Duration(timeout) * time.Second):
		cmd.Process.Kill()
		writeResult(req.ID, map[string]any{
			"stdout":   stdout.String(),
			"stderr":   "execution timed out",
			"exitCode": -1,
		})
	}
}

// --- Copy In ---

func handleCopyIn(req jsonRPCRequest, store *containerStore) {
	var params struct {
		SandboxID     string `json:"sandboxId"`
		HostPath      string `json:"hostPath"`
		ContainerPath string `json:"containerPath"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	if params.SandboxID == "" || params.HostPath == "" || params.ContainerPath == "" {
		writeError(req.ID, -32602, "sandboxId, hostPath, and containerPath are required")
		return
	}

	if _, ok := store.get(params.SandboxID); !ok {
		writeError(req.ID, -32000, fmt.Sprintf("sandbox %q not found", params.SandboxID))
		return
	}

	// docker cp hostPath containerID:containerPath
	out, err := exec.Command("docker", "cp", params.HostPath, params.SandboxID+":"+params.ContainerPath).CombinedOutput()
	if err != nil {
		writeError(req.ID, -32000, fmt.Sprintf("docker cp in failed: %v: %s", err, string(out)))
		return
	}

	writeResult(req.ID, map[string]any{"ok": true})
}

// --- Copy Out ---

func handleCopyOut(req jsonRPCRequest, store *containerStore) {
	var params struct {
		SandboxID     string `json:"sandboxId"`
		ContainerPath string `json:"containerPath"`
		HostPath      string `json:"hostPath"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	if params.SandboxID == "" || params.ContainerPath == "" || params.HostPath == "" {
		writeError(req.ID, -32602, "sandboxId, containerPath, and hostPath are required")
		return
	}

	if _, ok := store.get(params.SandboxID); !ok {
		writeError(req.ID, -32000, fmt.Sprintf("sandbox %q not found", params.SandboxID))
		return
	}

	// docker cp containerID:containerPath hostPath
	out, err := exec.Command("docker", "cp", params.SandboxID+":"+params.ContainerPath, params.HostPath).CombinedOutput()
	if err != nil {
		writeError(req.ID, -32000, fmt.Sprintf("docker cp out failed: %v: %s", err, string(out)))
		return
	}

	writeResult(req.ID, map[string]any{"ok": true})
}

// --- Destroy ---

func handleDestroy(req jsonRPCRequest, store *containerStore) {
	var params struct {
		SandboxID string `json:"sandboxId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	if params.SandboxID == "" {
		writeError(req.ID, -32602, "sandboxId is required")
		return
	}

	// Force remove container (ignore errors — container may already be gone).
	exec.Command("docker", "rm", "-f", params.SandboxID).Run()
	store.remove(params.SandboxID)

	writeResult(req.ID, map[string]any{"ok": true})
}

// --- JSON-RPC Helpers ---

func writeResult(id int, result any) {
	data, _ := json.Marshal(result)
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  data,
	}
	out, _ := json.Marshal(resp)
	fmt.Fprintln(os.Stdout, string(out))
}

func writeError(id int, code int, message string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: message},
	}
	out, _ := json.Marshal(resp)
	fmt.Fprintln(os.Stdout, string(out))
}
