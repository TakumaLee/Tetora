package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPairing_IsAllowed_Allowlist(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		HistoryDB: dbPath,
		AccessControl: AccessControlConfig{
			DMPairing: true,
			Allowlists: map[string][]string{
				"telegram": {"12345", "67890"},
			},
		},
	}

	pm := newPairingManager(cfg)

	// User in allowlist should be allowed.
	if !pm.IsAllowed("telegram", "12345") {
		t.Error("expected allowlisted user to be allowed")
	}

	// User not in allowlist should not be allowed (not yet approved).
	if pm.IsAllowed("telegram", "99999") {
		t.Error("expected non-allowlisted user to be denied")
	}
}

func TestPairing_IsAllowed_NoConfig(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		HistoryDB: dbPath,
		AccessControl: AccessControlConfig{
			DMPairing: false, // DM pairing disabled → allow all
		},
	}

	pm := newPairingManager(cfg)

	// All users should be allowed when DM pairing is disabled.
	if !pm.IsAllowed("telegram", "12345") {
		t.Error("expected all users to be allowed when DM pairing disabled")
	}
	if !pm.IsAllowed("slack", "99999") {
		t.Error("expected all users to be allowed when DM pairing disabled")
	}
}

func TestPairing_RequestAndApprove(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		HistoryDB: dbPath,
		AccessControl: AccessControlConfig{
			DMPairing:      true,
			PairingMessage: "Your code: {{.Code}}",
			PairingExpiry:  "15m",
		},
	}

	pm := newPairingManager(cfg)

	// Request pairing.
	msg, err := pm.RequestPairing("telegram", "12345", "testuser")
	if err != nil {
		t.Fatalf("RequestPairing failed: %v", err)
	}

	// Check message contains code.
	if !strings.Contains(msg, "Your code:") {
		t.Errorf("expected message to contain template text, got: %s", msg)
	}

	// Extract code from message.
	parts := strings.Split(msg, ": ")
	if len(parts) != 2 {
		t.Fatalf("unexpected message format: %s", msg)
	}
	code := strings.TrimSpace(parts[1])

	// Verify code is 6 digits.
	if len(code) != 6 {
		t.Errorf("expected 6-digit code, got: %s", code)
	}

	// User should not be allowed yet.
	if pm.IsAllowed("telegram", "12345") {
		t.Error("user should not be allowed before approval")
	}

	// Approve the request.
	req, err := pm.Approve(code)
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}
	if req.UserID != "12345" {
		t.Errorf("expected userID 12345, got: %s", req.UserID)
	}

	// User should now be allowed.
	if !pm.IsAllowed("telegram", "12345") {
		t.Error("user should be allowed after approval")
	}

	// Code should no longer be in pending.
	pending := pm.ListPending()
	for _, p := range pending {
		if p.Code == code {
			t.Error("code should be removed from pending after approval")
		}
	}
}

func TestPairing_RequestAndReject(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		HistoryDB: dbPath,
		AccessControl: AccessControlConfig{
			DMPairing:     true,
			PairingExpiry: "15m",
		},
	}

	pm := newPairingManager(cfg)

	// Request pairing.
	_, err := pm.RequestPairing("telegram", "12345", "testuser")
	if err != nil {
		t.Fatalf("RequestPairing failed: %v", err)
	}

	// Get pending requests.
	pending := pm.ListPending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending request, got: %d", len(pending))
	}
	code := pending[0].Code

	// Reject the request.
	if err := pm.Reject(code); err != nil {
		t.Fatalf("Reject failed: %v", err)
	}

	// Code should no longer be in pending.
	pending = pm.ListPending()
	if len(pending) != 0 {
		t.Errorf("expected 0 pending requests after reject, got: %d", len(pending))
	}

	// User should not be allowed.
	if pm.IsAllowed("telegram", "12345") {
		t.Error("user should not be allowed after rejection")
	}
}

func TestPairing_ExpiredCode(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		HistoryDB: dbPath,
		AccessControl: AccessControlConfig{
			DMPairing:     true,
			PairingExpiry: "1s", // Very short expiry for testing
		},
	}

	pm := newPairingManager(cfg)

	// Request pairing.
	_, err := pm.RequestPairing("telegram", "12345", "testuser")
	if err != nil {
		t.Fatalf("RequestPairing failed: %v", err)
	}

	// Get code.
	pending := pm.ListPending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending request, got: %d", len(pending))
	}
	code := pending[0].Code

	// Wait for expiry.
	time.Sleep(2 * time.Second)

	// Try to approve expired code.
	_, err = pm.Approve(code)
	if err == nil {
		t.Error("expected error when approving expired code")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("expected 'expired' error, got: %v", err)
	}
}

func TestPairing_DuplicateApproval(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		HistoryDB: dbPath,
		AccessControl: AccessControlConfig{
			DMPairing: true,
		},
	}

	pm := newPairingManager(cfg)

	// Request and approve first time.
	msg1, _ := pm.RequestPairing("telegram", "12345", "testuser")
	code1 := extractCode(msg1)
	if _, err := pm.Approve(code1); err != nil {
		t.Fatalf("first approval failed: %v", err)
	}

	// Request and approve again (duplicate).
	msg2, _ := pm.RequestPairing("telegram", "12345", "testuser")
	code2 := extractCode(msg2)
	if _, err := pm.Approve(code2); err != nil {
		t.Fatalf("duplicate approval failed: %v", err)
	}

	// Should still be allowed (idempotent).
	if !pm.IsAllowed("telegram", "12345") {
		t.Error("user should still be allowed after duplicate approval")
	}

	// Check only one entry in DB.
	approved, err := pm.ListApproved()
	if err != nil {
		t.Fatalf("ListApproved failed: %v", err)
	}
	count := 0
	for _, row := range approved {
		if row["user_id"] == "12345" && row["channel"] == "telegram" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 DB entry for duplicate approval, got: %d", count)
	}
}

func TestPairing_ListPending(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		HistoryDB: dbPath,
		AccessControl: AccessControlConfig{
			DMPairing: true,
		},
	}

	pm := newPairingManager(cfg)

	// Create multiple pending requests.
	pm.RequestPairing("telegram", "user1", "User 1")
	pm.RequestPairing("slack", "user2", "User 2")
	pm.RequestPairing("discord", "user3", "User 3")

	// List pending.
	pending := pm.ListPending()
	if len(pending) != 3 {
		t.Errorf("expected 3 pending requests, got: %d", len(pending))
	}

	// Verify channels.
	channels := make(map[string]bool)
	for _, req := range pending {
		channels[req.Channel] = true
	}
	if !channels["telegram"] || !channels["slack"] || !channels["discord"] {
		t.Error("missing expected channels in pending requests")
	}
}

func TestPairing_Revoke(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		HistoryDB: dbPath,
		AccessControl: AccessControlConfig{
			DMPairing: true,
		},
	}

	pm := newPairingManager(cfg)

	// Approve a user.
	msg, _ := pm.RequestPairing("telegram", "12345", "testuser")
	code := extractCode(msg)
	if _, err := pm.Approve(code); err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	// Verify user is allowed.
	if !pm.IsAllowed("telegram", "12345") {
		t.Error("user should be allowed after approval")
	}

	// Revoke access.
	if err := pm.Revoke("telegram", "12345"); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	// User should no longer be allowed.
	if pm.IsAllowed("telegram", "12345") {
		t.Error("user should not be allowed after revocation")
	}
}

func TestPairing_GenerateCode(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		HistoryDB: dbPath,
		AccessControl: AccessControlConfig{
			DMPairing: true,
		},
	}

	pm := newPairingManager(cfg)

	// Generate multiple codes and verify uniqueness.
	codes := make(map[string]bool)
	for i := 0; i < 100; i++ {
		code := pm.generateCode()
		if len(code) != 6 {
			t.Errorf("expected 6-digit code, got: %s", code)
		}
		// Verify all digits.
		for _, c := range code {
			if c < '0' || c > '9' {
				t.Errorf("code contains non-digit character: %s", code)
			}
		}
		codes[code] = true
	}

	// Should have mostly unique codes (collisions possible but rare).
	if len(codes) < 90 {
		t.Errorf("expected mostly unique codes, got %d unique out of 100", len(codes))
	}
}

func TestPairing_PairingMessage(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		HistoryDB: dbPath,
		AccessControl: AccessControlConfig{
			DMPairing:      true,
			PairingMessage: "Welcome! Use code {{.Code}} to pair your device.",
		},
	}

	pm := newPairingManager(cfg)

	msg, err := pm.RequestPairing("telegram", "12345", "testuser")
	if err != nil {
		t.Fatalf("RequestPairing failed: %v", err)
	}

	// Verify template was rendered.
	if !strings.Contains(msg, "Welcome!") {
		t.Errorf("message should contain custom text, got: %s", msg)
	}
	if !strings.Contains(msg, "to pair your device") {
		t.Errorf("message should contain custom text, got: %s", msg)
	}
	// Verify {{.Code}} was replaced.
	if strings.Contains(msg, "{{.Code}}") {
		t.Error("template variable should be replaced")
	}
}

// Helper to extract code from pairing message.
func extractCode(msg string) string {
	// Handle both default and custom messages.
	if strings.Contains(msg, ": ") {
		parts := strings.Split(msg, ": ")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	// Extract 6-digit number from message.
	fields := strings.Fields(msg)
	for _, field := range fields {
		if len(field) == 6 {
			allDigits := true
			for _, c := range field {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return field
			}
		}
	}
	return ""
}

// TestMain ensures DB cleanup after tests.
func init() {
	// Prevent test DBs from lingering.
	os.RemoveAll("test.db")
}
