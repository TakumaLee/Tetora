package main

// --- P14.1: Discord Components v2 — Tests ---

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- Ed25519 Signature Verification ---

func TestDiscordComponentVerifySignature_Valid(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pubHex := hex.EncodeToString(pub)
	timestamp := "1234567890"
	body := []byte(`{"type":1}`)
	msg := []byte(timestamp + string(body))
	sig := ed25519.Sign(priv, msg)
	sigHex := hex.EncodeToString(sig)

	if !verifyDiscordSignature(pubHex, sigHex, timestamp, body) {
		t.Error("expected valid signature to verify")
	}
}

func TestDiscordComponentVerifySignature_Invalid(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pubHex := hex.EncodeToString(pub)

	// Use a different key to sign — signature will be invalid.
	_, otherPriv, _ := ed25519.GenerateKey(nil)
	timestamp := "1234567890"
	body := []byte(`{"type":1}`)
	msg := []byte(timestamp + string(body))
	sig := ed25519.Sign(otherPriv, msg)
	sigHex := hex.EncodeToString(sig)

	if verifyDiscordSignature(pubHex, sigHex, timestamp, body) {
		t.Error("expected invalid signature to fail verification")
	}
}

func TestDiscordComponentVerifySignature_BadHex(t *testing.T) {
	if verifyDiscordSignature("not-hex", "also-not-hex", "ts", []byte("body")) {
		t.Error("expected bad hex to fail")
	}
}

func TestDiscordComponentVerifySignature_WrongKeySize(t *testing.T) {
	if verifyDiscordSignature("aabb", "ccdd", "ts", []byte("body")) {
		t.Error("expected wrong key size to fail")
	}
}

// --- PING Interaction → PONG ---

func TestDiscordComponentPingPong(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)

	cfg := &Config{}
	cfg.Discord.PublicKey = pubHex
	db := &DiscordBot{cfg: cfg, client: &http.Client{}}

	body := []byte(`{"type":1}`)
	timestamp := "1234567890"
	msg := []byte(timestamp + string(body))
	sig := ed25519.Sign(priv, msg)
	sigHex := hex.EncodeToString(sig)

	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", sigHex)
	req.Header.Set("X-Signature-Timestamp", timestamp)
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp discordInteractionResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Type != interactionResponsePong {
		t.Errorf("expected PONG (type 1), got %d", resp.Type)
	}
}

// --- Invalid Signature → 401 ---

func TestDiscordComponentInvalidSignature401(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)

	cfg := &Config{}
	cfg.Discord.PublicKey = pubHex
	db := &DiscordBot{cfg: cfg, client: &http.Client{}}

	body := []byte(`{"type":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", "deadbeef")
	req.Header.Set("X-Signature-Timestamp", "1234567890")
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// --- Button Interaction Routing ---

func TestDiscordComponentButtonRouting(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)

	cfg := &Config{}
	cfg.Discord.PublicKey = pubHex
	interactions := newDiscordInteractionState()

	interactions.register(&pendingInteraction{
		CustomID:  "test_btn",
		CreatedAt: time.Now(),
		Callback:  func(data discordInteractionData) {},
	})

	db := &DiscordBot{cfg: cfg, client: &http.Client{}, interactions: interactions}

	payload := map[string]any{
		"type":    interactionTypeMessageComponent,
		"id":      "int_1",
		"token":   "tok",
		"version": 1,
		"data":    map[string]any{"custom_id": "test_btn", "component_type": 2},
	}
	body, _ := json.Marshal(payload)
	timestamp := "1234567890"
	msg := []byte(timestamp + string(body))
	sig := ed25519.Sign(priv, msg)
	sigHex := hex.EncodeToString(sig)

	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", sigHex)
	req.Header.Set("X-Signature-Timestamp", timestamp)
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp discordInteractionResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Type != interactionResponseDeferredUpdate {
		t.Errorf("expected DEFERRED_UPDATE (type 6), got %d", resp.Type)
	}
}

// --- Select Menu Value Extraction ---

func TestDiscordComponentSelectMenuValues(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)

	cfg := &Config{}
	cfg.Discord.PublicKey = pubHex
	db := &DiscordBot{cfg: cfg, client: &http.Client{}, interactions: newDiscordInteractionState()}

	payload := map[string]any{
		"type":    interactionTypeMessageComponent,
		"id":      "int_2",
		"token":   "tok",
		"version": 1,
		"data":    map[string]any{"custom_id": "agent_select", "values": []string{"ruri"}},
	}
	body, _ := json.Marshal(payload)
	timestamp := "1234567890"
	msg := []byte(timestamp + string(body))
	sig := ed25519.Sign(priv, msg)
	sigHex := hex.EncodeToString(sig)

	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", sigHex)
	req.Header.Set("X-Signature-Timestamp", timestamp)
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp discordInteractionResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Type != interactionResponseMessage {
		t.Errorf("expected MESSAGE (type 4), got %d", resp.Type)
	}
	if resp.Data == nil || !strings.Contains(resp.Data.Content, "ruri") {
		t.Errorf("expected response to contain selected agent 'ruri', got %v", resp.Data)
	}
}

// --- Modal Submission Parsing ---

func TestDiscordComponentModalSubmitParsing(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)

	cfg := &Config{}
	cfg.Discord.PublicKey = pubHex
	db := &DiscordBot{cfg: cfg, client: &http.Client{}, interactions: newDiscordInteractionState()}

	payload := map[string]any{
		"type":    interactionTypeModalSubmit,
		"id":      "int_3",
		"token":   "tok",
		"version": 1,
		"data": map[string]any{
			"custom_id": "task_form",
			"components": []any{
				map[string]any{
					"type": componentTypeActionRow,
					"components": []any{
						map[string]any{"type": componentTypeTextInput, "custom_id": "title", "value": "My Task"},
					},
				},
				map[string]any{
					"type": componentTypeActionRow,
					"components": []any{
						map[string]any{"type": componentTypeTextInput, "custom_id": "desc", "value": "Do something"},
					},
				},
			},
		},
	}
	body, _ := json.Marshal(payload)
	timestamp := "1234567890"
	msg := []byte(timestamp + string(body))
	sig := ed25519.Sign(priv, msg)
	sigHex := hex.EncodeToString(sig)

	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", sigHex)
	req.Header.Set("X-Signature-Timestamp", timestamp)
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp discordInteractionResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Data == nil || !strings.Contains(resp.Data.Content, "2 fields") {
		t.Errorf("expected response mentioning 2 fields, got %v", resp.Data)
	}
}

// --- Allowed Users Enforcement ---

func TestDiscordComponentAllowedUsersEnforcement(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)

	cfg := &Config{}
	cfg.Discord.PublicKey = pubHex
	interactions := newDiscordInteractionState()
	interactions.register(&pendingInteraction{
		CustomID:   "restricted_btn",
		CreatedAt:  time.Now(),
		AllowedIDs: []string{"user_allowed"},
		Callback:   func(data discordInteractionData) {},
	})

	db := &DiscordBot{cfg: cfg, client: &http.Client{}, interactions: interactions}

	// Interaction from a disallowed user (no member/user = empty userID).
	payload := map[string]any{
		"type":    interactionTypeMessageComponent,
		"id":      "int_4",
		"token":   "tok",
		"version": 1,
		"member":  map[string]any{"user": map[string]any{"id": "user_blocked"}},
		"data":    map[string]any{"custom_id": "restricted_btn"},
	}
	body, _ := json.Marshal(payload)
	timestamp := "1234567890"
	msg := []byte(timestamp + string(body))
	sig := ed25519.Sign(priv, msg)
	sigHex := hex.EncodeToString(sig)

	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", sigHex)
	req.Header.Set("X-Signature-Timestamp", timestamp)
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp discordInteractionResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Data == nil || !strings.Contains(resp.Data.Content, "not allowed") {
		t.Errorf("expected 'not allowed' message, got %v", resp.Data)
	}
}

// --- Component Message Builder JSON ---

func TestDiscordComponentBuilderJSON(t *testing.T) {
	components := []discordComponent{
		discordActionRow(
			discordButton("btn_1", "Click Me", buttonStylePrimary),
			discordButton("btn_2", "Cancel", buttonStyleDanger),
		),
		discordActionRow(
			discordSelectMenu("sel_1", "Choose...", []discordSelectOption{
				{Label: "Option A", Value: "a"},
				{Label: "Option B", Value: "b", Description: "Second option"},
			}),
		),
	}

	data, err := json.Marshal(components)
	if err != nil {
		t.Fatal(err)
	}

	var decoded []map[string]any
	json.Unmarshal(data, &decoded)

	if len(decoded) != 2 {
		t.Fatalf("expected 2 action rows, got %d", len(decoded))
	}

	// First row: buttons.
	row1 := decoded[0]
	if int(row1["type"].(float64)) != componentTypeActionRow {
		t.Errorf("expected action row type %d, got %v", componentTypeActionRow, row1["type"])
	}
	buttons := row1["components"].([]any)
	if len(buttons) != 2 {
		t.Fatalf("expected 2 buttons, got %d", len(buttons))
	}
	btn1 := buttons[0].(map[string]any)
	if btn1["custom_id"] != "btn_1" {
		t.Errorf("expected custom_id 'btn_1', got %v", btn1["custom_id"])
	}
	if int(btn1["style"].(float64)) != buttonStylePrimary {
		t.Errorf("expected primary style, got %v", btn1["style"])
	}

	// Second row: select.
	row2 := decoded[1]
	selects := row2["components"].([]any)
	if len(selects) != 1 {
		t.Fatalf("expected 1 select menu, got %d", len(selects))
	}
	sel := selects[0].(map[string]any)
	if sel["custom_id"] != "sel_1" {
		t.Errorf("expected custom_id 'sel_1', got %v", sel["custom_id"])
	}
	opts := sel["options"].([]any)
	if len(opts) != 2 {
		t.Fatalf("expected 2 options, got %d", len(opts))
	}
}

// --- Modal Builder ---

func TestDiscordComponentModalBuilder(t *testing.T) {
	modal := discordBuildModal("form_1", "Enter Details",
		discordTextInput("name", "Your Name", true),
		discordParagraphInput("bio", "Your Bio", false),
	)

	if modal.Type != interactionResponseModal {
		t.Errorf("expected modal response type %d, got %d", interactionResponseModal, modal.Type)
	}
	if modal.Data == nil {
		t.Fatal("expected modal data")
	}
	if modal.Data.CustomID != "form_1" {
		t.Errorf("expected custom_id 'form_1', got %q", modal.Data.CustomID)
	}
	if modal.Data.Title != "Enter Details" {
		t.Errorf("expected title 'Enter Details', got %q", modal.Data.Title)
	}
	// Components should be wrapped in action rows.
	if len(modal.Data.Components) != 2 {
		t.Fatalf("expected 2 action rows, got %d", len(modal.Data.Components))
	}
	for i, row := range modal.Data.Components {
		if row.Type != componentTypeActionRow {
			t.Errorf("component %d: expected action row, got type %d", i, row.Type)
		}
		if len(row.Components) != 1 {
			t.Errorf("component %d: expected 1 inner component, got %d", i, len(row.Components))
		}
	}
}

// --- Approval Buttons Helper ---

func TestDiscordComponentApprovalButtons(t *testing.T) {
	components := discordApprovalButtons("task123")
	if len(components) != 1 {
		t.Fatalf("expected 1 action row, got %d", len(components))
	}
	row := components[0]
	if row.Type != componentTypeActionRow {
		t.Errorf("expected action row type")
	}
	if len(row.Components) != 2 {
		t.Fatalf("expected 2 buttons, got %d", len(row.Components))
	}
	if row.Components[0].CustomID != "approve:task123" {
		t.Errorf("expected approve custom_id, got %q", row.Components[0].CustomID)
	}
	if row.Components[0].Style != buttonStyleSuccess {
		t.Errorf("expected success style for approve button")
	}
	if row.Components[1].CustomID != "reject:task123" {
		t.Errorf("expected reject custom_id, got %q", row.Components[1].CustomID)
	}
	if row.Components[1].Style != buttonStyleDanger {
		t.Errorf("expected danger style for reject button")
	}
}

// --- Extract Modal Values ---

func TestDiscordComponentExtractModalValues(t *testing.T) {
	components := []discordComponent{
		{Type: componentTypeActionRow, Components: []discordComponent{
			{Type: componentTypeTextInput, CustomID: "field1", Value: "hello"},
		}},
		{Type: componentTypeActionRow, Components: []discordComponent{
			{Type: componentTypeTextInput, CustomID: "field2", Value: "world"},
		}},
	}

	values := extractModalValues(components)
	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(values))
	}
	if values["field1"] != "hello" {
		t.Errorf("expected field1='hello', got %q", values["field1"])
	}
	if values["field2"] != "world" {
		t.Errorf("expected field2='world', got %q", values["field2"])
	}
}

// --- Missing Signature Headers → 401 ---

func TestDiscordComponentMissingSignatureHeaders(t *testing.T) {
	cfg := &Config{}
	cfg.Discord.PublicKey = "aabbccdd" // doesn't matter, headers missing
	db := &DiscordBot{cfg: cfg, client: &http.Client{}}

	body := `{"type":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(body))
	// No signature headers.
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// --- Link Button Builder ---

func TestDiscordComponentLinkButton(t *testing.T) {
	btn := discordLinkButton("https://example.com", "Visit")
	if btn.Type != componentTypeButton {
		t.Errorf("expected button type")
	}
	if btn.Style != buttonStyleLink {
		t.Errorf("expected link style")
	}
	if btn.URL != "https://example.com" {
		t.Errorf("expected URL, got %q", btn.URL)
	}
	if btn.CustomID != "" {
		t.Errorf("expected empty custom_id for link button, got %q", btn.CustomID)
	}
}

// --- Agent Select Menu Helper ---

func TestDiscordComponentAgentSelectMenu(t *testing.T) {
	components := discordAgentSelectMenu([]string{"ruri", "hisui", "kokuyou", "kohaku"})
	if len(components) != 1 {
		t.Fatalf("expected 1 action row, got %d", len(components))
	}
	row := components[0]
	if len(row.Components) != 1 {
		t.Fatalf("expected 1 select menu, got %d", len(row.Components))
	}
	sel := row.Components[0]
	if sel.Type != componentTypeStringSelect {
		t.Errorf("expected string select type")
	}
	if len(sel.Options) != 4 {
		t.Errorf("expected 4 options, got %d", len(sel.Options))
	}
}

// --- No Public Key → 503 ---

func TestDiscordComponentNoPublicKey(t *testing.T) {
	cfg := &Config{}
	// No public key set.
	db := &DiscordBot{cfg: cfg, client: &http.Client{}}

	body := `{"type":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/discord/interactions", strings.NewReader(body))
	req.Header.Set("X-Signature-Ed25519", "aabb")
	req.Header.Set("X-Signature-Timestamp", "123")
	w := httptest.NewRecorder()

	handleDiscordInteraction(db, w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

// --- sliceContainsStr helper ---

func TestDiscordComponentContainsStr(t *testing.T) {
	if !sliceContainsStr([]string{"a", "b", "c"}, "b") {
		t.Error("expected true for 'b' in [a,b,c]")
	}
	if sliceContainsStr([]string{"a", "b"}, "c") {
		t.Error("expected false for 'c' in [a,b]")
	}
	if sliceContainsStr(nil, "a") {
		t.Error("expected false for nil slice")
	}
}

// --- interactionUserID ---

func TestDiscordComponentInteractionUserID(t *testing.T) {
	// Guild interaction (member).
	i := &discordInteraction{
		Member: &struct {
			User discordUser `json:"user"`
		}{User: discordUser{ID: "guild_user"}},
	}
	if got := interactionUserID(i); got != "guild_user" {
		t.Errorf("expected 'guild_user', got %q", got)
	}

	// DM interaction (user).
	i2 := &discordInteraction{
		User: &discordUser{ID: "dm_user"},
	}
	if got := interactionUserID(i2); got != "dm_user" {
		t.Errorf("expected 'dm_user', got %q", got)
	}

	// Neither.
	i3 := &discordInteraction{}
	if got := interactionUserID(i3); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

