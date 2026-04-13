package main

import (
	"sync"
	"time"

	"tetora/internal/discord"
)

// --- Interaction State ---

// discordInteractionState tracks pending interactions for follow-up.
type discordInteractionState struct {
	mu           sync.Mutex
	pending      map[string]*pendingInteraction
	cleanupEvery time.Duration
}

type pendingInteraction struct {
	CustomID      string
	ChannelID     string
	UserID        string
	CreatedAt     time.Time
	Callback      func(data discord.InteractionData)
	AllowedIDs    []string                    // restrict to specific user IDs (empty = allow all)
	Reusable      bool                        // if true, don't remove after first use
	ModalResponse *discord.InteractionResponse // if set, respond with this modal instead of deferred update
	Response      *discord.InteractionResponse // if set, use this instead of deferred update (e.g. type 7 message update)
}

func newDiscordInteractionState() *discordInteractionState {
	s := &discordInteractionState{
		pending:      make(map[string]*pendingInteraction),
		cleanupEvery: 30 * time.Minute,
	}
	go s.cleanupLoop()
	return s
}

func (s *discordInteractionState) register(pi *pendingInteraction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[pi.CustomID] = pi
}

func (s *discordInteractionState) lookup(customID string) *pendingInteraction {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending[customID]
}

func (s *discordInteractionState) remove(customID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, customID)
}

func (s *discordInteractionState) cleanupLoop() {
	ticker := time.NewTicker(s.cleanupEvery)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		cutoff := time.Now().Add(-1 * time.Hour)
		for k, v := range s.pending {
			if v.CreatedAt.Before(cutoff) {
				delete(s.pending, k)
			}
		}
		s.mu.Unlock()
	}
}

// --- Component Builder Aliases (canonical implementations in internal/discord) ---

var (
	discordActionRow       = discord.ActionRow
	discordButton          = discord.Button
	discordLinkButton      = discord.LinkButton
	discordSelectMenu      = discord.SelectMenu
	discordMultiSelectMenu = discord.MultiSelectMenu
	discordUserSelect      = discord.UserSelect
	discordRoleSelect      = discord.RoleSelect
	discordChannelSelect   = discord.ChannelSelect
	discordTextInput       = discord.TextInput
	discordParagraphInput  = discord.ParagraphInput
	discordBuildModal      = discord.BuildModal
	discordApprovalButtons = discord.ApprovalButtons
	discordAgentSelectMenu = discord.AgentSelectMenu
)

var (
	verifyDiscordSignature = discord.VerifySignature
	interactionUserID      = discord.InteractionUserID
	extractModalValues     = discord.ExtractModalValues
	runCallbackWithTimeout = discord.RunCallbackWithTimeout
)
