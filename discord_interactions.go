package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"tetora/internal/trace"
)

// --- Minimal WebSocket Client (RFC 6455, no external deps) ---

type wsConn struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex // protects writes
}

// wsConnect performs the WebSocket handshake over TLS.
func wsConnect(rawURL string) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}

	// TLS dial.
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", host, &tls.Config{})
	if err != nil {
		return nil, fmt.Errorf("tls dial: %w", err)
	}

	// Generate WebSocket key.
	keyBytes := make([]byte, 16)
	rand.Read(keyBytes)
	key := base64.StdEncoding.EncodeToString(keyBytes)

	// Send HTTP upgrade request.
	path := u.RequestURI()
	reqStr := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, u.Host, key)
	if _, err := conn.Write([]byte(reqStr)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write upgrade: %w", err)
	}

	// Read response.
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read status: %w", err)
	}
	if !strings.Contains(statusLine, "101") {
		conn.Close()
		return nil, fmt.Errorf("upgrade failed: %s", strings.TrimSpace(statusLine))
	}

	// Read headers until empty line.
	expectedAccept := wsAcceptKey(key)
	gotAccept := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("read headers: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "sec-websocket-accept:") {
			val := strings.TrimSpace(line[len("sec-websocket-accept:"):])
			if val == expectedAccept {
				gotAccept = true
			}
		}
	}
	if !gotAccept {
		conn.Close()
		return nil, fmt.Errorf("invalid Sec-WebSocket-Accept")
	}

	return &wsConn{conn: conn, reader: reader}, nil
}

// wsAcceptKey computes the expected Sec-WebSocket-Accept value.
func wsAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// ReadJSON reads a WebSocket text frame and decodes JSON.
func (ws *wsConn) ReadJSON(v any) error {
	data, err := ws.readFrame()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// WriteJSON encodes JSON and sends as a WebSocket text frame.
func (ws *wsConn) WriteJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return ws.writeFrame(1, data) // opcode 1 = text
}

// Close sends a close frame and closes the connection.
func (ws *wsConn) Close() error {
	ws.writeFrame(8, nil) // opcode 8 = close
	return ws.conn.Close()
}

// readFrame reads a single WebSocket frame (handles continuation).
func (ws *wsConn) readFrame() ([]byte, error) {
	ws.conn.SetReadDeadline(time.Now().Add(90 * time.Second))

	var result []byte
	for {
		// Read first 2 bytes.
		header := make([]byte, 2)
		if _, err := io.ReadFull(ws.reader, header); err != nil {
			return nil, err
		}

		fin := header[0]&0x80 != 0
		opcode := header[0] & 0x0F
		masked := header[1]&0x80 != 0
		payloadLen := int64(header[1] & 0x7F)

		// Close frame.
		if opcode == 8 {
			return nil, io.EOF
		}

		// Ping frame — respond with pong.
		if opcode == 9 {
			pongData := make([]byte, payloadLen)
			io.ReadFull(ws.reader, pongData)
			ws.writeFrame(10, pongData) // opcode 10 = pong
			continue
		}

		// Extended payload length.
		if payloadLen == 126 {
			ext := make([]byte, 2)
			if _, err := io.ReadFull(ws.reader, ext); err != nil {
				return nil, err
			}
			payloadLen = int64(binary.BigEndian.Uint16(ext))
		} else if payloadLen == 127 {
			ext := make([]byte, 8)
			if _, err := io.ReadFull(ws.reader, ext); err != nil {
				return nil, err
			}
			payloadLen = int64(binary.BigEndian.Uint64(ext))
		}

		// Masking key (server frames typically aren't masked, but handle it).
		var maskKey [4]byte
		if masked {
			if _, err := io.ReadFull(ws.reader, maskKey[:]); err != nil {
				return nil, err
			}
		}

		// Read payload.
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(ws.reader, payload); err != nil {
			return nil, err
		}

		if masked {
			for i := range payload {
				payload[i] ^= maskKey[i%4]
			}
		}

		result = append(result, payload...)

		if fin {
			break
		}
	}
	return result, nil
}

// writeFrame writes a WebSocket frame (client frames are masked per RFC 6455).
func (ws *wsConn) writeFrame(opcode byte, data []byte) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	var frame []byte
	frame = append(frame, 0x80|opcode) // FIN + opcode

	length := len(data)
	if length < 126 {
		frame = append(frame, byte(length)|0x80) // mask bit set
	} else if length < 65536 {
		frame = append(frame, 126|0x80)
		frame = append(frame, byte(length>>8), byte(length))
	} else {
		frame = append(frame, 127|0x80)
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(length))
		frame = append(frame, b...)
	}

	// Masking key.
	maskKey := make([]byte, 4)
	rand.Read(maskKey)
	frame = append(frame, maskKey...)

	// Masked payload.
	masked := make([]byte, length)
	for i := range data {
		masked[i] = data[i] ^ maskKey[i%4]
	}
	frame = append(frame, masked...)

	ws.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := ws.conn.Write(frame)
	return err
}

func (db *DiscordBot) sendIdentify(ws *wsConn) error {
	intents := intentGuildMessages | intentDirectMessages | intentMessageContent

	// P14.5: Add voice intents if voice is enabled
	if db.cfg.Discord.Voice.Enabled {
		intents |= intentGuildVoiceStates
	}

	id := identifyData{
		Token:   db.cfg.Discord.BotToken,
		Intents: intents,
		Properties: map[string]string{
			"os": "linux", "browser": "tetora", "device": "tetora",
		},
	}
	d, _ := json.Marshal(id)
	return ws.WriteJSON(gatewayPayload{Op: opIdentify, D: d})
}

func (db *DiscordBot) sendResume(ws *wsConn, seq int) error {
	r := resumePayload{
		Token: db.cfg.Discord.BotToken, SessionID: db.sessionID, Seq: seq,
	}
	d, _ := json.Marshal(r)
	return ws.WriteJSON(gatewayPayload{Op: opResume, D: d})
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
	return ws.WriteJSON(gatewayPayload{Op: opHeartbeat, D: d})
}

// handleGatewayInteraction processes Discord interactions received via the Gateway
// (as opposed to the HTTP webhook endpoint). Responds via REST API callback.
func (db *DiscordBot) handleGatewayInteraction(interaction *discordInteraction) {
	ctx := trace.WithID(context.Background(), trace.NewID("discord-interaction"))

	switch interaction.Type {
	case interactionTypePing:
		db.respondToInteraction(interaction, discordInteractionResponse{Type: interactionResponsePong})

	case interactionTypeMessageComponent:
		resp := db.handleGatewayComponent(ctx, interaction)
		db.respondToInteraction(interaction, resp)

	case interactionTypeModalSubmit:
		resp := db.handleGatewayModal(ctx, interaction)
		db.respondToInteraction(interaction, resp)
	}
}

// handleGatewayComponent routes button clicks received via Gateway.
func (db *DiscordBot) handleGatewayComponent(ctx context.Context, interaction *discordInteraction) discordInteractionResponse {
	var data discordInteractionData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		logWarnCtx(ctx, "discord gateway component: invalid data", "error", err)
		return discordInteractionResponse{Type: interactionResponseDeferredUpdate}
	}

	userID := interactionUserID(interaction)
	logInfoCtx(ctx, "discord gateway component interaction",
		"customID", data.CustomID, "userID", userID)

	// Check registered interaction callbacks.
	if db.interactions != nil {
		if pi := db.interactions.lookup(data.CustomID); pi != nil {
			if len(pi.AllowedIDs) > 0 && !sliceContainsStr(pi.AllowedIDs, userID) {
				return discordInteractionResponse{
					Type: interactionResponseMessage,
					Data: &discordInteractionResponseData{
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
			return discordInteractionResponse{Type: interactionResponseDeferredUpdate}
		}
	}

	// Fall through to built-in handlers.
	return handleBuiltinComponent(ctx, db, data, userID)
}

// handleGatewayModal routes modal submissions received via Gateway.
func (db *DiscordBot) handleGatewayModal(ctx context.Context, interaction *discordInteraction) discordInteractionResponse {
	var data discordInteractionData
	if err := json.Unmarshal(interaction.Data, &data); err != nil {
		logWarnCtx(ctx, "discord gateway modal: invalid data", "error", err)
		return discordInteractionResponse{Type: interactionResponseDeferredUpdate}
	}

	userID := interactionUserID(interaction)
	logInfoCtx(ctx, "discord gateway modal submit", "customID", data.CustomID, "userID", userID)

	if db.interactions != nil {
		if pi := db.interactions.lookup(data.CustomID); pi != nil {
			if len(pi.AllowedIDs) > 0 && !sliceContainsStr(pi.AllowedIDs, userID) {
				return discordInteractionResponse{
					Type: interactionResponseMessage,
					Data: &discordInteractionResponseData{
						Content: "You are not allowed to submit this form.",
						Flags:   64,
					},
				}
			}
			if pi.Callback != nil {
				runCallbackWithTimeout(pi.Callback, data)
			}
			db.interactions.remove(data.CustomID)
			return discordInteractionResponse{
				Type: interactionResponseDeferredUpdate,
			}
		}
	}

	return discordInteractionResponse{Type: interactionResponseDeferredUpdate}
}

// respondToInteraction sends an interaction response via REST API (for Gateway-received interactions).
func (db *DiscordBot) respondToInteraction(interaction *discordInteraction, resp discordInteractionResponse) {
	path := fmt.Sprintf("/interactions/%s/%s/callback", interaction.ID, interaction.Token)
	db.discordPost(path, resp)
}
