package main

import "tetora/internal/discord"

// Type aliases — canonical definitions in internal/discord.
type discordUser = discord.User
type discordAttachment = discord.Attachment
type discordMessage = discord.Message
type discordEmbed = discord.Embed
type discordEmbedField = discord.EmbedField
type discordEmbedFooter = discord.EmbedFooter
type discordMessageRef = discord.MessageRef
type discordComponent = discord.Component
type discordSelectOption = discord.SelectOption
type discordModalData = discord.ModalData
type discordInteraction = discord.Interaction
type discordInteractionData = discord.InteractionData
type discordInteractionResponse = discord.InteractionResponse
type discordInteractionResponseData = discord.InteractionResponseData
type gatewayPayload = discord.GatewayPayload
type helloData = discord.HelloData
type identifyData = discord.IdentifyData
type resumePayload = discord.ResumePayload
type readyData = discord.ReadyData

// Constant aliases.
const (
	discordGatewayURL = discord.GatewayURL
	discordAPIBase    = discord.APIBase

	opDispatch       = discord.OpDispatch
	opHeartbeat      = discord.OpHeartbeat
	opIdentify       = discord.OpIdentify
	opResume         = discord.OpResume
	opReconnect      = discord.OpReconnect
	opInvalidSession = discord.OpInvalidSession
	opHello          = discord.OpHello
	opHeartbeatAck   = discord.OpHeartbeatAck

	intentGuildMessages  = discord.IntentGuildMessages
	intentDirectMessages = discord.IntentDirectMessages
	intentMessageContent = discord.IntentMessageContent

	interactionTypePing             = discord.InteractionTypePing
	interactionTypeApplicationCmd   = discord.InteractionTypeApplicationCmd
	interactionTypeMessageComponent = discord.InteractionTypeMessageComponent
	interactionTypeModalSubmit      = discord.InteractionTypeModalSubmit

	componentTypeActionRow     = discord.ComponentTypeActionRow
	componentTypeButton        = discord.ComponentTypeButton
	componentTypeStringSelect  = discord.ComponentTypeStringSelect
	componentTypeTextInput     = discord.ComponentTypeTextInput
	componentTypeUserSelect    = discord.ComponentTypeUserSelect
	componentTypeRoleSelect    = discord.ComponentTypeRoleSelect
	componentTypeMentionSelect = discord.ComponentTypeMentionSelect
	componentTypeChannelSelect = discord.ComponentTypeChannelSelect

	buttonStylePrimary   = discord.ButtonStylePrimary
	buttonStyleSecondary = discord.ButtonStyleSecondary
	buttonStyleSuccess   = discord.ButtonStyleSuccess
	buttonStyleDanger    = discord.ButtonStyleDanger
	buttonStyleLink      = discord.ButtonStyleLink

	interactionResponsePong           = discord.InteractionResponsePong
	interactionResponseMessage        = discord.InteractionResponseMessage
	interactionResponseDeferredUpdate = discord.InteractionResponseDeferredUpdate
	interactionResponseUpdateMessage  = discord.InteractionResponseUpdateMessage
	interactionResponseModal          = discord.InteractionResponseModal

	textInputStyleShort     = discord.TextInputStyleShort
	textInputStyleParagraph = discord.TextInputStyleParagraph
)
