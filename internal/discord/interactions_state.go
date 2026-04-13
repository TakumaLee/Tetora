package discord

import (
	"sync"
	"time"
)

// --- Interaction State ---

// DiscordInteractionState tracks pending interactions for follow-up.
type DiscordInteractionState struct {
	mu           sync.Mutex
	pending      map[string]*pendingInteraction
	cleanupEvery time.Duration
}

type pendingInteraction struct {
	CustomID      string
	ChannelID     string
	UserID        string
	CreatedAt     time.Time
	Callback      func(data InteractionData)
	AllowedIDs    []string             // restrict to specific user IDs (empty = allow all)
	Reusable      bool                 // if true, don't remove after first use
	ModalResponse *InteractionResponse // if set, respond with this modal instead of deferred update
	Response      *InteractionResponse // if set, use this instead of deferred update (e.g. type 7 message update)
}

func NewDiscordInteractionState() *DiscordInteractionState {
	s := &DiscordInteractionState{
		pending:      make(map[string]*pendingInteraction),
		cleanupEvery: 30 * time.Minute,
	}
	go s.cleanupLoop()
	return s
}

func (s *DiscordInteractionState) register(pi *pendingInteraction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[pi.CustomID] = pi
}

func (s *DiscordInteractionState) lookup(customID string) *pendingInteraction {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending[customID]
}

func (s *DiscordInteractionState) remove(customID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, customID)
}

func (s *DiscordInteractionState) cleanupLoop() {
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

// --- Component Builder Aliases ---

var (
	discordActionRow       = ActionRow
	discordButton          = Button
	discordLinkButton      = LinkButton
	discordSelectMenu      = SelectMenu
	discordMultiSelectMenu = MultiSelectMenu
	discordUserSelect      = UserSelect
	discordRoleSelect      = RoleSelect
	discordChannelSelect   = ChannelSelect
	discordTextInput       = TextInput
	discordParagraphInput  = ParagraphInput
	discordBuildModal      = BuildModal
	discordApprovalButtons = ApprovalButtons
	discordAgentSelectMenu = AgentSelectMenu
)

var (
	verifyDiscordSignature = VerifySignature
	interactionUserID      = InteractionUserID
	extractModalValues     = ExtractModalValues
	runCallbackWithTimeout = RunCallbackWithTimeout
)
