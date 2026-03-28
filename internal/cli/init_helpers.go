package cli

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
)

// RandomListenAddr picks a random available port on 127.0.0.1 and returns
// the full listen address string (e.g. "127.0.0.1:52341").
func RandomListenAddr() string {
	return RandomListenPort("127.0.0.1")
}

// RandomListenPort picks a random available port on the given host and returns
// the full listen address string (e.g. "0.0.0.0:52341").
func RandomListenPort(host string) string {
	l, err := net.Listen("tcp", host+":0")
	if err != nil {
		return host + ":8991"
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// DetectClaude finds the claude CLI binary path.
// Preference order: /usr/local/bin/claude → ~/.local/bin/claude → PATH lookup.
func DetectClaude() string {
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		"/usr/local/bin/claude",
		filepath.Join(home, ".local", "bin", "claude"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if path, err := exec.LookPath("claude"); err == nil {
		return path
	}
	return "/usr/local/bin/claude"
}

// MutateConfig reads a JSON config file, applies a mutation function, and
// writes it back. The mutation receives the decoded top-level map and may
// modify it in place. Preserves all other fields via raw JSON round-trip.
func MutateConfig(configPath string, mutate func(raw map[string]any)) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	mutate(raw)
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o600)
}
