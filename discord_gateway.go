package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tetora/internal/discord"
	"tetora/internal/log"
	"tetora/internal/trace"
)

// wsConn is a type alias so all root-package files share the same type.
type wsConn = discord.WsConn

// wsConnect dials a WebSocket URL (TLS) and completes the upgrade handshake.
var wsConnect = discord.WsConnect

// wsAcceptKey computes the expected Sec-WebSocket-Accept header value.
var wsAcceptKey = discord.WsAcceptKey

func (db *DiscordBot) sendIdentify(ws *wsConn) error {
	intents := discord.IntentGuildMessages | discord.IntentDirectMessages | discord.IntentMessageContent

	// P14.5: Add voice intents if voice is enabled
	if db.cfg.Discord.Voice.Enabled {
		intents |= intentGuildVoiceStates
	}

	id := discord.IdentifyData{
		Token:   db.cfg.Discord.BotToken,
		Intents: intents,
		Properties: map[string]string{
			"os": "linux", "browser": "tetora", "device": "tetora",
		},
	}
	d, _ := json.Marshal(id)
	return ws.WriteJSON(discord.GatewayPayload{Op: discord.OpIdentify, D: d})
}

func (db *DiscordBot) sendResume(ws *wsConn, seq int) error {
	r := discord.ResumePayload{
		Token: db.cfg.Discord.BotToken, SessionID: db.sessionID, Seq: seq,
	}
	d, _ := json.Marshal(r)
	return ws.WriteJSON(discord.GatewayPayload{Op: discord.OpResume, D: d})
}

func (db *DiscordBot) heartbeatLoop(ctx context.Context, ws *wsConn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := db.sendHeartbeatWS(ws); err != nil {
				return
			}
		}
	}
}

func (db *DiscordBot) sendHeartbeatWS(ws *wsConn) error {
	db.seqMu.Lock()
	seq := db.seq
	db.seqMu.Unlock()
	d, _ := json.Marshal(seq)
	return ws.WriteJSON(discord.GatewayPayload{Op: discord.OpHeartbeat, D: d})
}

// handleGatewayInteraction processes Discord interactions received via the Gateway
// (as opposed to the HTTP webhook endpoint). Responds via REST API callback.
func (db *DiscordBot) handleGatewayInteraction(interaction *discord.Interaction) {
	ctx := trace.WithID(context.Background(), trace.NewID("discord-interaction"))

	switch interaction.Type {
	case discord.InteractionTypePing:
		db.respondToInteraction(interaction, discord.InteractionResponse{Type: discord.InteractionResponsePong})

	case discord.InteractionTypeMessageComponent:
		resp := db.handleGatewayComponent(ctx, interaction)
		db.respondToInteraction(interaction, resp)

	case discord.InteractionTypeModalSubmit:
		resp := db.handleGatewayModal(ctx, interaction)
		db.respondToInteraction(interaction, resp)
	}
}

// handleGatewayComponent routes button clicks received via Gateway.
func (db *DiscordBot) handleGatewayComponent(ctx context.Context, interaction *discord.Interaction) discord.InteractionResponse {
	var data discord.InteractionData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		log.WarnCtx(ctx, "discord gateway component: invalid data", "error", err)
		return discord.InteractionResponse{Type: discord.InteractionResponseDeferredUpdate}
	}

	userID := interactionUserID(interaction)
	log.InfoCtx(ctx, "discord gateway component interaction",
		"customID", data.CustomID, "userID", userID)

	// Check registered interaction callbacks.
	if db.interactions != nil {
		if pi := db.interactions.lookup(data.CustomID); pi != nil {
			if len(pi.AllowedIDs) > 0 && !sliceContainsStr(pi.AllowedIDs, userID) {
				return discord.InteractionResponse{
					Type: discord.InteractionResponseMessage,
					Data: &discord.InteractionResponseData{
						Content: "You are not allowed to use this component.",
						Flags:   64,
					},
				}
			}
			if pi.Callback != nil {
				runCallbackWithTimeout(pi.Callback, data)
			}
			if !pi.Reusable {
				db.interactions.remove(data.CustomID)
			}
			if pi.Response != nil {
				return *pi.Response
			}
			if pi.ModalResponse != nil {
				return *pi.ModalResponse
			}
			return discord.InteractionResponse{Type: discord.InteractionResponseDeferredUpdate}
		}
	}

	// Fall through to built-in handlers.
	return handleBuiltinComponent(ctx, db, data, userID)
}

// handleGatewayModal routes modal submissions received via Gateway.
func (db *DiscordBot) handleGatewayModal(ctx context.Context, interaction *discord.Interaction) discord.InteractionResponse {
	var data discord.InteractionData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		log.WarnCtx(ctx, "discord gateway modal: invalid data", "error", err)
		return discord.InteractionResponse{Type: discord.InteractionResponseDeferredUpdate}
	}

	userID := interactionUserID(interaction)
	log.InfoCtx(ctx, "discord gateway modal submit", "customID", data.CustomID, "userID", userID)

	if db.interactions != nil {
		if pi := db.interactions.lookup(data.CustomID); pi != nil {
			if len(pi.AllowedIDs) > 0 && !sliceContainsStr(pi.AllowedIDs, userID) {
				return discord.InteractionResponse{
					Type: discord.InteractionResponseMessage,
					Data: &discord.InteractionResponseData{
						Content: "You are not allowed to submit this form.",
						Flags:   64,
					},
				}
			}
			if pi.Callback != nil {
				runCallbackWithTimeout(pi.Callback, data)
			}
			db.interactions.remove(data.CustomID)
			return discord.InteractionResponse{
				Type: discord.InteractionResponseDeferredUpdate,
			}
		}
	}

	return discord.InteractionResponse{Type: discord.InteractionResponseDeferredUpdate}
}

// respondToInteraction sends an interaction response via REST API (for Gateway-received interactions).
func (db *DiscordBot) respondToInteraction(interaction *discord.Interaction, resp discord.InteractionResponse) {
	path := fmt.Sprintf("/interactions/%s/%s/callback", interaction.ID, interaction.Token)
	db.discordPost(path, resp)
}
