package main

import (
	"fmt"
	"strings"
	"time"

	"tetora/internal/discord"
	"tetora/internal/history"
)

// --- Commands ---

func (db *DiscordBot) handleCommand(msg discord.Message, cmdText string) {
	parts := strings.SplitN(cmdText, " ", 2)
	command := strings.ToLower(parts[0])
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}

	switch command {
	case "status":
		db.cmdStatus(msg)
	case "jobs", "cron":
		db.cmdJobs(msg)
	case "cost":
		db.cmdCost(msg)
	case "model":
		db.cmdModel(msg, args)
	case "local":
		db.cmdLocal(msg, args)
	case "cloud":
		db.cmdCloud(msg, args)
	case "mode":
		db.cmdMode(msg)
	case "new":
		db.cmdNewSession(msg)
	case "cancel":
		db.cmdCancel(msg)
	case "chat":
		if args == "" {
			db.sendMessage(msg.ChannelID, "Usage: `!chat <agent-name>`")
		} else {
			db.cmdChat(msg, strings.Fields(args)[0])
		}
	case "end":
		db.cmdEnd(msg)
	case "ask":
		if args == "" {
			db.sendMessage(msg.ChannelID, "Usage: `!ask <prompt>`")
		} else {
			db.cmdAsk(msg, args)
		}
	case "approve":
		db.cmdApprove(msg, args)
	case "term", "terminal":
		if db.terminal != nil {
			db.terminal.handleTermCommand(msg, args)
		} else {
			db.sendMessage(msg.ChannelID, "Terminal bridge is not enabled.")
		}
	case "version", "ver":
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Tetora v%s", tetoraVersion))
	case "help":
		db.cmdHelp(msg)
	default:
		if args != "" {
			db.handleRoute(msg, cmdText)
		} else {
			db.sendMessage(msg.ChannelID, "Unknown command `!"+command+"`. Use `!help` for available commands.")
		}
	}
}

func (db *DiscordBot) cmdStatus(msg discord.Message) {
	running := 0
	if db.state != nil {
		db.state.mu.Lock()
		running = len(db.state.running)
		db.state.mu.Unlock()
	}
	jobs := 0
	if db.cron != nil {
		jobs = len(db.cron.ListJobs())
	}
	db.sendEmbed(msg.ChannelID, discord.Embed{
		Title: "Tetora Status",
		Color: 0x5865F2,
		Fields: []discord.EmbedField{
			{Name: "Version", Value: "v" + tetoraVersion, Inline: true},
			{Name: "Running", Value: fmt.Sprintf("%d", running), Inline: true},
			{Name: "Cron Jobs", Value: fmt.Sprintf("%d", jobs), Inline: true},
		},
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

func (db *DiscordBot) cmdJobs(msg discord.Message) {
	if db.cron == nil {
		db.sendMessage(msg.ChannelID, "Cron engine not available.")
		return
	}
	jobs := db.cron.ListJobs()
	if len(jobs) == 0 {
		db.sendMessage(msg.ChannelID, "No cron jobs configured.")
		return
	}
	var fields []discord.EmbedField
	for _, j := range jobs {
		status := "enabled"
		if !j.Enabled {
			status = "disabled"
		}
		fields = append(fields, discord.EmbedField{
			Name: j.Name, Value: fmt.Sprintf("`%s` [%s]", j.Schedule, status), Inline: true,
		})
	}
	db.sendEmbed(msg.ChannelID, discord.Embed{
		Title: fmt.Sprintf("Cron Jobs (%d)", len(jobs)), Color: 0x57F287, Fields: fields,
	})
}

func (db *DiscordBot) cmdCost(msg discord.Message) {
	dbPath := db.cfg.HistoryDB
	if dbPath == "" {
		db.sendMessage(msg.ChannelID, "History DB not configured.")
		return
	}
	stats, err := history.QueryCostStats(dbPath)
	if err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
		return
	}
	db.sendEmbed(msg.ChannelID, discord.Embed{
		Title: "Cost Summary",
		Color: 0xFEE75C,
		Fields: []discord.EmbedField{
			{Name: "Today", Value: fmt.Sprintf("$%.4f", stats.Today), Inline: true},
			{Name: "This Week", Value: fmt.Sprintf("$%.4f", stats.Week), Inline: true},
			{Name: "This Month", Value: fmt.Sprintf("$%.4f", stats.Month), Inline: true},
		},
	})
}

func (db *DiscordBot) cmdHelp(msg discord.Message) {
	db.sendEmbed(msg.ChannelID, discord.Embed{
		Title:       "Tetora Help",
		Description: "Mention me with a message to route it to the best agent, or use commands:",
		Color:       0x5865F2,
		Fields: []discord.EmbedField{
			{Name: "!status", Value: "Show daemon status"},
			{Name: "!jobs", Value: "List cron jobs"},
			{Name: "!cost", Value: "Show cost summary"},
			{Name: "!model [model] [agent]", Value: "Show/switch model"},
			{Name: "!model pick [agent]", Value: "Interactive model picker"},
			{Name: "!local [agent]", Value: "Switch to local models (Ollama)"},
			{Name: "!cloud [agent]", Value: "Switch back to cloud models"},
			{Name: "!mode", Value: "Show inference mode summary"},
			{Name: "!new", Value: "Start a new session (clear context)"},
			{Name: "!cancel", Value: "Cancel all running tasks"},
			{Name: "!chat <agent>", Value: "Lock this channel to an agent (skip dispatch)"},
			{Name: "!end", Value: "Unlock channel, resume smart dispatch"},
			{Name: "!ask <prompt>", Value: "Quick question (no routing, no session)"},
			{Name: "!approve [tool|reset]", Value: "Manage auto-approved tools"},
			{Name: "!help", Value: "Show this help"},
			{Name: "Free text", Value: "Mention me + your prompt for smart dispatch"},
		},
	})
}

func (db *DiscordBot) cmdApprove(msg discord.Message, args string) {
	if db.approvalGate == nil {
		db.sendMessage(msg.ChannelID, "Approval gates are not enabled.")
		return
	}

	args = strings.TrimSpace(args)
	if args == "" {
		// List auto-approved tools.
		db.approvalGate.mu.Lock()
		var tools []string
		for t := range db.approvalGate.autoApproved {
			tools = append(tools, "`"+t+"`")
		}
		db.approvalGate.mu.Unlock()
		if len(tools) == 0 {
			db.sendMessage(msg.ChannelID, "No auto-approved tools.")
		} else {
			db.sendMessage(msg.ChannelID, "Auto-approved tools: "+strings.Join(tools, ", "))
		}
		return
	}

	if args == "reset" {
		db.approvalGate.mu.Lock()
		db.approvalGate.autoApproved = make(map[string]bool)
		db.approvalGate.mu.Unlock()
		db.sendMessage(msg.ChannelID, "Cleared all auto-approved tools.")
		return
	}

	// Add tool to auto-approved list.
	db.approvalGate.AutoApprove(args)
	db.sendMessage(msg.ChannelID, fmt.Sprintf("Auto-approved `%s` for this runtime.", args))
}
