package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tetora/internal/discord"
	"tetora/internal/log"
)

// --- War Room Status JSON types ---

type warRoomStatus struct {
	SchemaVersion int        `json:"schema_version"`
	GeneratedAt   string     `json:"generated_at"`
	Fronts        []warFront `json:"fronts"`
}

type warFront struct {
	ID                      string           `json:"id"`
	Name                    string           `json:"name"`
	Category                string           `json:"category"`
	Auto                    bool             `json:"auto"`
	Status                  string           `json:"status"`
	Summary                 string           `json:"summary"`
	Blocking                string           `json:"blocking"`
	NextAction              string           `json:"next_action"`
	LastUpdated             string           `json:"last_updated"`
	StalenessThresholdHours *int             `json:"staleness_threshold_hours"`
	ManualOverride          warManualOverride `json:"manual_override"`
	DependsOn               []string         `json:"depends_on"`
}

type warManualOverride struct {
	Active    bool    `json:"active"`
	ExpiresAt *string `json:"expires_at"`
}

func (db *DiscordBot) warRoomStatusPath() string {
	return filepath.Join(db.cfg.BaseDir, "workspace/memory/war-room/status.json")
}

func (db *DiscordBot) loadWarRoomStatus() (*warRoomStatus, error) {
	data, err := os.ReadFile(db.warRoomStatusPath())
	if err != nil {
		return nil, fmt.Errorf("read status.json: %w", err)
	}
	var s warRoomStatus
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse status.json: %w", err)
	}
	return &s, nil
}

func (db *DiscordBot) saveWarRoomStatus(s *warRoomStatus) error {
	s.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal status.json: %w", err)
	}
	return os.WriteFile(db.warRoomStatusPath(), data, 0o644)
}

func findFront(s *warRoomStatus, id string) *warFront {
	for i := range s.Fronts {
		if s.Fronts[i].ID == id {
			return &s.Fronts[i]
		}
	}
	return nil
}

// --- /wr Slash Command Handler ---

func (db *DiscordBot) handleWrSlashCommand(interaction *discord.Interaction) discord.InteractionResponse {
	eph := func(msg string) discord.InteractionResponse {
		return discord.InteractionResponse{
			Type: discord.InteractionResponseMessage,
			Data: &discord.InteractionResponseData{Content: msg, Flags: 64},
		}
	}

	var data discord.SlashCommandData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		return eph("❌ 解析指令失敗。")
	}
	if len(data.Options) == 0 {
		return eph("❌ 缺少子命令。")
	}

	sub := data.Options[0]
	opts := sub.OptionMap()

	switch sub.Name {
	case "update":
		frontID, status, summary := opts["front_id"], opts["status"], opts["summary"]
		if frontID == "" || status == "" || summary == "" {
			return eph("❌ 缺少必要參數。")
		}
		valid := map[string]bool{"green": true, "yellow": true, "red": true, "unknown": true, "paused": true}
		if !valid[status] {
			return eph("❌ 無效 status，合法值：green / yellow / red / unknown / paused")
		}
		return db.wrUpdate(frontID, status, summary, eph)

	case "block":
		frontID, blockingText := opts["front_id"], opts["blocking_text"]
		if frontID == "" || blockingText == "" {
			return eph("❌ 缺少必要參數。")
		}
		return db.wrBlock(frontID, blockingText, eph)

	case "green":
		if opts["front_id"] == "" {
			return eph("❌ 缺少 front_id。")
		}
		return db.wrQuickStatus(opts["front_id"], "green", eph)

	case "red":
		if opts["front_id"] == "" {
			return eph("❌ 缺少 front_id。")
		}
		return db.wrQuickStatus(opts["front_id"], "red", eph)

	case "status":
		return db.wrShowStatus(eph)

	default:
		return eph("❌ 未知子命令：" + sub.Name)
	}
}

func (db *DiscordBot) wrUpdate(frontID, status, summary string, eph func(string) discord.InteractionResponse) discord.InteractionResponse {
	s, err := db.loadWarRoomStatus()
	if err != nil {
		log.Error("wr update: load failed", "err", err)
		return eph("❌ 讀取 status.json 失敗。")
	}
	f := findFront(s, frontID)
	if f == nil {
		return eph(fmt.Sprintf("❌ 找不到戰線 `%s`。", frontID))
	}
	f.Status = status
	f.Summary = summary
	f.LastUpdated = time.Now().UTC().Format(time.RFC3339)
	f.ManualOverride = warManualOverride{Active: false}
	if err := db.saveWarRoomStatus(s); err != nil {
		log.Error("wr update: save failed", "err", err)
		return eph("❌ 寫入 status.json 失敗。")
	}
	return discord.InteractionResponse{
		Type: discord.InteractionResponseMessage,
		Data: &discord.InteractionResponseData{
			Content: fmt.Sprintf("%s **%s** → `%s`\n> %s", wrStatusEmoji(status), f.Name, status, summary),
		},
	}
}

func (db *DiscordBot) wrBlock(frontID, blockingText string, eph func(string) discord.InteractionResponse) discord.InteractionResponse {
	s, err := db.loadWarRoomStatus()
	if err != nil {
		log.Error("wr block: load failed", "err", err)
		return eph("❌ 讀取 status.json 失敗。")
	}
	f := findFront(s, frontID)
	if f == nil {
		return eph(fmt.Sprintf("❌ 找不到戰線 `%s`。", frontID))
	}
	f.Status = "red"
	f.Blocking = blockingText
	f.LastUpdated = time.Now().UTC().Format(time.RFC3339)
	if err := db.saveWarRoomStatus(s); err != nil {
		log.Error("wr block: save failed", "err", err)
		return eph("❌ 寫入 status.json 失敗。")
	}
	return discord.InteractionResponse{
		Type: discord.InteractionResponseMessage,
		Data: &discord.InteractionResponseData{
			Content: fmt.Sprintf("🚨 **%s** blocked\n> %s", f.Name, blockingText),
		},
	}
}

func (db *DiscordBot) wrQuickStatus(frontID, status string, eph func(string) discord.InteractionResponse) discord.InteractionResponse {
	s, err := db.loadWarRoomStatus()
	if err != nil {
		log.Error("wr quick: load failed", "err", err)
		return eph("❌ 讀取 status.json 失敗。")
	}
	f := findFront(s, frontID)
	if f == nil {
		return eph(fmt.Sprintf("❌ 找不到戰線 `%s`。", frontID))
	}
	now := time.Now().UTC()
	expires := now.Add(24 * time.Hour).Format(time.RFC3339)
	f.Status = status
	f.LastUpdated = now.Format(time.RFC3339)
	f.ManualOverride = warManualOverride{Active: true, ExpiresAt: &expires}
	if err := db.saveWarRoomStatus(s); err != nil {
		log.Error("wr quick: save failed", "err", err)
		return eph("❌ 寫入 status.json 失敗。")
	}
	return discord.InteractionResponse{
		Type: discord.InteractionResponseMessage,
		Data: &discord.InteractionResponseData{
			Content: fmt.Sprintf("%s **%s** → `%s` (manual override, 有效 24h)", wrStatusEmoji(status), f.Name, status),
		},
	}
}

func (db *DiscordBot) wrShowStatus(eph func(string) discord.InteractionResponse) discord.InteractionResponse {
	s, err := db.loadWarRoomStatus()
	if err != nil {
		log.Error("wr status: load failed", "err", err)
		return eph("❌ 讀取 status.json 失敗。")
	}

	sorted := make([]warFront, len(s.Fronts))
	copy(sorted, s.Fronts)
	sort.SliceStable(sorted, func(i, j int) bool {
		return wrStatusPriority(sorted[i].Status) < wrStatusPriority(sorted[j].Status)
	})

	var sb strings.Builder
	sb.WriteString("**⚔️ War Room Status**\n")
	for _, f := range sorted {
		summary := f.Summary
		if len([]rune(summary)) > 50 {
			summary = string([]rune(summary)[:50]) + "…"
		}
		line := fmt.Sprintf("%s `%s` **%s**", wrStatusEmoji(f.Status), f.ID, f.Name)
		if summary != "" {
			line += " — " + summary
		}
		if f.Blocking != "" {
			line += "\n  🚨 " + f.Blocking
		}
		sb.WriteString(line + "\n")
	}

	return discord.InteractionResponse{
		Type: discord.InteractionResponseMessage,
		Data: &discord.InteractionResponseData{Content: sb.String(), Flags: 64},
	}
}

func wrStatusEmoji(s string) string {
	switch s {
	case "green":
		return "🟢"
	case "yellow":
		return "🟡"
	case "red":
		return "🔴"
	case "paused":
		return "⏸️"
	default:
		return "⬜"
	}
}

func wrStatusPriority(s string) int {
	switch s {
	case "red":
		return 0
	case "yellow":
		return 1
	case "green":
		return 2
	case "paused":
		return 3
	default:
		return 4
	}
}

// --- Slash Command Registration ---

// registerWrSlashCommand registers the /wr slash command with Discord.
func (db *DiscordBot) registerWrSlashCommand(appID string) {
	cmd := discord.ApplicationCommand{
		Name:        "wr",
		Description: "War Room — 管理戰線狀態",
		Options: []discord.ApplicationCommandOption{
			{
				Type:        discord.ApplicationCommandOptionSubCommand,
				Name:        "update",
				Description: "更新戰線 status + summary",
				Options: []discord.ApplicationCommandOption{
					{Type: discord.ApplicationCommandOptionString, Name: "front_id", Description: "戰線 ID", Required: true},
					{
						Type:        discord.ApplicationCommandOptionString,
						Name:        "status",
						Description: "狀態",
						Required:    true,
						Choices: []discord.ApplicationCommandChoice{
							{Name: "🟢 green", Value: "green"},
							{Name: "🟡 yellow", Value: "yellow"},
							{Name: "🔴 red", Value: "red"},
							{Name: "⬜ unknown", Value: "unknown"},
							{Name: "⏸️ paused", Value: "paused"},
						},
					},
					{Type: discord.ApplicationCommandOptionString, Name: "summary", Description: "摘要", Required: true},
				},
			},
			{
				Type:        discord.ApplicationCommandOptionSubCommand,
				Name:        "block",
				Description: "設定阻礙事項（自動設 red）",
				Options: []discord.ApplicationCommandOption{
					{Type: discord.ApplicationCommandOptionString, Name: "front_id", Description: "戰線 ID", Required: true},
					{Type: discord.ApplicationCommandOptionString, Name: "blocking_text", Description: "阻礙描述", Required: true},
				},
			},
			{
				Type:        discord.ApplicationCommandOptionSubCommand,
				Name:        "green",
				Description: "快速設為 green（manual override 24h）",
				Options: []discord.ApplicationCommandOption{
					{Type: discord.ApplicationCommandOptionString, Name: "front_id", Description: "戰線 ID", Required: true},
				},
			},
			{
				Type:        discord.ApplicationCommandOptionSubCommand,
				Name:        "red",
				Description: "快速設為 red（manual override 24h）",
				Options: []discord.ApplicationCommandOption{
					{Type: discord.ApplicationCommandOptionString, Name: "front_id", Description: "戰線 ID", Required: true},
				},
			},
			{
				Type:        discord.ApplicationCommandOptionSubCommand,
				Name:        "status",
				Description: "顯示所有戰線摘要",
			},
		},
	}
	if err := db.api.RegisterGlobalCommand(appID, cmd); err != nil {
		log.Error("wr: slash command registration failed", "err", err)
	} else {
		log.Info("wr: slash command registered", "appID", appID)
	}
}
