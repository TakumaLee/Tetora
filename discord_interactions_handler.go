package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"tetora/internal/audit"
	"tetora/internal/discord"
	"tetora/internal/log"
	"tetora/internal/trace"
)

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
		log.Warn("discord interactions: no public key configured")
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
		log.Warn("discord interactions: invalid signature", "ip", clientIP(r))
		http.Error(w, `{"error":"invalid signature"}`, http.StatusUnauthorized)
		return
	}

	// Parse interaction.
	var interaction discord.Interaction
	if err := json.Unmarshal(body, &interaction); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	ctx := trace.WithID(context.Background(), trace.NewID("discord-interaction"))

	// Route by interaction type.
	switch interaction.Type {
	case discord.InteractionTypePing:
		// Respond with PONG.
		log.InfoCtx(ctx, "discord interaction PING received")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(discord.InteractionResponse{Type: discord.InteractionResponsePong})
		return

	case discord.InteractionTypeMessageComponent:
		handleComponentInteraction(ctx, db, w, &interaction)
		return

	case discord.InteractionTypeModalSubmit:
		handleModalSubmit(ctx, db, w, &interaction)
		return

	case discord.InteractionTypeApplicationCmd:
		// Application commands — respond with a basic message for now.
		log.InfoCtx(ctx, "discord application command received")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(discord.InteractionResponse{
			Type: discord.InteractionResponseMessage,
			Data: &discord.InteractionResponseData{
				Content: "Command received. Use the Tetora dashboard for full functionality.",
			},
		})
		return

	default:
		log.Warn("discord interactions: unknown type", "type", interaction.Type)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(discord.InteractionResponse{
			Type: discord.InteractionResponseMessage,
			Data: &discord.InteractionResponseData{
				Content: "Unknown interaction type.",
				Flags:   64, // ephemeral
			},
		})
	}
}

// handleComponentInteraction routes button clicks and select menu selections.
func handleComponentInteraction(ctx context.Context, db *DiscordBot, w http.ResponseWriter, interaction *discord.Interaction) {
	var data discord.InteractionData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		log.WarnCtx(ctx, "discord component: invalid data", "error", err)
		http.Error(w, `{"error":"invalid component data"}`, http.StatusBadRequest)
		return
	}

	userID := interactionUserID(interaction)
	log.InfoCtx(ctx, "discord component interaction",
		"customID", data.CustomID,
		"userID", userID,
		"values", fmt.Sprintf("%v", data.Values))

	// Check registered interaction callbacks.
	if db.interactions != nil {
		if pi := db.interactions.lookup(data.CustomID); pi != nil {
			// Check allowed users.
			if len(pi.AllowedIDs) > 0 && !sliceContainsStr(pi.AllowedIDs, userID) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(discord.InteractionResponse{
					Type: discord.InteractionResponseMessage,
					Data: &discord.InteractionResponseData{
						Content: "You are not allowed to use this component.",
						Flags:   64, // ephemeral
					},
				})
				return
			}

			// Fire callback in background.
			if pi.Callback != nil {
				runCallbackWithTimeout(pi.Callback, data)
			}

			// Remove if not reusable.
			if !pi.Reusable {
				db.interactions.remove(data.CustomID)
			}

			// Respond: custom Response → modal → deferred update.
			w.Header().Set("Content-Type", "application/json")
			if pi.Response != nil {
				json.NewEncoder(w).Encode(*pi.Response)
			} else if pi.ModalResponse != nil {
				json.NewEncoder(w).Encode(*pi.ModalResponse)
			} else {
				json.NewEncoder(w).Encode(discord.InteractionResponse{
					Type: discord.InteractionResponseDeferredUpdate,
				})
			}
			return
		}
	}

	// Default: handle common built-in custom_id patterns.
	response := handleBuiltinComponent(ctx, db, data, userID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleModalSubmit processes modal form submissions.
func handleModalSubmit(ctx context.Context, db *DiscordBot, w http.ResponseWriter, interaction *discord.Interaction) {
	var data discord.InteractionData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		log.WarnCtx(ctx, "discord modal: invalid data", "error", err)
		http.Error(w, `{"error":"invalid modal data"}`, http.StatusBadRequest)
		return
	}

	userID := interactionUserID(interaction)
	log.InfoCtx(ctx, "discord modal submit",
		"customID", data.CustomID,
		"userID", userID)

	// Extract modal field values.
	values := extractModalValues(data.Components)

	// Check registered interaction callbacks.
	if db.interactions != nil {
		if pi := db.interactions.lookup(data.CustomID); pi != nil {
			if len(pi.AllowedIDs) > 0 && !sliceContainsStr(pi.AllowedIDs, userID) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(discord.InteractionResponse{
					Type: discord.InteractionResponseMessage,
					Data: &discord.InteractionResponseData{
						Content: "You are not allowed to submit this form.",
						Flags:   64,
					},
				})
				return
			}

			if pi.Callback != nil {
				runCallbackWithTimeout(pi.Callback, data)
			}
			db.interactions.remove(data.CustomID)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(discord.InteractionResponse{
				Type: discord.InteractionResponseMessage,
				Data: &discord.InteractionResponseData{
					Content: "Form submitted successfully.",
					Flags:   64,
				},
			})
			return
		}
	}

	// Default response for unhandled modals.
	log.InfoCtx(ctx, "discord modal unhandled", "customID", data.CustomID, "values", fmt.Sprintf("%v", values))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(discord.InteractionResponse{
		Type: discord.InteractionResponseMessage,
		Data: &discord.InteractionResponseData{
			Content: fmt.Sprintf("Form received (%d fields).", len(values)),
			Flags:   64,
		},
	})
}

// --- Built-in Component Handlers ---

// handleBuiltinComponent handles common built-in component custom_id patterns.
func handleBuiltinComponent(ctx context.Context, db *DiscordBot, data discord.InteractionData, userID string) discord.InteractionResponse {
	customID := data.CustomID

	// P28.0: Approval gate callbacks.
	if strings.HasPrefix(customID, "gate_approve:") {
		reqID := strings.TrimPrefix(customID, "gate_approve:")
		if db.approvalGate != nil {
			db.approvalGate.handleGateCallback(reqID, true)
		}
		return discord.InteractionResponse{
			Type: discord.InteractionResponseUpdateMessage,
			Data: &discord.InteractionResponseData{
				Content: fmt.Sprintf("Approved by <@%s>.", userID),
			},
		}
	}
	if strings.HasPrefix(customID, "gate_always:") {
		rest := strings.TrimPrefix(customID, "gate_always:")
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) == 2 {
			reqID, toolName := parts[0], parts[1]
			if db.approvalGate != nil {
				db.approvalGate.AutoApprove(toolName)
				db.approvalGate.handleGateCallback(reqID, true)
			}
			return discord.InteractionResponse{
				Type: discord.InteractionResponseUpdateMessage,
				Data: &discord.InteractionResponseData{
					Content: fmt.Sprintf("Always approved `%s` by <@%s>.", toolName, userID),
				},
			}
		}
	}
	if strings.HasPrefix(customID, "gate_reject:") {
		reqID := strings.TrimPrefix(customID, "gate_reject:")
		if db.approvalGate != nil {
			db.approvalGate.handleGateCallback(reqID, false)
		}
		return discord.InteractionResponse{
			Type: discord.InteractionResponseUpdateMessage,
			Data: &discord.InteractionResponseData{
				Content: fmt.Sprintf("Rejected by <@%s>.", userID),
			},
		}
	}

	// Pattern: "approve:{taskID}" / "reject:{taskID}"
	if strings.HasPrefix(customID, "approve:") {
		taskID := strings.TrimPrefix(customID, "approve:")
		log.InfoCtx(ctx, "discord component: task approved", "taskID", taskID, "userID", userID)
		audit.Log(db.cfg.HistoryDB, "discord.component.approve", "discord",
			fmt.Sprintf("task=%s user=%s", taskID, userID), "")
		return discord.InteractionResponse{
			Type: discord.InteractionResponseUpdateMessage,
			Data: &discord.InteractionResponseData{
				Content: fmt.Sprintf("Task `%s` approved by <@%s>.", truncate(taskID, 8), userID),
			},
		}
	}

	if strings.HasPrefix(customID, "reject:") {
		taskID := strings.TrimPrefix(customID, "reject:")
		log.InfoCtx(ctx, "discord component: task rejected", "taskID", taskID, "userID", userID)
		audit.Log(db.cfg.HistoryDB, "discord.component.reject", "discord",
			fmt.Sprintf("task=%s user=%s", taskID, userID), "")
		return discord.InteractionResponse{
			Type: discord.InteractionResponseUpdateMessage,
			Data: &discord.InteractionResponseData{
				Content: fmt.Sprintf("Task `%s` rejected by <@%s>.", truncate(taskID, 8), userID),
			},
		}
	}

	// Pattern: "agent_select" — route to selected agent.
	if customID == "agent_select" && len(data.Values) > 0 {
		agent := data.Values[0]
		log.InfoCtx(ctx, "discord component: agent selected", "agent", agent, "userID", userID)
		return discord.InteractionResponse{
			Type: discord.InteractionResponseMessage,
			Data: &discord.InteractionResponseData{
				Content: fmt.Sprintf("Routing to agent **%s**...", agent),
			},
		}
	}

	// Unknown component.
	log.InfoCtx(ctx, "discord component: unhandled", "customID", customID)
	return discord.InteractionResponse{
		Type: discord.InteractionResponseDeferredUpdate,
	}
}
