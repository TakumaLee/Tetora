package discord

import (
	"fmt"
	"time"

	"tetora/internal/tmux"
)

// --- Discord Terminal UI ---

func (tb *TerminalBridge) sendControlPanel(channelID, content, sessionID string, allowedIDs []string) (string, error) {
	prefix := "term_" + sessionID + "_"

	row1 := discordActionRow(
		discordButton(prefix+"up", "\u2b06 Up", ButtonStyleSecondary),
		discordButton(prefix+"down", "\u2b07 Down", ButtonStyleSecondary),
		discordButton(prefix+"enter", "\u23ce Enter", ButtonStylePrimary),
		discordButton(prefix+"tab", "Tab", ButtonStyleSecondary),
		discordButton(prefix+"esc", "Esc", ButtonStyleSecondary),
	)
	row2 := discordActionRow(
		discordButton(prefix+"type", "\u2328 Type", ButtonStylePrimary),
		discordButton(prefix+"y", "Y", ButtonStyleSuccess),
		discordButton(prefix+"n", "N", ButtonStyleDanger),
		discordButton(prefix+"ctrlc", "Ctrl+C", ButtonStyleDanger),
		discordButton(prefix+"stop", "Stop", ButtonStyleDanger),
	)

	components := []Component{row1, row2}

	body, err := tb.bot.discordRequestWithResponse("POST",
		fmt.Sprintf("/channels/%s/messages", channelID),
		map[string]any{
			"content":    content,
			"components": components,
		})
	if err != nil {
		return "", err
	}
	var msg struct {
		ID string `json:"id"`
	}
	if err := jsonUnmarshalBytes(body, &msg); err != nil {
		return "", err
	}

	tb.registerControlButtons(sessionID, allowedIDs)
	return msg.ID, nil
}

func (tb *TerminalBridge) registerControlButtons(sessionID string, allowedIDs []string) {
	prefix := "term_" + sessionID + "_"

	keyMap := map[string][]string{
		"up":    {"Up"},
		"down":  {"Down"},
		"enter": {"Enter"},
		"tab":   {"Tab"},
		"esc":   {"Escape"},
		"y":     {"y"},
		"n":     {"n"},
		"ctrlc": {"C-c"},
	}

	for action, keys := range keyMap {
		keys := keys
		customID := prefix + action
		tb.bot.interactions.register(&pendingInteraction{
			CustomID:   customID,
			CreatedAt:  time.Now(),
			AllowedIDs: allowedIDs,
			Reusable:   true,
			Callback: func(data InteractionData) {
				session := tb.getSessionByID(sessionID)
				if session == nil {
					return
				}
				session.mu.Lock()
				session.LastActivity = time.Now()
				session.mu.Unlock()

				tmux.SendKeys(session.TmuxName, keys...)
				tb.signalCapture(session)
			},
		})
	}

	// "Type" button → modal.
	typeCustomID := prefix + "type"
	modalCustomID := "term_modal_" + sessionID
	tb.bot.interactions.register(&pendingInteraction{
		CustomID:   typeCustomID,
		CreatedAt:  time.Now(),
		AllowedIDs: allowedIDs,
		Reusable:   true,
		ModalResponse: func() *InteractionResponse {
			resp := discordBuildModal(modalCustomID, "Terminal Input",
				discordParagraphInput("term_input", "Text to send", true),
			)
			return &resp
		}(),
	})

	// Modal submit handler.
	tb.bot.interactions.register(&pendingInteraction{
		CustomID:   modalCustomID,
		CreatedAt:  time.Now(),
		AllowedIDs: allowedIDs,
		Reusable:   true,
		Callback: func(data InteractionData) {
			session := tb.getSessionByID(sessionID)
			if session == nil {
				return
			}
			values := extractModalValues(data.Components)
			text := values["term_input"]
			if text == "" {
				return
			}
			session.mu.Lock()
			session.LastActivity = time.Now()
			session.mu.Unlock()

			tmux.SendText(session.TmuxName, text)
			tmux.SendKeys(session.TmuxName, "Enter")
			tb.signalCapture(session)
		},
	})

	// "Stop" button.
	tb.bot.interactions.register(&pendingInteraction{
		CustomID:   prefix + "stop",
		CreatedAt:  time.Now(),
		AllowedIDs: allowedIDs,
		Reusable:   false,
		Callback: func(data InteractionData) {
			session := tb.getSessionByID(sessionID)
			if session == nil {
				return
			}
			tb.stopSession(session.ChannelID)
			tb.bot.sendMessage(session.ChannelID, "Terminal session stopped.")
		},
	})
}

func (tb *TerminalBridge) unregisterControlButtons(sessionID string) {
	prefix := "term_" + sessionID + "_"
	for _, action := range []string{"up", "down", "enter", "tab", "esc", "y", "n", "ctrlc", "type", "stop"} {
		tb.bot.interactions.remove(prefix + action)
	}
	tb.bot.interactions.remove("term_modal_" + sessionID)
}

func (tb *TerminalBridge) getSessionByID(sessionID string) *terminalSession {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	for _, s := range tb.sessions {
		if s.ID == sessionID {
			return s
		}
	}
	return nil
}
