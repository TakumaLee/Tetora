package discord

import (
	"fmt"
	"strings"
	"time"

	"tetora/internal/history"
)

// --- Commands ---

func (b *Bot) handleCommand(msg Message, cmdText string) {
	parts := strings.SplitN(cmdText, " ", 2)
	command := strings.ToLower(parts[0])
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}

	switch command {
	case "status":
		b.cmdStatus(msg)
	case "jobs", "cron":
		b.cmdJobs(msg)
	case "cost":
		b.cmdCost(msg)
	case "model":
		b.cmdModel(msg, args)
	case "local":
		b.cmdLocal(msg, args)
	case "cloud":
		b.cmdCloud(msg, args)
	case "mode":
		b.cmdMode(msg)
	case "new":
		b.cmdNewSession(msg)
	case "cancel":
		b.cmdCancel(msg)
	case "chat":
		if args == "" {
			b.sendMessage(msg.ChannelID, "Usage: `!chat <agent-name>`")
		} else {
			b.cmdChat(msg, strings.Fields(args)[0])
		}
	case "end":
		b.cmdEnd(msg)
	case "ask":
		if args == "" {
			b.sendMessage(msg.ChannelID, "Usage: `!ask <prompt>`")
		} else {
			b.cmdAsk(msg, args)
		}
	case "approve":
		b.cmdApprove(msg, args)
	case "term", "terminal":
		if b.terminal != nil {
			b.terminal.handleTermCommand(msg, args)
		} else {
			b.sendMessage(msg.ChannelID, "Terminal bridge is not enabled.")
		}
	case "version", "ver":
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Tetora v%s", b.deps.Version))
	case "help":
		b.cmdHelp(msg)
	default:
		if args != "" {
			b.handleRoute(msg, cmdText)
		} else {
			b.sendMessage(msg.ChannelID, "Unknown command `!"+command+"`. Use `!help` for available commands.")
		}
	}
}

func (b *Bot) cmdStatus(msg Message) {
	running := 0
	if b.state != nil {
		running = b.state.RunningCount()
	}
	jobs := 0
	if b.cronEng != nil {
		jobs = len(b.cronEng.ListJobs())
	}
	b.sendEmbed(msg.ChannelID, Embed{
		Title: "Tetora Status",
		Color: 0x5865F2,
		Fields: []EmbedField{
			{Name: "Version", Value: "v" + b.deps.Version, Inline: true},
			{Name: "Running", Value: fmt.Sprintf("%d", running), Inline: true},
			{Name: "Cron Jobs", Value: fmt.Sprintf("%d", jobs), Inline: true},
		},
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

func (b *Bot) cmdJobs(msg Message) {
	if b.cronEng == nil {
		b.sendMessage(msg.ChannelID, "Cron engine not available.")
		return
	}
	jobs := b.cronEng.ListJobs()
	if len(jobs) == 0 {
		b.sendMessage(msg.ChannelID, "No cron jobs configured.")
		return
	}
	var fields []EmbedField
	for _, j := range jobs {
		status := "enabled"
		if !j.Enabled {
			status = "disabled"
		}
		fields = append(fields, EmbedField{
			Name: j.Name, Value: fmt.Sprintf("`%s` [%s]", j.Schedule, status), Inline: true,
		})
	}
	b.sendEmbed(msg.ChannelID, Embed{
		Title: fmt.Sprintf("Cron Jobs (%d)", len(jobs)), Color: 0x57F287, Fields: fields,
	})
}

func (b *Bot) cmdCost(msg Message) {
	dbPath := b.cfg.HistoryDB
	if dbPath == "" {
		b.sendMessage(msg.ChannelID, "History DB not configured.")
		return
	}
	stats, err := history.QueryCostStats(dbPath)
	if err != nil {
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
		return
	}
	b.sendEmbed(msg.ChannelID, Embed{
		Title: "Cost Summary",
		Color: 0xFEE75C,
		Fields: []EmbedField{
			{Name: "Today", Value: fmt.Sprintf("$%.4f", stats.Today), Inline: true},
			{Name: "This Week", Value: fmt.Sprintf("$%.4f", stats.Week), Inline: true},
			{Name: "This Month", Value: fmt.Sprintf("$%.4f", stats.Month), Inline: true},
		},
	})
}

func (b *Bot) cmdHelp(msg Message) {
	b.sendEmbed(msg.ChannelID, Embed{
		Title:       "Tetora Help",
		Description: "Mention me with a message to route it to the best agent, or use commands:",
		Color:       0x5865F2,
		Fields: []EmbedField{
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

func (b *Bot) cmdApprove(msg Message, args string) {
	if b.approvalGate == nil {
		b.sendMessage(msg.ChannelID, "Approval gates are not enabled.")
		return
	}

	args = strings.TrimSpace(args)
	if args == "" {
		// List auto-approved tools.
		b.approvalGate.mu.Lock()
		var tools []string
		for t := range b.approvalGate.autoApproved {
			tools = append(tools, "`"+t+"`")
		}
		b.approvalGate.mu.Unlock()
		if len(tools) == 0 {
			b.sendMessage(msg.ChannelID, "No auto-approved tools.")
		} else {
			b.sendMessage(msg.ChannelID, "Auto-approved tools: "+strings.Join(tools, ", "))
		}
		return
	}

	if args == "reset" {
		b.approvalGate.mu.Lock()
		b.approvalGate.autoApproved = make(map[string]bool)
		b.approvalGate.mu.Unlock()
		b.sendMessage(msg.ChannelID, "Cleared all auto-approved tools.")
		return
	}

	// Add tool to auto-approved list.
	b.approvalGate.AutoApprove(args)
	b.sendMessage(msg.ChannelID, fmt.Sprintf("Auto-approved `%s` for this runtime.", args))
}
