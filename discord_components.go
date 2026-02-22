package main

// --- P14.1: Discord Components v2 ---

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// --- Interaction Types ---

const (
	// Discord interaction types.
	interactionTypePing             = 1
	interactionTypeApplicationCmd   = 2
	interactionTypeMessageComponent = 3
	interactionTypeModalSubmit      = 5

	// Discord component types.
	componentTypeActionRow       = 1
	componentTypeButton          = 2
	componentTypeStringSelect    = 3
	componentTypeTextInput       = 4
	componentTypeUserSelect      = 5
	componentTypeRoleSelect      = 6
	componentTypeMentionSelect   = 7
	componentTypeChannelSelect   = 8

	// Discord button styles.
	buttonStylePrimary   = 1
	buttonStyleSecondary = 2
	buttonStyleSuccess   = 3
	buttonStyleDanger    = 4
	buttonStyleLink      = 5

	// Discord interaction response types.
	interactionResponsePong           = 1
	interactionResponseMessage        = 4
	interactionResponseDeferredUpdate = 6
	interactionResponseUpdateMessage  = 7
	interactionResponseModal          = 9

	// Text input styles.
	textInputStyleShort     = 1
	textInputStyleParagraph = 2
)

// --- Component Structures ---

// discordComponent represents a Discord message component (button, select, text input, action row).
type discordComponent struct {
	Type        int                   `json:"type"`
	CustomID    string                `json:"custom_id,omitempty"`
	Style       int                   `json:"style,omitempty"`
	Label       string                `json:"label,omitempty"`
	Disabled    bool                  `json:"disabled,omitempty"`
	URL         string                `json:"url,omitempty"`
	Placeholder string                `json:"placeholder,omitempty"`
	MinValues   *int                  `json:"min_values,omitempty"`
	MaxValues   *int                  `json:"max_values,omitempty"`
	Options     []discordSelectOption `json:"options,omitempty"`
	Components  []discordComponent    `json:"components,omitempty"`
	Value       string                `json:"value,omitempty"`
	Required    bool                  `json:"required,omitempty"`
	MinLength   *int                  `json:"min_length,omitempty"`
	MaxLength   *int                  `json:"max_length,omitempty"`
}

// discordSelectOption represents an option in a string select menu.
type discordSelectOption struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

// discordModalData represents a modal (popup form).
type discordModalData struct {
	CustomID   string             `json:"custom_id"`
	Title      string             `json:"title"`
	Components []discordComponent `json:"components"`
}

// --- Discord Interaction ---

// discordInteraction represents an incoming Discord interaction webhook payload.
type discordInteraction struct {
	ID      string          `json:"id"`
	Type    int             `json:"type"`
	Data    json.RawMessage `json:"data,omitempty"`
	GuildID string          `json:"guild_id,omitempty"`
	Channel *struct {
		ID string `json:"id"`
	} `json:"channel,omitempty"`
	Member *struct {
		User discordUser `json:"user"`
	} `json:"member,omitempty"`
	User    *discordUser `json:"user,omitempty"`
	Message *struct {
		ID        string `json:"id"`
		ChannelID string `json:"channel_id"`
	} `json:"message,omitempty"`
	Token   string `json:"token"`
	Version int    `json:"version"`
}

// discordInteractionData represents the parsed data field of an interaction.
type discordInteractionData struct {
	CustomID   string             `json:"custom_id"`
	Values     []string           `json:"values,omitempty"`
	Components []discordComponent `json:"components,omitempty"`
	// For APPLICATION_COMMAND:
	Name string `json:"name,omitempty"`
}

// discordInteractionResponse represents a response to a Discord interaction.
type discordInteractionResponse struct {
	Type int                              `json:"type"`
	Data *discordInteractionResponseData  `json:"data,omitempty"`
}

// discordInteractionResponseData is the data field of an interaction response.
type discordInteractionResponseData struct {
	Content    string             `json:"content,omitempty"`
	Embeds     []discordEmbed     `json:"embeds,omitempty"`
	Components []discordComponent `json:"components,omitempty"`
	Flags      int                `json:"flags,omitempty"`
	// For modals:
	CustomID   string             `json:"custom_id,omitempty"`
	Title      string             `json:"title,omitempty"`
}

// --- Interaction State ---

// discordInteractionState tracks pending interactions for follow-up.
type discordInteractionState struct {
	mu           sync.Mutex
	pending      map[string]*pendingInteraction
	cleanupEvery time.Duration
}

type pendingInteraction struct {
	CustomID   string
	ChannelID  string
	UserID     string
	CreatedAt  time.Time
	Callback   func(data discordInteractionData)
	AllowedIDs []string // restrict to specific user IDs (empty = allow all)
	Reusable   bool     // if true, don't remove after first use
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

// --- Component Builders ---

// discordActionRow creates an action row containing components.
func discordActionRow(components ...discordComponent) discordComponent {
	return discordComponent{
		Type:       componentTypeActionRow,
		Components: components,
	}
}

// discordButton creates a button component.
func discordButton(customID, label string, style int) discordComponent {
	c := discordComponent{
		Type:     componentTypeButton,
		CustomID: customID,
		Label:    label,
		Style:    style,
	}
	// Link buttons don't use custom_id, they use url.
	if style == buttonStyleLink {
		c.URL = customID
		c.CustomID = ""
	}
	return c
}

// discordLinkButton creates a link button with a URL.
func discordLinkButton(url, label string) discordComponent {
	return discordComponent{
		Type:  componentTypeButton,
		Label: label,
		Style: buttonStyleLink,
		URL:   url,
	}
}

// discordSelectMenu creates a string select menu.
func discordSelectMenu(customID, placeholder string, options []discordSelectOption) discordComponent {
	return discordComponent{
		Type:        componentTypeStringSelect,
		CustomID:    customID,
		Placeholder: placeholder,
		Options:     options,
	}
}

// discordUserSelect creates a user select menu.
func discordUserSelect(customID, placeholder string) discordComponent {
	return discordComponent{
		Type:        componentTypeUserSelect,
		CustomID:    customID,
		Placeholder: placeholder,
	}
}

// discordRoleSelect creates a role select menu.
func discordRoleSelect(customID, placeholder string) discordComponent {
	return discordComponent{
		Type:        componentTypeRoleSelect,
		CustomID:    customID,
		Placeholder: placeholder,
	}
}

// discordChannelSelect creates a channel select menu.
func discordChannelSelect(customID, placeholder string) discordComponent {
	return discordComponent{
		Type:        componentTypeChannelSelect,
		CustomID:    customID,
		Placeholder: placeholder,
	}
}

// discordTextInput creates a text input for use in modals.
func discordTextInput(customID, label string, required bool) discordComponent {
	return discordComponent{
		Type:     componentTypeTextInput,
		CustomID: customID,
		Label:    label,
		Style:    textInputStyleShort,
		Required: required,
	}
}

// discordParagraphInput creates a paragraph (multi-line) text input for modals.
func discordParagraphInput(customID, label string, required bool) discordComponent {
	return discordComponent{
		Type:     componentTypeTextInput,
		CustomID: customID,
		Label:    label,
		Style:    textInputStyleParagraph,
		Required: required,
	}
}

// discordBuildModal creates a modal interaction response.
func discordBuildModal(customID, title string, components ...discordComponent) discordInteractionResponse {
	// Wrap text inputs in action rows if they aren't already.
	rows := make([]discordComponent, 0, len(components))
	for _, c := range components {
		if c.Type == componentTypeActionRow {
			rows = append(rows, c)
		} else {
			rows = append(rows, discordActionRow(c))
		}
	}
	return discordInteractionResponse{
		Type: interactionResponseModal,
		Data: &discordInteractionResponseData{
			CustomID:   customID,
			Title:      title,
			Components: rows,
		},
	}
}

// --- Ed25519 Signature Verification ---

// verifyDiscordSignature verifies a Discord interaction webhook signature.
// Discord sends X-Signature-Ed25519 (hex-encoded signature) and X-Signature-Timestamp headers.
// The signed message is timestamp + body.
func verifyDiscordSignature(publicKeyHex, signature, timestamp string, body []byte) bool {
	pubKeyBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		return false
	}

	sigBytes, err := hex.DecodeString(signature)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return false
	}

	msg := []byte(timestamp + string(body))
	return ed25519.Verify(ed25519.PublicKey(pubKeyBytes), msg, sigBytes)
}

// --- Interaction Handler ---

// handleDiscordInteraction processes incoming Discord interaction webhooks.
func handleDiscordInteraction(db *DiscordBot, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}

	// Read body.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Verify Ed25519 signature.
	publicKey := db.cfg.Discord.PublicKey
	if publicKey == "" {
		logWarn("discord interactions: no public key configured")
		http.Error(w, `{"error":"interactions not configured"}`, http.StatusServiceUnavailable)
		return
	}

	sig := r.Header.Get("X-Signature-Ed25519")
	ts := r.Header.Get("X-Signature-Timestamp")
	if sig == "" || ts == "" {
		http.Error(w, `{"error":"missing signature headers"}`, http.StatusUnauthorized)
		return
	}

	if !verifyDiscordSignature(publicKey, sig, ts, body) {
		logWarn("discord interactions: invalid signature", "ip", clientIP(r))
		http.Error(w, `{"error":"invalid signature"}`, http.StatusUnauthorized)
		return
	}

	// Parse interaction.
	var interaction discordInteraction
	if err := json.Unmarshal(body, &interaction); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	ctx := withTraceID(context.Background(), newTraceID("discord-interaction"))

	// Route by interaction type.
	switch interaction.Type {
	case interactionTypePing:
		// Respond with PONG.
		logInfoCtx(ctx, "discord interaction PING received")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(discordInteractionResponse{Type: interactionResponsePong})
		return

	case interactionTypeMessageComponent:
		handleComponentInteraction(ctx, db, w, &interaction)
		return

	case interactionTypeModalSubmit:
		handleModalSubmit(ctx, db, w, &interaction)
		return

	case interactionTypeApplicationCmd:
		// Application commands — respond with a basic message for now.
		logInfoCtx(ctx, "discord application command received")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(discordInteractionResponse{
			Type: interactionResponseMessage,
			Data: &discordInteractionResponseData{
				Content: "Command received. Use the Tetora dashboard for full functionality.",
			},
		})
		return

	default:
		logWarn("discord interactions: unknown type", "type", interaction.Type)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(discordInteractionResponse{
			Type: interactionResponseMessage,
			Data: &discordInteractionResponseData{
				Content: "Unknown interaction type.",
				Flags:   64, // ephemeral
			},
		})
	}
}

// handleComponentInteraction routes button clicks and select menu selections.
func handleComponentInteraction(ctx context.Context, db *DiscordBot, w http.ResponseWriter, interaction *discordInteraction) {
	var data discordInteractionData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		logWarnCtx(ctx, "discord component: invalid data", "error", err)
		http.Error(w, `{"error":"invalid component data"}`, http.StatusBadRequest)
		return
	}

	userID := interactionUserID(interaction)
	logInfoCtx(ctx, "discord component interaction",
		"customID", data.CustomID,
		"userID", userID,
		"values", fmt.Sprintf("%v", data.Values))

	// Check registered interaction callbacks.
	if db.interactions != nil {
		if pi := db.interactions.lookup(data.CustomID); pi != nil {
			// Check allowed users.
			if len(pi.AllowedIDs) > 0 && !sliceContainsStr(pi.AllowedIDs, userID) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(discordInteractionResponse{
					Type: interactionResponseMessage,
					Data: &discordInteractionResponseData{
						Content: "You are not allowed to use this component.",
						Flags:   64, // ephemeral
					},
				})
				return
			}

			// Fire callback in background.
			if pi.Callback != nil {
				go pi.Callback(data)
			}

			// Remove if not reusable.
			if !pi.Reusable {
				db.interactions.remove(data.CustomID)
			}

			// Acknowledge.
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(discordInteractionResponse{
				Type: interactionResponseDeferredUpdate,
			})
			return
		}
	}

	// Default: handle common built-in custom_id patterns.
	response := handleBuiltinComponent(ctx, db, data, userID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleModalSubmit processes modal form submissions.
func handleModalSubmit(ctx context.Context, db *DiscordBot, w http.ResponseWriter, interaction *discordInteraction) {
	var data discordInteractionData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		logWarnCtx(ctx, "discord modal: invalid data", "error", err)
		http.Error(w, `{"error":"invalid modal data"}`, http.StatusBadRequest)
		return
	}

	userID := interactionUserID(interaction)
	logInfoCtx(ctx, "discord modal submit",
		"customID", data.CustomID,
		"userID", userID)

	// Extract modal field values.
	values := extractModalValues(data.Components)

	// Check registered interaction callbacks.
	if db.interactions != nil {
		if pi := db.interactions.lookup(data.CustomID); pi != nil {
			if len(pi.AllowedIDs) > 0 && !sliceContainsStr(pi.AllowedIDs, userID) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(discordInteractionResponse{
					Type: interactionResponseMessage,
					Data: &discordInteractionResponseData{
						Content: "You are not allowed to submit this form.",
						Flags:   64,
					},
				})
				return
			}

			if pi.Callback != nil {
				go pi.Callback(data)
			}
			db.interactions.remove(data.CustomID)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(discordInteractionResponse{
				Type: interactionResponseMessage,
				Data: &discordInteractionResponseData{
					Content: "Form submitted successfully.",
					Flags:   64,
				},
			})
			return
		}
	}

	// Default response for unhandled modals.
	logInfoCtx(ctx, "discord modal unhandled", "customID", data.CustomID, "values", fmt.Sprintf("%v", values))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(discordInteractionResponse{
		Type: interactionResponseMessage,
		Data: &discordInteractionResponseData{
			Content: fmt.Sprintf("Form received (%d fields).", len(values)),
			Flags:   64,
		},
	})
}

// --- Built-in Component Handlers ---

// handleBuiltinComponent handles common built-in component custom_id patterns.
func handleBuiltinComponent(ctx context.Context, db *DiscordBot, data discordInteractionData, userID string) discordInteractionResponse {
	customID := data.CustomID

	// Pattern: "approve:{taskID}" / "reject:{taskID}"
	if strings.HasPrefix(customID, "approve:") {
		taskID := strings.TrimPrefix(customID, "approve:")
		logInfoCtx(ctx, "discord component: task approved", "taskID", taskID, "userID", userID)
		auditLog(db.cfg.HistoryDB, "discord.component.approve", "discord",
			fmt.Sprintf("task=%s user=%s", taskID, userID), "")
		return discordInteractionResponse{
			Type: interactionResponseUpdateMessage,
			Data: &discordInteractionResponseData{
				Content: fmt.Sprintf("Task `%s` approved by <@%s>.", truncate(taskID, 8), userID),
			},
		}
	}

	if strings.HasPrefix(customID, "reject:") {
		taskID := strings.TrimPrefix(customID, "reject:")
		logInfoCtx(ctx, "discord component: task rejected", "taskID", taskID, "userID", userID)
		auditLog(db.cfg.HistoryDB, "discord.component.reject", "discord",
			fmt.Sprintf("task=%s user=%s", taskID, userID), "")
		return discordInteractionResponse{
			Type: interactionResponseUpdateMessage,
			Data: &discordInteractionResponseData{
				Content: fmt.Sprintf("Task `%s` rejected by <@%s>.", truncate(taskID, 8), userID),
			},
		}
	}

	// Pattern: "agent_select" — route to selected agent.
	if customID == "agent_select" && len(data.Values) > 0 {
		agent := data.Values[0]
		logInfoCtx(ctx, "discord component: agent selected", "agent", agent, "userID", userID)
		return discordInteractionResponse{
			Type: interactionResponseMessage,
			Data: &discordInteractionResponseData{
				Content: fmt.Sprintf("Routing to agent **%s**...", agent),
			},
		}
	}

	// Unknown component.
	logInfoCtx(ctx, "discord component: unhandled", "customID", customID)
	return discordInteractionResponse{
		Type: interactionResponseDeferredUpdate,
	}
}

// --- Helpers ---

// interactionUserID extracts the user ID from an interaction (guild or DM).
func interactionUserID(i *discordInteraction) string {
	if i.Member != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// extractModalValues extracts field values from modal submit components.
// Modal components are action rows containing text inputs.
func extractModalValues(components []discordComponent) map[string]string {
	values := make(map[string]string)
	for _, row := range components {
		if row.Type == componentTypeActionRow {
			for _, field := range row.Components {
				if field.CustomID != "" {
					values[field.CustomID] = field.Value
				}
			}
		}
	}
	return values
}

// sliceContainsStr checks if a string slice contains a value.
func sliceContainsStr(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// --- Convenience: Task Approval Components ---

// discordApprovalButtons creates approve/reject buttons for a task.
func discordApprovalButtons(taskID string) []discordComponent {
	return []discordComponent{
		discordActionRow(
			discordButton("approve:"+taskID, "Approve", buttonStyleSuccess),
			discordButton("reject:"+taskID, "Reject", buttonStyleDanger),
		),
	}
}

// discordAgentSelectMenu creates a select menu for choosing an agent.
func discordAgentSelectMenu(agents []string) []discordComponent {
	options := make([]discordSelectOption, len(agents))
	for i, a := range agents {
		options[i] = discordSelectOption{Label: a, Value: a}
	}
	return []discordComponent{
		discordActionRow(
			discordSelectMenu("agent_select", "Select an agent...", options),
		),
	}
}
