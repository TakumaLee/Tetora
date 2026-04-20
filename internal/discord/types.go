package discord

import "encoding/json"

// --- Gateway Types ---

type GatewayPayload struct {
	Op int              `json:"op"`
	D  json.RawMessage  `json:"d,omitempty"`
	S  *int             `json:"s,omitempty"`
	T  string           `json:"t,omitempty"`
}

type HelloData struct {
	HeartbeatInterval int `json:"heartbeat_interval"`
}

type IdentifyData struct {
	Token      string            `json:"token"`
	Intents    int               `json:"intents"`
	Properties map[string]string `json:"properties"`
}

type ResumePayload struct {
	Token     string `json:"token"`
	SessionID string `json:"session_id"`
	Seq       int    `json:"seq"`
}

type ReadyData struct {
	SessionID string `json:"session_id"`
	User      User   `json:"user"`
}

// --- API Types ---

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Bot      bool   `json:"bot"`
}

type Attachment struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
}

type Message struct {
	ID          string       `json:"id"`
	ChannelID   string       `json:"channel_id"`
	GuildID     string       `json:"guild_id,omitempty"`
	Author      User         `json:"author"`
	Content     string       `json:"content"`
	Mentions    []User       `json:"mentions,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type Embed struct {
	Title       string       `json:"title,omitempty"`
	URL         string       `json:"url,omitempty"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color,omitempty"`
	Fields      []EmbedField `json:"fields,omitempty"`
	Footer      *EmbedFooter `json:"footer,omitempty"`
	Timestamp   string       `json:"timestamp,omitempty"`
}

type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type EmbedFooter struct {
	Text string `json:"text"`
}

type MessageRef struct {
	MessageID       string `json:"message_id"`
	FailIfNotExists bool   `json:"fail_if_not_exists"`
}

// --- Component Types ---

// Component type constants.
const (
	ComponentTypeActionRow     = 1
	ComponentTypeButton        = 2
	ComponentTypeStringSelect  = 3
	ComponentTypeTextInput     = 4
	ComponentTypeUserSelect    = 5
	ComponentTypeRoleSelect    = 6
	ComponentTypeMentionSelect = 7
	ComponentTypeChannelSelect = 8
)

// Button style constants.
const (
	ButtonStylePrimary   = 1
	ButtonStyleSecondary = 2
	ButtonStyleSuccess   = 3
	ButtonStyleDanger    = 4
	ButtonStyleLink      = 5
)

// Interaction type constants.
const (
	InteractionTypePing             = 1
	InteractionTypeApplicationCmd   = 2
	InteractionTypeMessageComponent = 3
	InteractionTypeModalSubmit      = 5
)

// Interaction response type constants.
const (
	InteractionResponsePong           = 1
	InteractionResponseMessage        = 4
	InteractionResponseDeferredUpdate = 6
	InteractionResponseUpdateMessage  = 7
	InteractionResponseModal          = 9
)

// Text input style constants.
const (
	TextInputStyleShort     = 1
	TextInputStyleParagraph = 2
)

// Component represents a Discord message component (button, select, text input, action row).
type Component struct {
	Type        int            `json:"type"`
	CustomID    string         `json:"custom_id,omitempty"`
	Style       int            `json:"style,omitempty"`
	Label       string         `json:"label,omitempty"`
	Disabled    bool           `json:"disabled,omitempty"`
	URL         string         `json:"url,omitempty"`
	Placeholder string         `json:"placeholder,omitempty"`
	MinValues   *int           `json:"min_values,omitempty"`
	MaxValues   *int           `json:"max_values,omitempty"`
	Options     []SelectOption `json:"options,omitempty"`
	Components  []Component    `json:"components,omitempty"`
	Value       string         `json:"value,omitempty"`
	Required    bool           `json:"required,omitempty"`
	MinLength   *int           `json:"min_length,omitempty"`
	MaxLength   *int           `json:"max_length,omitempty"`
}

// SelectOption represents an option in a string select menu.
type SelectOption struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

// ModalData represents a modal (popup form).
type ModalData struct {
	CustomID   string      `json:"custom_id"`
	Title      string      `json:"title"`
	Components []Component `json:"components"`
}

// Interaction represents an incoming Discord interaction webhook payload.
type Interaction struct {
	ID      string          `json:"id"`
	Type    int             `json:"type"`
	Data    json.RawMessage `json:"data,omitempty"`
	GuildID string          `json:"guild_id,omitempty"`
	Channel *struct {
		ID string `json:"id"`
	} `json:"channel,omitempty"`
	Member *struct {
		User User `json:"user"`
	} `json:"member,omitempty"`
	User    *User `json:"user,omitempty"`
	Message *struct {
		ID        string `json:"id"`
		ChannelID string `json:"channel_id"`
	} `json:"message,omitempty"`
	Token   string `json:"token"`
	Version int    `json:"version"`
}

// InteractionData represents the parsed data field of an interaction.
type InteractionData struct {
	CustomID   string      `json:"custom_id"`
	Values     []string    `json:"values,omitempty"`
	Components []Component `json:"components,omitempty"`
	// For APPLICATION_COMMAND:
	Name string `json:"name,omitempty"`
}

// Application command option type constants.
const (
	ApplicationCommandOptionSubCommand = 1
	ApplicationCommandOptionString     = 3
)

// ApplicationCommand defines a Discord application (slash) command for registration.
type ApplicationCommand struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Options     []ApplicationCommandOption `json:"options,omitempty"`
}

// ApplicationCommandOption is a subcommand or parameter definition.
type ApplicationCommandOption struct {
	Type        int                        `json:"type"`
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Required    bool                       `json:"required,omitempty"`
	Choices     []ApplicationCommandChoice `json:"choices,omitempty"`
	Options     []ApplicationCommandOption `json:"options,omitempty"`
}

// ApplicationCommandChoice is a fixed choice for a string option.
type ApplicationCommandChoice struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SlashCommandData is the parsed "data" field of a slash command interaction.
type SlashCommandData struct {
	Name    string               `json:"name"`
	Options []SlashCommandOption `json:"options,omitempty"`
}

// SlashCommandOption represents a resolved option in an incoming slash command.
type SlashCommandOption struct {
	Name    string               `json:"name"`
	Type    int                  `json:"type"`
	Value   string               `json:"value,omitempty"`
	Options []SlashCommandOption `json:"options,omitempty"`
}

// OptionMap returns a map from option name to string value for quick access.
func (o SlashCommandOption) OptionMap() map[string]string {
	m := make(map[string]string, len(o.Options))
	for _, opt := range o.Options {
		m[opt.Name] = opt.Value
	}
	return m
}

// InteractionResponse represents a response to a Discord interaction.
type InteractionResponse struct {
	Type int                      `json:"type"`
	Data *InteractionResponseData `json:"data,omitempty"`
}

// InteractionResponseData is the data field of an interaction response.
type InteractionResponseData struct {
	Content    string      `json:"content,omitempty"`
	Embeds     []Embed     `json:"embeds,omitempty"`
	Components []Component `json:"components,omitempty"`
	Flags      int         `json:"flags,omitempty"`
	// For modals:
	CustomID string `json:"custom_id,omitempty"`
	Title    string `json:"title,omitempty"`
}
