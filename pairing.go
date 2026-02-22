package main

import (
	"crypto/rand"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// --- Types ---

type PairingManager struct {
	mu      sync.RWMutex
	cfg     *Config
	pending map[string]*PairingRequest // code → request
}

type PairingRequest struct {
	Code      string    `json:"code"`
	Channel   string    `json:"channel"` // "telegram", "slack", "discord", "whatsapp"
	UserID    string    `json:"userId"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type AccessControlConfig struct {
	DMPairing      bool                `json:"dmPairing,omitempty"`
	PairingMessage string              `json:"pairingMessage,omitempty"` // template with {{.Code}}
	PairingExpiry  string              `json:"pairingExpiry,omitempty"`  // duration string, default "15m"
	Allowlists     map[string][]string `json:"allowlists,omitempty"`     // channel → list of user IDs
}

// --- Core Functions ---

func newPairingManager(cfg *Config) *PairingManager {
	pm := &PairingManager{
		cfg:     cfg,
		pending: make(map[string]*PairingRequest),
	}
	if err := pm.initPairingDB(); err != nil {
		logWarn("init pairing db failed", "error", err)
	}
	// Cleanup expired pending requests periodically.
	go pm.cleanupExpired()
	return pm
}

// cleanupExpired removes expired pending requests every minute.
func (pm *PairingManager) cleanupExpired() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		pm.mu.Lock()
		now := time.Now()
		for code, req := range pm.pending {
			if now.After(req.ExpiresAt) {
				delete(pm.pending, code)
			}
		}
		pm.mu.Unlock()
	}
}

// IsAllowed checks if a user is allowed to interact with the bot.
// Check order: 1) allowlist, 2) approved in DB, 3) DM pairing disabled → allow all
func (pm *PairingManager) IsAllowed(channel, userID string) bool {
	// If DM pairing is disabled, allow all.
	if !pm.cfg.AccessControl.DMPairing {
		return true
	}

	// Check allowlist first.
	if allowlist, ok := pm.cfg.AccessControl.Allowlists[channel]; ok {
		for _, id := range allowlist {
			if id == userID {
				return true
			}
		}
	}

	// Check DB for approval.
	return pm.isApprovedInDB(channel, userID)
}

// RequestPairing generates a 6-digit code and stores the pending request.
// Returns the formatted pairing message.
func (pm *PairingManager) RequestPairing(channel, userID, username string) (string, error) {
	code := pm.generateCode()

	// Parse expiry duration (default 15m).
	expiryStr := pm.cfg.AccessControl.PairingExpiry
	if expiryStr == "" {
		expiryStr = "15m"
	}
	expiry, err := time.ParseDuration(expiryStr)
	if err != nil {
		expiry = 15 * time.Minute
	}

	now := time.Now()
	req := &PairingRequest{
		Code:      code,
		Channel:   channel,
		UserID:    userID,
		Username:  username,
		CreatedAt: now,
		ExpiresAt: now.Add(expiry),
	}

	pm.mu.Lock()
	pm.pending[code] = req
	pm.mu.Unlock()

	// Format message using template.
	msg := pm.cfg.AccessControl.PairingMessage
	if msg == "" {
		msg = "Please approve this request using the code: {{.Code}}"
	}
	msg = strings.ReplaceAll(msg, "{{.Code}}", code)

	return msg, nil
}

// Approve finds a pending request by code, stores approval in DB, and removes from pending.
func (pm *PairingManager) Approve(code string) (*PairingRequest, error) {
	pm.mu.Lock()
	req, ok := pm.pending[code]
	if !ok {
		pm.mu.Unlock()
		return nil, fmt.Errorf("invalid or expired code")
	}

	// Check expiry.
	if time.Now().After(req.ExpiresAt) {
		delete(pm.pending, code)
		pm.mu.Unlock()
		return nil, fmt.Errorf("code expired")
	}

	// Remove from pending.
	delete(pm.pending, code)
	pm.mu.Unlock()

	// Store approval in DB.
	if err := pm.storeApproval(req.Channel, req.UserID, req.Username); err != nil {
		return nil, fmt.Errorf("store approval failed: %w", err)
	}

	return req, nil
}

// Reject removes a pending request by code.
func (pm *PairingManager) Reject(code string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, ok := pm.pending[code]; !ok {
		return fmt.Errorf("invalid or expired code")
	}

	delete(pm.pending, code)
	return nil
}

// ListPending returns all pending pairing requests.
func (pm *PairingManager) ListPending() []*PairingRequest {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var result []*PairingRequest
	for _, req := range pm.pending {
		result = append(result, req)
	}
	return result
}

// ListApproved returns all approved users from DB.
func (pm *PairingManager) ListApproved() ([]map[string]any, error) {
	sql := `SELECT channel, user_id, username, approved_at FROM pairing_approved ORDER BY approved_at DESC`
	rows, err := queryDB(pm.cfg.HistoryDB, sql)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// Revoke removes an approved user from DB.
func (pm *PairingManager) Revoke(channel, userID string) error {
	return pm.removeApproval(channel, userID)
}

// --- DB Storage Helpers ---

// initPairingDB creates the pairing_approved table if it doesn't exist.
func (pm *PairingManager) initPairingDB() error {
	sql := `CREATE TABLE IF NOT EXISTS pairing_approved (
		channel TEXT NOT NULL,
		user_id TEXT NOT NULL,
		username TEXT NOT NULL,
		approved_at TEXT NOT NULL,
		PRIMARY KEY(channel, user_id)
	)`
	cmd := exec.Command("sqlite3", pm.cfg.HistoryDB, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3: %s: %w", string(out), err)
	}
	return nil
}

// isApprovedInDB checks if a user is approved in the database.
func (pm *PairingManager) isApprovedInDB(channel, userID string) bool {
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM pairing_approved WHERE channel = '%s' AND user_id = '%s'`,
		escapeSQLite(channel), escapeSQLite(userID))
	rows, err := queryDB(pm.cfg.HistoryDB, sql)
	if err != nil || len(rows) == 0 {
		return false
	}
	cnt, ok := rows[0]["cnt"].(float64)
	return ok && cnt > 0
}

// storeApproval stores a user approval in the database.
// Uses INSERT OR REPLACE to handle duplicate approvals.
func (pm *PairingManager) storeApproval(channel, userID, username string) error {
	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO pairing_approved (channel, user_id, username, approved_at)
		 VALUES ('%s', '%s', '%s', datetime('now'))`,
		escapeSQLite(channel), escapeSQLite(userID), escapeSQLite(username))
	cmd := exec.Command("sqlite3", pm.cfg.HistoryDB, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3: %s: %w", string(out), err)
	}
	return nil
}

// removeApproval removes a user approval from the database.
func (pm *PairingManager) removeApproval(channel, userID string) error {
	sql := fmt.Sprintf(
		`DELETE FROM pairing_approved WHERE channel = '%s' AND user_id = '%s'`,
		escapeSQLite(channel), escapeSQLite(userID))
	cmd := exec.Command("sqlite3", pm.cfg.HistoryDB, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3: %s: %w", string(out), err)
	}
	return nil
}

// generateCode generates a random 6-digit numeric code.
func (pm *PairingManager) generateCode() string {
	for {
		// Generate 3 random bytes (24 bits).
		b := make([]byte, 3)
		if _, err := rand.Read(b); err != nil {
			// Fallback to timestamp-based code.
			return fmt.Sprintf("%06d", time.Now().Unix()%1000000)
		}
		// Convert to integer and mod 1000000 to get 6 digits.
		num := int(b[0])<<16 | int(b[1])<<8 | int(b[2])
		code := num % 1000000
		// Ensure it's 6 digits (pad with zeros if needed).
		codeStr := fmt.Sprintf("%06d", code)

		// Check uniqueness.
		pm.mu.RLock()
		_, exists := pm.pending[codeStr]
		pm.mu.RUnlock()
		if !exists {
			return codeStr
		}
	}
}
