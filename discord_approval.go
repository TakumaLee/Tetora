package main

import (
	"context"
	"fmt"
	"sync"

	"tetora/internal/discord"
)

// --- P28.0: Discord Approval Gate ---

// discordApprovalGate implements ApprovalGate via Discord button components.
type discordApprovalGate struct {
	bot          *DiscordBot
	channelID    string
	mu           sync.Mutex
	pending      map[string]chan bool
	autoApproved map[string]bool // tool name → always approved
}

func newDiscordApprovalGate(bot *DiscordBot, channelID string) *discordApprovalGate {
	g := &discordApprovalGate{
		bot:          bot,
		channelID:    channelID,
		pending:      make(map[string]chan bool),
		autoApproved: make(map[string]bool),
	}
	// Copy config-level auto-approve tools.
	for _, tool := range bot.cfg.ApprovalGates.AutoApproveTools {
		g.autoApproved[tool] = true
	}
	return g
}

func (g *discordApprovalGate) AutoApprove(toolName string) {
	g.mu.Lock()
	g.autoApproved[toolName] = true
	g.mu.Unlock()
}

func (g *discordApprovalGate) IsAutoApproved(toolName string) bool {
	g.mu.Lock()
	ok := g.autoApproved[toolName]
	g.mu.Unlock()
	return ok
}

func (g *discordApprovalGate) RequestApproval(ctx context.Context, req ApprovalRequest) (bool, error) {
	ch := make(chan bool, 1)
	g.mu.Lock()
	g.pending[req.ID] = ch
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.pending, req.ID)
		g.mu.Unlock()
	}()

	text := fmt.Sprintf("**Approval needed**\n\nTool: `%s`\n%s", req.Tool, req.Summary)
	components := []discord.Component{{
		Type: discord.ComponentTypeActionRow,
		Components: []discord.Component{
			{Type: discord.ComponentTypeButton, Style: discord.ButtonStyleSuccess, Label: "Approve", CustomID: "gate_approve:" + req.ID},
			{Type: discord.ComponentTypeButton, Style: discord.ButtonStylePrimary, Label: "Always", CustomID: "gate_always:" + req.ID + ":" + req.Tool},
			{Type: discord.ComponentTypeButton, Style: discord.ButtonStyleDanger, Label: "Reject", CustomID: "gate_reject:" + req.ID},
		},
	}}
	g.bot.sendMessageWithComponents(g.channelID, text, components)

	select {
	case approved := <-ch:
		return approved, nil
	case <-ctx.Done():
		return false, fmt.Errorf("approval timed out: %v", ctx.Err())
	}
}

func (g *discordApprovalGate) handleGateCallback(reqID string, approved bool) {
	g.mu.Lock()
	ch, ok := g.pending[reqID]
	g.mu.Unlock()
	if ok {
		select {
		case ch <- approved:
		default:
		}
	}
}
