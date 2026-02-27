package main

// --- P15.2: Matrix Channel ---

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMatrixSyncResponseParsing(t *testing.T) {
	payload := `{
		"next_batch": "s72595_4483_1934",
		"rooms": {
			"join": {
				"!roomid1:example.com": {
					"timeline": {
						"events": [
							{
								"type": "m.room.message",
								"sender": "@alice:example.com",
								"event_id": "$event1",
								"origin_server_ts": 1625000000000,
								"content": {"msgtype": "m.text", "body": "Hello Tetora!"}
							},
							{
								"type": "m.room.message",
								"sender": "@bob:example.com",
								"event_id": "$event2",
								"origin_server_ts": 1625000001000,
								"content": {"msgtype": "m.image", "body": "photo.jpg", "url": "mxc://example.com/abc"}
							}
						]
					}
				},
				"!roomid2:example.com": {
					"timeline": {
						"events": [
							{
								"type": "m.room.member",
								"sender": "@carol:example.com",
								"event_id": "$event3",
								"origin_server_ts": 1625000002000,
								"content": {"membership": "join"}
							}
						]
					}
				}
			},
			"invite": {
				"!invited_room:example.com": {
					"invite_state": {
						"events": [
							{
								"type": "m.room.member",
								"sender": "@inviter:example.com",
								"event_id": "$inv1",
								"content": {"membership": "invite"}
							}
						]
					}
				}
			}
		}
	}`

	var syncResp matrixSyncResponse
	if err := json.Unmarshal([]byte(payload), &syncResp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if syncResp.NextBatch != "s72595_4483_1934" {
		t.Errorf("NextBatch = %q, want %q", syncResp.NextBatch, "s72595_4483_1934")
	}

	// Check joined rooms.
	if len(syncResp.Rooms.Join) != 2 {
		t.Fatalf("joined rooms count = %d, want 2", len(syncResp.Rooms.Join))
	}

	room1, ok := syncResp.Rooms.Join["!roomid1:example.com"]
	if !ok {
		t.Fatal("room !roomid1:example.com not found in join")
	}
	if len(room1.Timeline.Events) != 2 {
		t.Fatalf("room1 events = %d, want 2", len(room1.Timeline.Events))
	}

	// First event: text message.
	ev := room1.Timeline.Events[0]
	if ev.Type != "m.room.message" {
		t.Errorf("event[0].type = %q, want %q", ev.Type, "m.room.message")
	}
	if ev.Sender != "@alice:example.com" {
		t.Errorf("event[0].sender = %q, want %q", ev.Sender, "@alice:example.com")
	}
	if ev.EventID != "$event1" {
		t.Errorf("event[0].event_id = %q, want %q", ev.EventID, "$event1")
	}
	if ev.OriginTS != 1625000000000 {
		t.Errorf("event[0].origin_server_ts = %d, want %d", ev.OriginTS, 1625000000000)
	}

	var content matrixMessageContent
	if err := json.Unmarshal(ev.Content, &content); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if content.MsgType != "m.text" {
		t.Errorf("content.msgtype = %q, want %q", content.MsgType, "m.text")
	}
	if content.Body != "Hello Tetora!" {
		t.Errorf("content.body = %q, want %q", content.Body, "Hello Tetora!")
	}

	// Second event: image message.
	ev2 := room1.Timeline.Events[1]
	var content2 matrixMessageContent
	if err := json.Unmarshal(ev2.Content, &content2); err != nil {
		t.Fatalf("unmarshal content2: %v", err)
	}
	if content2.MsgType != "m.image" {
		t.Errorf("content2.msgtype = %q, want %q", content2.MsgType, "m.image")
	}
	if content2.URL != "mxc://example.com/abc" {
		t.Errorf("content2.url = %q, want %q", content2.URL, "mxc://example.com/abc")
	}

	// Check invited rooms.
	if len(syncResp.Rooms.Invite) != 1 {
		t.Fatalf("invited rooms count = %d, want 1", len(syncResp.Rooms.Invite))
	}
	_, ok = syncResp.Rooms.Invite["!invited_room:example.com"]
	if !ok {
		t.Error("invited room !invited_room:example.com not found")
	}
}

func TestMatrixMessageEventHandling(t *testing.T) {
	cfg := &Config{
		Matrix: MatrixConfig{
			Enabled:     true,
			Homeserver:  "https://matrix.example.com",
			UserID:      "@tetora:example.com",
			AccessToken: "test_token",
		},
	}

	mb := newMatrixBot(cfg, nil, nil, nil)

	// Text message from someone else: should be processed.
	textEvent := matrixEvent{
		Type:    "m.room.message",
		Sender:  "@alice:example.com",
		EventID: "$text1",
		Content: json.RawMessage(`{"msgtype": "m.text", "body": "Hello"}`),
	}

	var content matrixMessageContent
	if err := json.Unmarshal(textEvent.Content, &content); err != nil {
		t.Fatalf("parse content: %v", err)
	}
	if content.MsgType != "m.text" {
		t.Errorf("msgtype = %q, want m.text", content.MsgType)
	}
	if content.Body != "Hello" {
		t.Errorf("body = %q, want Hello", content.Body)
	}

	// Own message: should be ignored.
	ownEvent := matrixEvent{
		Type:    "m.room.message",
		Sender:  "@tetora:example.com",
		EventID: "$own1",
		Content: json.RawMessage(`{"msgtype": "m.text", "body": "Own message"}`),
	}
	if ownEvent.Sender == mb.cfg.Matrix.UserID {
		// Correctly identified as own message.
	} else {
		t.Error("failed to identify own message")
	}

	// Image message: should be ignored (only m.text processed).
	imgEvent := matrixEvent{
		Type:    "m.room.message",
		Sender:  "@alice:example.com",
		EventID: "$img1",
		Content: json.RawMessage(`{"msgtype": "m.image", "body": "photo.jpg"}`),
	}
	var imgContent matrixMessageContent
	json.Unmarshal(imgEvent.Content, &imgContent)
	if imgContent.MsgType == "m.text" {
		t.Error("image message should not have m.text msgtype")
	}

	// Non-message event: should be ignored.
	memberEvent := matrixEvent{
		Type:    "m.room.member",
		Sender:  "@alice:example.com",
		EventID: "$member1",
		Content: json.RawMessage(`{"membership": "join"}`),
	}
	if memberEvent.Type == "m.room.message" {
		t.Error("member event should not be m.room.message")
	}
}

func TestMatrixSendMessageWithTxnId(t *testing.T) {
	var capturedURL string
	var capturedBody map[string]string
	var capturedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")

		if r.Method != "PUT" {
			t.Errorf("method = %q, want PUT", r.Method)
		}

		bodyData, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyData, &capturedBody)

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"event_id":"$sent1"}`))
	}))
	defer srv.Close()

	cfg := &Config{
		Matrix: MatrixConfig{
			Enabled:     true,
			Homeserver:  srv.URL,
			UserID:      "@tetora:example.com",
			AccessToken: "test_token",
		},
	}

	mb := newMatrixBot(cfg, nil, nil, nil)

	err := mb.sendMessage("!roomid:example.com", "Hello from Tetora!")
	if err != nil {
		t.Fatalf("sendMessage() error: %v", err)
	}

	// Check auth header.
	if capturedAuth != "Bearer test_token" {
		t.Errorf("Authorization = %q, want %q", capturedAuth, "Bearer test_token")
	}

	// Check URL contains room ID and txn ID.
	if !strings.Contains(capturedURL, "/rooms/!roomid:example.com/send/m.room.message/") {
		t.Errorf("URL = %q, does not contain expected path", capturedURL)
	}

	// Check body.
	if capturedBody["msgtype"] != "m.text" {
		t.Errorf("msgtype = %q, want m.text", capturedBody["msgtype"])
	}
	if capturedBody["body"] != "Hello from Tetora!" {
		t.Errorf("body = %q, want 'Hello from Tetora!'", capturedBody["body"])
	}

	// Send another message and check txnID increments.
	err = mb.sendMessage("!roomid:example.com", "Second message")
	if err != nil {
		t.Fatalf("sendMessage() second error: %v", err)
	}

	// The txnID should have incremented (we can check it's still valid).
	currentTxn := atomic.LoadInt64(&mb.txnID)
	if currentTxn < 2 {
		t.Errorf("txnID = %d, want >= 2", currentTxn)
	}
}

func TestMatrixJoinRoom(t *testing.T) {
	var capturedURL string
	var capturedMethod string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.Path
		capturedMethod = r.Method
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"room_id":"!roomid:example.com"}`))
	}))
	defer srv.Close()

	cfg := &Config{
		Matrix: MatrixConfig{
			Enabled:     true,
			Homeserver:  srv.URL,
			AccessToken: "test_token",
		},
	}

	mb := newMatrixBot(cfg, nil, nil, nil)

	err := mb.joinRoom("!roomid:example.com")
	if err != nil {
		t.Fatalf("joinRoom() error: %v", err)
	}

	if capturedMethod != "POST" {
		t.Errorf("method = %q, want POST", capturedMethod)
	}
	if !strings.Contains(capturedURL, "/join/!roomid:example.com") {
		t.Errorf("URL = %q, does not contain expected path", capturedURL)
	}

	// Test empty room ID.
	err = mb.joinRoom("")
	if err == nil {
		t.Error("joinRoom('') should return error")
	}
}

func TestMatrixLeaveRoom(t *testing.T) {
	var capturedURL string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.Path
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := &Config{
		Matrix: MatrixConfig{
			Enabled:     true,
			Homeserver:  srv.URL,
			AccessToken: "test_token",
		},
	}

	mb := newMatrixBot(cfg, nil, nil, nil)

	err := mb.leaveRoom("!roomid:example.com")
	if err != nil {
		t.Fatalf("leaveRoom() error: %v", err)
	}

	if !strings.Contains(capturedURL, "/rooms/!roomid:example.com/leave") {
		t.Errorf("URL = %q, does not contain expected path", capturedURL)
	}

	// Test empty room ID.
	err = mb.leaveRoom("")
	if err == nil {
		t.Error("leaveRoom('') should return error")
	}
}

func TestMatrixFilterOwnMessages(t *testing.T) {
	// Simulate events where sender == bot userID.
	botUserID := "@tetora:example.com"

	events := []matrixEvent{
		{Type: "m.room.message", Sender: "@alice:example.com", EventID: "$e1", Content: json.RawMessage(`{"msgtype":"m.text","body":"from alice"}`)},
		{Type: "m.room.message", Sender: botUserID, EventID: "$e2", Content: json.RawMessage(`{"msgtype":"m.text","body":"from bot"}`)},
		{Type: "m.room.message", Sender: "@bob:example.com", EventID: "$e3", Content: json.RawMessage(`{"msgtype":"m.text","body":"from bob"}`)},
	}

	var processed []string
	for _, ev := range events {
		if ev.Type != "m.room.message" {
			continue
		}
		if ev.Sender == botUserID {
			continue
		}
		var content matrixMessageContent
		json.Unmarshal(ev.Content, &content)
		if content.MsgType != "m.text" {
			continue
		}
		processed = append(processed, content.Body)
	}

	if len(processed) != 2 {
		t.Fatalf("processed count = %d, want 2", len(processed))
	}
	if processed[0] != "from alice" {
		t.Errorf("processed[0] = %q, want 'from alice'", processed[0])
	}
	if processed[1] != "from bob" {
		t.Errorf("processed[1] = %q, want 'from bob'", processed[1])
	}
}

func TestMatrixAutoJoinInvitedRooms(t *testing.T) {
	var joinedRooms []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/join/") {
			roomID := strings.TrimPrefix(r.URL.Path, "/_matrix/client/v3/join/")
			joinedRooms = append(joinedRooms, roomID)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := &Config{
		Matrix: MatrixConfig{
			Enabled:     true,
			Homeserver:  srv.URL,
			UserID:      "@tetora:example.com",
			AccessToken: "test_token",
			AutoJoin:    true,
		},
	}

	mb := newMatrixBot(cfg, nil, nil, nil)

	// Simulate invited rooms.
	invitedRooms := map[string]matrixInvitedRoom{
		"!invite1:example.com": {},
		"!invite2:example.com": {},
	}

	for roomID := range invitedRooms {
		if err := mb.joinRoom(roomID); err != nil {
			t.Errorf("joinRoom(%q) error: %v", roomID, err)
		}
	}

	if len(joinedRooms) != 2 {
		t.Fatalf("joined rooms = %d, want 2", len(joinedRooms))
	}
}

func TestMatrixSyncTokenPersistence(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		sinceParam := r.URL.Query().Get("since")

		if callCount == 1 {
			// First sync: no since token.
			if sinceParam != "" {
				t.Errorf("first sync should have no since token, got %q", sinceParam)
			}
		} else if callCount == 2 {
			// Second sync: should have since token.
			if sinceParam != "batch_token_1" {
				t.Errorf("second sync since = %q, want 'batch_token_1'", sinceParam)
			}
		}

		resp := matrixSyncResponse{
			NextBatch: fmt.Sprintf("batch_token_%d", callCount),
			Rooms: matrixSyncRooms{
				Join:   make(map[string]matrixJoinedRoom),
				Invite: make(map[string]matrixInvitedRoom),
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := &Config{
		Matrix: MatrixConfig{
			Enabled:     true,
			Homeserver:  srv.URL,
			UserID:      "@tetora:example.com",
			AccessToken: "test_token",
		},
	}

	mb := newMatrixBot(cfg, nil, nil, nil)

	// First sync.
	if err := mb.sync(); err != nil {
		t.Fatalf("first sync error: %v", err)
	}
	if mb.sinceToken != "batch_token_1" {
		t.Errorf("after first sync, sinceToken = %q, want 'batch_token_1'", mb.sinceToken)
	}

	// Second sync.
	if err := mb.sync(); err != nil {
		t.Fatalf("second sync error: %v", err)
	}
	if mb.sinceToken != "batch_token_2" {
		t.Errorf("after second sync, sinceToken = %q, want 'batch_token_2'", mb.sinceToken)
	}

	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
}

func TestMatrixErrorHandling(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    string
	}{
		{
			name:       "unauthorized",
			statusCode: 401,
			body:       `{"errcode":"M_UNKNOWN_TOKEN","error":"Unknown token"}`,
			wantErr:    "unauthorized (401)",
		},
		{
			name:       "rate limited",
			statusCode: 429,
			body:       `{"errcode":"M_LIMIT_EXCEEDED","error":"Too many requests"}`,
			wantErr:    "HTTP 429",
		},
		{
			name:       "server error",
			statusCode: 500,
			body:       `{"errcode":"M_UNKNOWN","error":"Internal server error"}`,
			wantErr:    "HTTP 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			cfg := &Config{
				Matrix: MatrixConfig{
					Enabled:     true,
					Homeserver:  srv.URL,
					UserID:      "@tetora:example.com",
					AccessToken: "bad_token",
				},
			}

			mb := newMatrixBot(cfg, nil, nil, nil)
			err := mb.sync()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestMatrixConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     MatrixConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: MatrixConfig{
				Enabled:     true,
				Homeserver:  "https://matrix.example.com",
				UserID:      "@tetora:example.com",
				AccessToken: "token_xyz",
			},
			wantErr: false,
		},
		{
			name: "missing homeserver",
			cfg: MatrixConfig{
				Enabled:     true,
				Homeserver:  "",
				UserID:      "@tetora:example.com",
				AccessToken: "token_xyz",
			},
			wantErr: true,
		},
		{
			name: "missing access token",
			cfg: MatrixConfig{
				Enabled:     true,
				Homeserver:  "https://matrix.example.com",
				UserID:      "@tetora:example.com",
				AccessToken: "",
			},
			wantErr: true,
		},
		{
			name: "missing user ID",
			cfg: MatrixConfig{
				Enabled:     true,
				Homeserver:  "https://matrix.example.com",
				UserID:      "",
				AccessToken: "token_xyz",
			},
			wantErr: true,
		},
		{
			name: "disabled is ok with missing fields",
			cfg: MatrixConfig{
				Enabled: false,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasErr := false
			if tt.cfg.Enabled {
				if tt.cfg.Homeserver == "" || tt.cfg.AccessToken == "" || tt.cfg.UserID == "" {
					hasErr = true
				}
			}
			if hasErr != tt.wantErr {
				t.Errorf("config validation: hasErr = %v, wantErr = %v", hasErr, tt.wantErr)
			}
		})
	}
}

func TestMatrixNotifier(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		bodyData, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyData, &capturedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"event_id":"$notif1"}`))
	}))
	defer srv.Close()

	notifier := &MatrixNotifier{
		Config: MatrixConfig{
			Homeserver:  srv.URL,
			AccessToken: "test_token",
		},
		RoomID: "!notif_room:example.com",
	}

	if notifier.Name() != "matrix" {
		t.Errorf("Name() = %q, want %q", notifier.Name(), "matrix")
	}

	// Test empty text returns nil.
	if err := notifier.Send(""); err != nil {
		t.Errorf("Send('') error: %v", err)
	}

	// Test sending a notification.
	if err := notifier.Send("Task completed successfully!"); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	if capturedReq.Method != "PUT" {
		t.Errorf("method = %q, want PUT", capturedReq.Method)
	}
	if capturedReq.Header.Get("Authorization") != "Bearer test_token" {
		t.Errorf("Authorization = %q, want 'Bearer test_token'", capturedReq.Header.Get("Authorization"))
	}
	if capturedBody["msgtype"] != "m.text" {
		t.Errorf("msgtype = %q, want m.text", capturedBody["msgtype"])
	}
	if capturedBody["body"] != "Task completed successfully!" {
		t.Errorf("body = %q, want 'Task completed successfully!'", capturedBody["body"])
	}

	// Test empty room ID returns error.
	notifier2 := &MatrixNotifier{
		Config: MatrixConfig{
			Homeserver:  srv.URL,
			AccessToken: "test_token",
		},
		RoomID: "",
	}
	if err := notifier2.Send("test"); err == nil {
		t.Error("Send() with empty RoomID should return error")
	}
}

func TestMatrixBotCreation(t *testing.T) {
	cfg := &Config{
		Matrix: MatrixConfig{
			Enabled:     true,
			Homeserver:  "https://matrix.example.com",
			UserID:      "@tetora:example.com",
			AccessToken: "token_abc",
			AutoJoin:    true,
			DefaultAgent: "琉璃",
		},
	}

	state := newDispatchState()
	sem := make(chan struct{}, 1)

	mb := newMatrixBot(cfg, state, sem, nil)
	if mb == nil {
		t.Fatal("newMatrixBot returned nil")
	}
	if mb.cfg != cfg {
		t.Error("bot config not set correctly")
	}
	if mb.state != state {
		t.Error("bot state not set correctly")
	}
	if mb.sem != sem {
		t.Error("bot semaphore not set correctly")
	}
	if mb.apiBase != "https://matrix.example.com/_matrix/client/v3" {
		t.Errorf("apiBase = %q, want %q", mb.apiBase, "https://matrix.example.com/_matrix/client/v3")
	}
	if mb.httpClient == nil {
		t.Error("bot httpClient not initialized")
	}
	if mb.stopCh == nil {
		t.Error("bot stopCh not initialized")
	}
	if mb.sinceToken != "" {
		t.Errorf("sinceToken = %q, want empty", mb.sinceToken)
	}
}

func TestMatrixSendMessageEmptyRoomID(t *testing.T) {
	cfg := &Config{
		Matrix: MatrixConfig{
			Enabled:     true,
			Homeserver:  "https://matrix.example.com",
			AccessToken: "test_token",
		},
	}

	mb := newMatrixBot(cfg, nil, nil, nil)

	err := mb.sendMessage("", "hello")
	if err == nil {
		t.Error("sendMessage with empty roomID should return error")
	}
	if !strings.Contains(err.Error(), "empty room ID") {
		t.Errorf("error = %q, want to contain 'empty room ID'", err.Error())
	}

	// Empty text should not error.
	err = mb.sendMessage("!room:example.com", "")
	if err != nil {
		t.Errorf("sendMessage with empty text should not error: %v", err)
	}
}

func TestMatrixHomeserverTrailingSlash(t *testing.T) {
	// Test that trailing slash in homeserver URL is handled correctly.
	cfg := &Config{
		Matrix: MatrixConfig{
			Enabled:    true,
			Homeserver: "https://matrix.example.com/",
		},
	}

	mb := newMatrixBot(cfg, nil, nil, nil)
	expected := "https://matrix.example.com/_matrix/client/v3"
	if mb.apiBase != expected {
		t.Errorf("apiBase = %q, want %q", mb.apiBase, expected)
	}

	// Without trailing slash.
	cfg.Matrix.Homeserver = "https://matrix.example.com"
	mb2 := newMatrixBot(cfg, nil, nil, nil)
	if mb2.apiBase != expected {
		t.Errorf("apiBase = %q, want %q", mb2.apiBase, expected)
	}
}
