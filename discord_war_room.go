package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tetora/internal/discord"
	"tetora/internal/httpapi"
	"tetora/internal/log"
	"tetora/internal/warroom"
)

// warFrontSummary is a display-only projection of a front used by wrShowStatus.
// Writes go through warroom.UpdateFrontFields so unknown fields are preserved
// byte-for-byte; this struct is never marshalled back to disk.
type warFrontSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Summary  string `json:"summary"`
	Blocking string `json:"blocking"`
}

// findFrontIndex returns the index of the front with the given ID, or -1.
func findFrontIndex(s *warroom.Status, id string) int {
	for i, raw := range s.Fronts {
		fid, err := warroom.FrontID(raw)
		if err == nil && fid == id {
			return i
		}
	}
	return -1
}

// frontName extracts the display name from a raw front (falls back to id).
func frontName(raw json.RawMessage, fallback string) string {
	var name string
	if err := warroom.FrontField(raw, "name", &name); err == nil && name != "" {
		return name
	}
	return fallback
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

	case "intel":
		frontID, note := opts["front_id"], opts["note"]
		if frontID == "" || note == "" {
			return eph("❌ 缺少必要參數。")
		}
		return db.wrIntel(frontID, note, eph)

	default:
		return eph("❌ 未知子命令：" + sub.Name)
	}
}

// wrIntel appends an intel bullet to the front's md living document.
func (db *DiscordBot) wrIntel(frontID, note string, eph func(string) discord.InteractionResponse) discord.InteractionResponse {
	wsDir := db.cfg.WorkspaceDir
	if wsDir == "" {
		wsDir = filepath.Join(db.cfg.BaseDir, "workspace")
	}
	bullet, err := httpapi.AppendIntel(wsDir, frontID, note)
	if err != nil {
		switch {
		case errors.Is(err, httpapi.ErrInvalidFrontID):
			return eph(fmt.Sprintf("❌ 無效的 front_id：`%s`", frontID))
		case errors.Is(err, httpapi.ErrInvalidNote):
			return eph("❌ note 不能為空或超過 4096 字元。")
		default:
			log.Error("wr intel: append failed", "front_id", frontID, "err", err)
			return eph("❌ 寫入 intel 失敗。")
		}
	}
	return discord.InteractionResponse{
		Type: discord.InteractionResponseMessage,
		Data: &discord.InteractionResponseData{
			Content: fmt.Sprintf("📥 **%s** intel 已記錄\n%s", frontID, bullet),
		},
	}
}

// mutateFront loads status.json under warroom.Mu, applies fields to the front
// matching frontID, and saves atomically. Returns the front's display name (or
// frontID if absent) on success. mutate may return nil fields to skip saving.
func (db *DiscordBot) mutateFront(frontID string, mutate func(raw json.RawMessage) (map[string]any, error)) (string, error) {
	statusPath := warroom.StatusPath(db.cfg.BaseDir)

	warroom.Mu.Lock()
	defer warroom.Mu.Unlock()

	s, err := warroom.LoadStatus(statusPath)
	if err != nil {
		return "", fmt.Errorf("load: %w", err)
	}
	idx := findFrontIndex(s, frontID)
	if idx < 0 {
		return "", fmt.Errorf("front not found: %s", frontID)
	}
	raw := s.Fronts[idx]
	name := frontName(raw, frontID)

	fields, err := mutate(raw)
	if err != nil {
		return name, err
	}
	if fields == nil {
		return name, nil
	}

	newRaw, err := warroom.UpdateFrontFields(raw, fields)
	if err != nil {
		return name, fmt.Errorf("merge fields: %w", err)
	}
	s.Fronts[idx] = newRaw

	if err := warroom.SaveStatus(statusPath, s); err != nil {
		return name, fmt.Errorf("save: %w", err)
	}
	return name, nil
}

func (db *DiscordBot) wrUpdate(frontID, status, summary string, eph func(string) discord.InteractionResponse) discord.InteractionResponse {
	name, err := db.mutateFront(frontID, func(raw json.RawMessage) (map[string]any, error) {
		return map[string]any{
			"status":       status,
			"summary":      summary,
			"last_updated": time.Now().UTC().Format(time.RFC3339),
			"manual_override": map[string]any{
				"active": false,
			},
		}, nil
	})
	if err != nil {
		log.Error("wr update: mutate failed", "front_id", frontID, "err", err)
		if strings.HasPrefix(err.Error(), "front not found") {
			return eph(fmt.Sprintf("❌ 找不到戰線 `%s`。", frontID))
		}
		return eph("❌ 讀寫 status.json 失敗。")
	}
	return discord.InteractionResponse{
		Type: discord.InteractionResponseMessage,
		Data: &discord.InteractionResponseData{
			Content: fmt.Sprintf("%s **%s** → `%s`\n> %s", wrStatusEmoji(status), name, status, summary),
		},
	}
}

func (db *DiscordBot) wrBlock(frontID, blockingText string, eph func(string) discord.InteractionResponse) discord.InteractionResponse {
	name, err := db.mutateFront(frontID, func(raw json.RawMessage) (map[string]any, error) {
		return map[string]any{
			"status":       "red",
			"blocking":     blockingText,
			"last_updated": time.Now().UTC().Format(time.RFC3339),
		}, nil
	})
	if err != nil {
		log.Error("wr block: mutate failed", "front_id", frontID, "err", err)
		if strings.HasPrefix(err.Error(), "front not found") {
			return eph(fmt.Sprintf("❌ 找不到戰線 `%s`。", frontID))
		}
		return eph("❌ 讀寫 status.json 失敗。")
	}
	return discord.InteractionResponse{
		Type: discord.InteractionResponseMessage,
		Data: &discord.InteractionResponseData{
			Content: fmt.Sprintf("🚨 **%s** blocked\n> %s", name, blockingText),
		},
	}
}

func (db *DiscordBot) wrQuickStatus(frontID, status string, eph func(string) discord.InteractionResponse) discord.InteractionResponse {
	now := time.Now().UTC()
	expires := now.Add(24 * time.Hour).Format(time.RFC3339)
	name, err := db.mutateFront(frontID, func(raw json.RawMessage) (map[string]any, error) {
		return map[string]any{
			"status":       status,
			"last_updated": now.Format(time.RFC3339),
			"manual_override": map[string]any{
				"active":     true,
				"expires_at": expires,
			},
		}, nil
	})
	if err != nil {
		log.Error("wr quick: mutate failed", "front_id", frontID, "err", err)
		if strings.HasPrefix(err.Error(), "front not found") {
			return eph(fmt.Sprintf("❌ 找不到戰線 `%s`。", frontID))
		}
		return eph("❌ 讀寫 status.json 失敗。")
	}
	return discord.InteractionResponse{
		Type: discord.InteractionResponseMessage,
		Data: &discord.InteractionResponseData{
			Content: fmt.Sprintf("%s **%s** → `%s` (manual override, 有效 24h)", wrStatusEmoji(status), name, status),
		},
	}
}

func (db *DiscordBot) wrShowStatus(eph func(string) discord.InteractionResponse) discord.InteractionResponse {
	statusPath := warroom.StatusPath(db.cfg.BaseDir)

	warroom.Mu.Lock()
	s, err := warroom.LoadStatus(statusPath)
	warroom.Mu.Unlock()
	if err != nil {
		log.Error("wr status: load failed", "err", err)
		return eph("❌ 讀取 status.json 失敗。")
	}

	projected := make([]warFrontSummary, 0, len(s.Fronts))
	for _, raw := range s.Fronts {
		var f warFrontSummary
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		projected = append(projected, f)
	}
	sort.SliceStable(projected, func(i, j int) bool {
		return wrStatusPriority(projected[i].Status) < wrStatusPriority(projected[j].Status)
	})

	var sb strings.Builder
	sb.WriteString("**⚔️ War Room Status**\n")
	for _, f := range projected {
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
			{
				Type:        discord.ApplicationCommandOptionSubCommand,
				Name:        "intel",
				Description: "餵一條 intel（觀察/文章/tip）到戰線",
				Options: []discord.ApplicationCommandOption{
					{Type: discord.ApplicationCommandOptionString, Name: "front_id", Description: "戰線 ID", Required: true},
					{Type: discord.ApplicationCommandOptionString, Name: "note", Description: "intel 內容（最多 4096 字）", Required: true},
				},
			},
		},
	}
	if err := db.api.RegisterGlobalCommand(appID, cmd); err != nil {
		log.Error("wr: slash command registration failed", "err", err)
	} else {
		log.Info("wr: slash command registered", "appID", appID)
	}
}
