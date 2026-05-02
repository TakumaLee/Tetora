// Package agent provides agent configuration and task-watching logic.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// askAgentTimeout caps how long we wait for claude to answer the capability
// question; without this, a hung claude blocks `tetora agent configure`
// indefinitely.
const askAgentTimeout = 120 * time.Second

// Capability describes a Tetora capability that an agent can opt into.
type Capability struct {
	ID          string
	Description string
	DetailFile  string // filename inside capabilities/ (empty = no file)
	CronExpr    string // non-empty → add a cron job entry
	ConfigFlag  string // non-empty → set this config key to true (e.g. "deepMemoryExtract.enabled")
}

// BuiltinCapabilities is the fixed catalogue of capabilities configure can install.
var BuiltinCapabilities = []Capability{
	{
		ID:          "memory.auto-extracts",
		Description: "訂閱跨 session 萃取的知識",
		DetailFile:  "memory-subscribe.md",
	},
	{
		ID:          "skill-evolve",
		Description: "管理 skill 改寫 proposals（list / approve / reject）",
		DetailFile:  "skill-evolve.md",
	},
	{
		ID:          "weekly-review",
		Description: "週期性 lesson promote + rule audit（每週日 09:00）",
		DetailFile:  "weekly-review.md",
		CronExpr:    "0 9 * * 0",
	},
	{
		ID:          "deep-memory-extract",
		Description: "高品質任務後自動萃取跨 session 知識",
		DetailFile:  "deep-memory-extract.md",
		ConfigFlag:  "deepMemoryExtract.enabled",
	},
}

// capabilityResponse is the JSON the agent returns during capability selection.
type capabilityResponse struct {
	SelectedCapabilities []string `json:"selected_capabilities"`
	Reason               string   `json:"reason"`
}

// ConfigureResult summarises what was done for one agent.
type ConfigureResult struct {
	Agent    string
	Selected []string
	Reason   string
	// CronJobs holds IDs of capabilities that require a cron job entry.
	CronJobs []string
	// ConfigFlags maps config key → true for capabilities that set a flag
	// (e.g. "deepMemoryExtract.enabled"). The CLI layer applies these.
	ConfigFlags map[string]bool
}

// Configure asks the agent which capabilities it needs, then writes the files.
// ioProtocolPath is the absolute path to tetora-agent-io-protocol.md (used in
// the generated CLAUDE.md @include line).
func Configure(claudePath, agentsDir, ioProtocolPath, agentName string) (*ConfigureResult, error) {
	agentDir := filepath.Join(agentsDir, agentName)

	soulContent := loadSoul(agentDir)
	if soulContent == "" {
		fmt.Fprintf(os.Stderr, "warning: no SOUL.md found in %s — generated CLAUDE.md still references @SOUL.md\n", agentDir)
	}
	prompt := buildConfigurePrompt(agentName, soulContent)

	resp, err := askAgent(claudePath, agentDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("asking agent: %w", err)
	}

	known := make(map[string]bool, len(BuiltinCapabilities))
	for _, c := range BuiltinCapabilities {
		known[c.ID] = true
	}
	for _, id := range resp.SelectedCapabilities {
		if !known[id] {
			fmt.Fprintf(os.Stderr, "warning: agent %s requested unknown capability %q (ignored)\n", agentName, id)
		}
	}

	if err := generateCapabilityFiles(agentDir, ioProtocolPath, resp); err != nil {
		return nil, err
	}

	result := &ConfigureResult{
		Agent:       agentName,
		Selected:    resp.SelectedCapabilities,
		Reason:      resp.Reason,
		ConfigFlags: make(map[string]bool),
	}
	for _, id := range resp.SelectedCapabilities {
		for _, cap := range BuiltinCapabilities {
			if cap.ID != id {
				continue
			}
			if cap.CronExpr != "" {
				result.CronJobs = append(result.CronJobs, id)
			}
			if cap.ConfigFlag != "" {
				result.ConfigFlags[cap.ConfigFlag] = true
			}
		}
	}
	return result, nil
}

// ConfigureAll runs Configure for every agent name in the list. It continues on
// individual failures and returns an aggregated error if any agents failed.
func ConfigureAll(claudePath, agentsDir, ioProtocolPath string, agents []string) ([]*ConfigureResult, error) {
	var results []*ConfigureResult
	var errs []string
	for _, name := range agents {
		fmt.Printf("Configuring %s...\n", name)
		r, err := Configure(claudePath, agentsDir, ioProtocolPath, name)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			fmt.Printf("  Error: %v\n", err)
			continue
		}
		results = append(results, r)
		fmt.Printf("  Selected: %s\n", strings.Join(r.Selected, ", "))
	}
	if len(errs) > 0 {
		return results, fmt.Errorf("%d agent(s) failed: %s", len(errs), strings.Join(errs, "; "))
	}
	return results, nil
}

// loadSoul reads SOUL.md from the agent dir. Returns "" if not found.
func loadSoul(agentDir string) string {
	for _, name := range []string{"SOUL.md", "soul.md"} {
		data, err := os.ReadFile(filepath.Join(agentDir, name))
		if err == nil {
			return string(data)
		}
	}
	return ""
}

func buildConfigurePrompt(agentName, soulContent string) string {
	var sb strings.Builder

	if soulContent != "" {
		sb.WriteString("Your role definition:\n\n")
		sb.WriteString(soulContent)
		sb.WriteString("\n\n---\n\n")
	}

	sb.WriteString(fmt.Sprintf("You are %s, an agent registered in Tetora.\n\n", agentName))
	sb.WriteString("Tetora is setting up your capability system. Review the available capabilities and select which ones are relevant to your role.\n\n")
	sb.WriteString("Available capabilities:\n")
	for _, c := range BuiltinCapabilities {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", c.ID, c.Description))
	}
	sb.WriteString("\nRespond ONLY with a JSON object (no markdown, no explanation):\n")
	sb.WriteString(`{"selected_capabilities": ["id1", "id2"], "reason": "brief explanation"}`)
	sb.WriteString("\n")

	return sb.String()
}

// askAgent spawns claude with the prompt and parses the JSON capability response.
func askAgent(claudePath, agentDir, prompt string) (*capabilityResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), askAgentTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, claudePath, "-p", prompt)
	if _, err := os.Stat(agentDir); err == nil {
		cmd.Dir = agentDir
	}

	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("claude timed out after %s", askAgentTimeout)
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("claude exit %d: %s", ee.ExitCode(), string(ee.Stderr))
		}
		return nil, err
	}

	return parseCapabilityResponse(string(out))
}

// extractJSONObject returns the first balanced `{...}` block in s that contains
// the literal `"selected_capabilities"`. Walks the string respecting brace
// depth and string literals, so reasons like `"reason": "use {memory.x}"` and
// nested examples don't break the match.
func extractJSONObject(s, mustContain string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		end := matchBrace(s, i)
		if end == -1 {
			continue
		}
		block := s[i : end+1]
		if strings.Contains(block, mustContain) {
			return block
		}
	}
	return ""
}

// matchBrace returns the index of the `}` matching the `{` at start, or -1.
// Honours string literals (with backslash escapes) so braces inside JSON
// strings don't unbalance the count.
func matchBrace(s string, start int) int {
	depth := 0
	inStr := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func parseCapabilityResponse(output string) (*capabilityResponse, error) {
	raw := extractJSONObject(output, `"selected_capabilities"`)
	if raw == "" {
		raw = strings.TrimSpace(output)
	}

	var resp capabilityResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, fmt.Errorf("parse response JSON: %w\nraw: %s", err, output)
	}
	return &resp, nil
}

// generateCapabilityFiles writes CLAUDE.md and the capabilities/ subtree.
func generateCapabilityFiles(agentDir, ioProtocolPath string, resp *capabilityResponse) error {
	capDir := filepath.Join(agentDir, "capabilities")
	if err := os.MkdirAll(capDir, 0o755); err != nil {
		return fmt.Errorf("create capabilities dir: %w", err)
	}

	selected := make(map[string]bool, len(resp.SelectedCapabilities))
	for _, id := range resp.SelectedCapabilities {
		selected[id] = true
	}

	if err := writeCLAUDEMD(agentDir, ioProtocolPath); err != nil {
		return err
	}
	if err := writeCapabilitiesIndex(capDir, selected); err != nil {
		return err
	}
	for _, cap := range BuiltinCapabilities {
		if selected[cap.ID] {
			if err := writeCapabilityDetail(capDir, cap); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeCLAUDEMD(agentDir, ioProtocolPath string) error {
	content := fmt.Sprintf("@SOUL.md\n@capabilities/index.md\n@%s\n", ioProtocolPath)
	return os.WriteFile(filepath.Join(agentDir, "CLAUDE.md"), []byte(content), 0o644)
}

func writeCapabilitiesIndex(capDir string, selected map[string]bool) error {
	var sb strings.Builder
	sb.WriteString("# Capabilities\n\n")
	for _, cap := range BuiltinCapabilities {
		if !selected[cap.ID] {
			continue
		}
		sb.WriteString(fmt.Sprintf("## %s\n%s\nDetail: capabilities/%s\n\n", cap.ID, cap.Description, cap.DetailFile))
	}
	return os.WriteFile(filepath.Join(capDir, "index.md"), []byte(sb.String()), 0o644)
}

var capabilityDetailContents = map[string]string{
	"deep-memory-extract": `# deep-memory-extract

高品質任務完成後（reflection score ≥ 4 + cost ≥ $0.10 + 成功），Tetora 自動從對話中萃取跨 session 知識。

## 已啟用

你的 Tetora 設定中 deepMemoryExtract.enabled 已設為 true。萃取會在符合條件的任務完成後自動觸發。

## 萃取後的知識去哪

- memory/extract:<slug>.md（個別知識條目）
- memory/auto-extracts.md（FIFO index，最多 100 筆）

## 搭配使用

在 SOUL.md 訂閱萃取結果：

` + "```" + `
{{memory.auto-extracts}}
` + "```" + `

這樣每次任務都能看到 Tetora 最近自動萃取的跨 session 知識。
`,
	"memory.auto-extracts": `# memory.auto-extracts

Tetora 自動從高品質任務中萃取跨 session 知識，存入 memory/auto-extracts.md。

## 使用方式

在 SOUL.md 的 Tetora 記憶訂閱區段加入：

` + "```" + `
{{memory.auto-extracts}}
` + "```" + `

## 讀取個別萃取記憶

` + "```" + `
{{memory.extract:SLUG}}
` + "```" + `

其中 SLUG 是 memory/extract:<slug>.md 的 slug 部分。
每次接到任務時，Tetora 會自動展開這些模板並注入最新知識。
`,
	"skill-evolve": `# skill-evolve

當 skill 失敗率 ≥ 40%（最少 10 次 invocation，7 天冷卻），Tetora 自動生成改寫 proposal。

## 指令

` + "```bash" + `
tetora skill evolve list              # 列出所有待審 proposals
tetora skill evolve approve <name>    # 套用 proposal
tetora skill evolve reject <name>     # 拒絕 proposal
` + "```" + `

## 流程

1. Tetora 每日 03:00 掃描高失敗率 skill
2. 超過門檻 → 生成 proposal 到 skills/<name>/proposals/<timestamp>.md
3. 你收到通知後執行 approve 或 reject
`,
	"weekly-review": `# weekly-review

每週日 09:00 執行自我整理，維護 lesson/rule 系統的健康。

## 指令

` + "```bash" + `
tetora lesson promote    # 把重複 3+ 次的 lesson 升格為 rule
tetora rule audit        # 掃描長時間未更新的 stale rule
` + "```" + `

## 建議串聯

` + "```bash" + `
tetora lesson promote && tetora rule audit
` + "```" + `

由你的 cron job（每週日 09:00）自動執行。
`,
}

func writeCapabilityDetail(capDir string, cap Capability) error {
	content, ok := capabilityDetailContents[cap.ID]
	if !ok {
		content = fmt.Sprintf("# %s\n\n%s\n", cap.ID, cap.Description)
	}
	return os.WriteFile(filepath.Join(capDir, cap.DetailFile), []byte(content), 0o644)
}
