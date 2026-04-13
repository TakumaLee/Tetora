package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tetora/internal/log"
	"tetora/internal/trace"
)

func (b *Bot) sendIdentify(ws *WsConn) error {
	intents := IntentGuildMessages | IntentDirectMessages | IntentMessageContent

	// P14.5: Add voice intents if voice is enabled
	if b.cfg.Discord.Voice.Enabled {
		intents |= IntentGuildVoiceStates
	}

	id := IdentifyData{
		Token:   b.cfg.Discord.BotToken,
		Intents: intents,
		Properties: map[string]string{
			"os": "linux", "browser": "tetora", "device": "tetora",
		},
	}
	d, _ := json.Marshal(id)
	return ws.WriteJSON(GatewayPayload{Op: OpIdentify, D: d})
}

func (b *Bot) sendResume(ws *WsConn, seq int) error {
	r := ResumePayload{
		Token: b.cfg.Discord.BotToken, SessionID: b.sessionID, Seq: seq,
	}
	d, _ := json.Marshal(r)
	return ws.WriteJSON(GatewayPayload{Op: OpResume, D: d})
}

func (b *Bot) heartbeatLoop(ctx context.Context, ws *WsConn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := b.sendHeartbeatWS(ws); err != nil {
				return
			}
		}
	}
}

func (b *Bot) sendHeartbeatWS(ws *WsConn) error {
	b.seqMu.Lock()
	seq := b.seq
	b.seqMu.Unlock()
	d, _ := json.Marshal(seq)
	return ws.WriteJSON(GatewayPayload{Op: OpHeartbeat, D: d})
}

// handleGatewayInteraction processes Discord interactions received via the Gateway
// (as opposed to the HTTP webhook endpoint). Responds via REST API callback.
func (b *Bot) handleGatewayInteraction(interaction *Interaction) {
	ctx := trace.WithID(context.Background(), trace.NewID("discord-interaction"))

	switch interaction.Type {
	case InteractionTypePing:
		b.respondToInteraction(interaction, InteractionResponse{Type: InteractionResponsePong})

	case InteractionTypeMessageComponent:
		resp := b.handleGatewayComponent(ctx, interaction)
		b.respondToInteraction(interaction, resp)

	case InteractionTypeModalSubmit:
		resp := b.handleGatewayModal(ctx, interaction)
		b.respondToInteraction(interaction, resp)
	}
}

// handleGatewayComponent routes button clicks received via Gateway.
func (b *Bot) handleGatewayComponent(ctx context.Context, interaction *Interaction) InteractionResponse {
	var data InteractionData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		log.WarnCtx(ctx, "discord gateway component: invalid data", "error", err)
		return InteractionResponse{Type: InteractionResponseDeferredUpdate}
	}

	userID := InteractionUserID(interaction)
	log.InfoCtx(ctx, "discord gateway component interaction",
		"customID", data.CustomID, "userID", userID)

	// Check registered interaction callbacks.
	if b.interactions != nil {
		if pi := b.interactions.lookup(data.CustomID); pi != nil {
			if len(pi.AllowedIDs) > 0 && !sliceContainsStr(pi.AllowedIDs, userID) {
				return InteractionResponse{
					Type: InteractionResponseMessage,
					Data: &InteractionResponseData{
						Content: "You are not allowed to use this component.",
						Flags:   64,
					},
				}
			}
			if pi.Callback != nil {
				RunCallbackWithTimeout(pi.Callback, data)
			}
			if !pi.Reusable {
				b.interactions.remove(data.CustomID)
			}
			if pi.Response != nil {
				return *pi.Response
			}
			if pi.ModalResponse != nil {
				return *pi.ModalResponse
			}
			return InteractionResponse{Type: InteractionResponseDeferredUpdate}
		}
	}

	// Fall through to built-in handlers.
	return handleBuiltinComponent(ctx, b, data, userID)
}

// handleGatewayModal routes modal submissions received via Gateway.
func (b *Bot) handleGatewayModal(ctx context.Context, interaction *Interaction) InteractionResponse {
	var data InteractionData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		log.WarnCtx(ctx, "discord gateway modal: invalid data", "error", err)
		return InteractionResponse{Type: InteractionResponseDeferredUpdate}
	}

	userID := InteractionUserID(interaction)
	log.InfoCtx(ctx, "discord gateway modal submit", "customID", data.CustomID, "userID", userID)

	if b.interactions != nil {
		if pi := b.interactions.lookup(data.CustomID); pi != nil {
			if len(pi.AllowedIDs) > 0 && !sliceContainsStr(pi.AllowedIDs, userID) {
				return InteractionResponse{
					Type: InteractionResponseMessage,
					Data: &InteractionResponseData{
						Content: "You are not allowed to submit this form.",
						Flags:   64,
					},
				}
			}
			if pi.Callback != nil {
				RunCallbackWithTimeout(pi.Callback, data)
			}
			b.interactions.remove(data.CustomID)
			return InteractionResponse{
				Type: InteractionResponseDeferredUpdate,
			}
		}
	}

	return InteractionResponse{Type: InteractionResponseDeferredUpdate}
}

// respondToInteraction sends an interaction response via REST API (for Gateway-received interactions).
func (b *Bot) respondToInteraction(interaction *Interaction, resp InteractionResponse) {
	path := fmt.Sprintf("/interactions/%s/%s/callback", interaction.ID, interaction.Token)
	b.discordPost(path, resp)
}

func (b *Bot) connectAndRun(ctx context.Context) error {
	ws, err := WsConnect(GatewayURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer ws.Close()

	b.gatewayConn = ws
	defer func() { b.gatewayConn = nil }()

	var hello GatewayPayload
	if err := ws.ReadJSON(&hello); err != nil {
		return fmt.Errorf("read hello: %w", err)
	}
	if hello.Op != OpHello {
		return fmt.Errorf("expected op 10, got %d", hello.Op)
	}

	var hd HelloData
	json.Unmarshal(hello.D, &hd)

	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go b.heartbeatLoop(hbCtx, ws, time.Duration(hd.HeartbeatInterval)*time.Millisecond)

	if b.sessionID != "" {
		b.seqMu.Lock()
		seq := b.seq
		b.seqMu.Unlock()
		err = b.sendResume(ws, seq)
	} else {
		err = b.sendIdentify(ws)
	}
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.stopCh:
			return nil
		default:
		}

		var payload GatewayPayload
		if err := ws.ReadJSON(&payload); err != nil {
			return fmt.Errorf("read: %w", err)
		}

		if payload.S != nil {
			b.seqMu.Lock()
			b.seq = *payload.S
			b.seqMu.Unlock()
		}

		switch payload.Op {
		case OpDispatch:
			b.handleEvent(payload)
		case OpHeartbeat:
			b.sendHeartbeatWS(ws)
		case OpReconnect:
			return nil
		case OpInvalidSession:
			b.sessionID = ""
			return nil
		case OpHeartbeatAck:
			// OK
		}
	}
}
