package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"tetora/internal/dispatch"
)

const ManifestSchemaVersion = 1

// Manifest captures the structural breakdown of a dispatched prompt.
// One manifest is produced per BuildTieredPrompt call and saved alongside
// the task output for post-hoc debugging.
type Manifest struct {
	SchemaVersion      int       `json:"schema_version"`
	TaskID             string    `json:"task_id"`
	TaskName           string    `json:"task_name,omitempty"`
	GeneratedAt        string    `json:"generated_at"`
	Tier               string    `json:"tier"`
	Provider           string    `json:"provider,omitempty"`
	ProviderType       string    `json:"provider_type,omitempty"`
	Agent              string    `json:"agent,omitempty"`
	Source             string    `json:"source,omitempty"`
	ScopeBoundary      string    `json:"scope_boundary,omitempty"`
	ComplexityHintUsed bool      `json:"complexity_hint_used,omitempty"`
	ClassifyResult     string    `json:"classify_result,omitempty"`
	Totals             Totals    `json:"totals"`
	Sections           []Section `json:"sections"`
}

// Totals aggregates the final prompt sizes after all injection is complete.
type Totals struct {
	SystemPromptBytes int `json:"system_prompt_bytes"`
	UserPromptBytes   int `json:"user_prompt_bytes"`
	AllowedToolsCount int `json:"allowed_tools_count"`
	AddDirsCount      int `json:"add_dirs_count"`
}

// Section records a single inject region.
// Target values: "system_prompt" | "user_prompt" | "add_dirs".
type Section struct {
	Name      string   `json:"name"`
	Target    string   `json:"target"`
	Path      string   `json:"path,omitempty"`
	Bytes     int      `json:"bytes"`
	Truncated bool     `json:"truncated,omitempty"`
	HashHex   string   `json:"hash_hex,omitempty"`
	Items     []string `json:"items,omitempty"`
	Dropped   []string `json:"dropped,omitempty"`
	ItemCount int      `json:"item_count,omitempty"`
	MsgCount  int      `json:"msg_count,omitempty"`
}

// SectionOpt is a functional option for Record.
type SectionOpt func(*Section)

// Path sets the source file path for the section.
func Path(p string) SectionOpt { return func(s *Section) { s.Path = p } }

// Truncated marks the section as having been truncated.
func Truncated(t bool) SectionOpt { return func(s *Section) { s.Truncated = t } }

// Hash sets a content hash (hex sha256) for the section.
func Hash(h string) SectionOpt { return func(s *Section) { s.HashHex = h } }

// HashOf computes sha256 of the given content and sets it on the section.
func HashOf(content string) SectionOpt {
	sum := sha256.Sum256([]byte(content))
	return Hash(hex.EncodeToString(sum[:]))
}

// Items records the named items included (e.g. matched skill names).
func Items(items []string) SectionOpt { return func(s *Section) { s.Items = items } }

// Dropped records named items that were considered but dropped.
func Dropped(items []string) SectionOpt { return func(s *Section) { s.Dropped = items } }

// ItemCount records the number of items (when names are not tracked).
func ItemCount(n int) SectionOpt { return func(s *Section) { s.ItemCount = n } }

// MsgCount records the number of messages in a history-style section.
func MsgCount(n int) SectionOpt { return func(s *Section) { s.MsgCount = n } }

// NewManifest constructs an empty manifest bound to a task.
// Safe to call with nil task; fields are best-effort populated.
func NewManifest(task *dispatch.Task, tier, provider, providerType, agent string) *Manifest {
	m := &Manifest{
		SchemaVersion: ManifestSchemaVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Tier:          tier,
		Provider:      provider,
		ProviderType:  providerType,
		Agent:         agent,
	}
	if task != nil {
		m.TaskID = task.ID
		m.TaskName = task.Name
		m.Source = task.Source
		m.ScopeBoundary = task.ScopeBoundary
	}
	return m
}

// Record adds a section to the manifest. No-op when m is nil or bytes <= 0.
// Bytes <= 0 is treated as "nothing was injected" and skipped to keep the
// manifest free of empty records.
func (m *Manifest) Record(name, target string, bytes int, opts ...SectionOpt) {
	if m == nil || bytes <= 0 {
		return
	}
	sec := Section{
		Name:   name,
		Target: target,
		Bytes:  bytes,
	}
	for _, opt := range opts {
		opt(&sec)
	}
	m.Sections = append(m.Sections, sec)
}

// Finalize computes Totals from the task's current prompt state.
// Call after all injections finish (and before returning from BuildTieredPrompt).
func (m *Manifest) Finalize(task *dispatch.Task) {
	if m == nil || task == nil {
		return
	}
	m.Totals = Totals{
		SystemPromptBytes: len(task.SystemPrompt),
		UserPromptBytes:   len(task.Prompt),
		AllowedToolsCount: len(task.AllowedTools),
		AddDirsCount:      len(task.AddDirs),
	}
}

// Save writes the manifest as JSON to {baseDir}/outputs/{shortID}_{ts}.prompt-manifest.json.
// baseDir should match the value passed to saveTaskOutput — typically cfg.OutputsDirFor(task.ClientID).
// Returns the filename (not full path) for storage in history; "" on error or when m is nil.
func (m *Manifest) Save(baseDir string) (string, error) {
	if m == nil {
		return "", nil
	}
	if baseDir == "" {
		return "", fmt.Errorf("prompt manifest: empty baseDir")
	}
	outputDir := filepath.Join(baseDir, "outputs")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("prompt manifest: mkdir outputs: %w", err)
	}

	shortID := m.TaskID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	if shortID == "" {
		shortID = "unknown"
	}
	ts := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s_%s.prompt-manifest.json", shortID, ts)
	filePath := filepath.Join(outputDir, filename)

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("prompt manifest: marshal: %w", err)
	}
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return "", fmt.Errorf("prompt manifest: write: %w", err)
	}
	return filename, nil
}
