package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"sync"

	"tetora/internal/crypto"
)

// globalEncKey holds the resolved encryption key for use by standalone functions
// (e.g., session.go) that don't have access to *Config.
var (
	globalEncKeyMu  sync.RWMutex
	globalEncKeyVal string
)

// setGlobalEncryptionKey sets the package-level encryption key (called at startup).
func setGlobalEncryptionKey(key string) {
	globalEncKeyMu.Lock()
	globalEncKeyVal = key
	globalEncKeyMu.Unlock()
}

// globalEncryptionKey returns the current encryption key.
func globalEncryptionKey() string {
	globalEncKeyMu.RLock()
	defer globalEncKeyMu.RUnlock()
	return globalEncKeyVal
}

// encryptField encrypts a value using the config's encryption key.
// No-op if no encryption key is configured.
func encryptField(cfg *Config, value string) string {
	key := resolveEncryptionKey(cfg)
	if key == "" || value == "" {
		return value
	}
	enc, err := crypto.Encrypt(value, key)
	if err != nil {
		return value // fallback to plaintext on error
	}
	return enc
}

// decryptField decrypts a value using the config's encryption key.
// Gracefully returns original value if not encrypted or key is missing.
func decryptField(cfg *Config, value string) string {
	key := resolveEncryptionKey(cfg)
	if key == "" || value == "" {
		return value
	}
	dec, err := crypto.Decrypt(value, key)
	if err != nil {
		return value // fallback to original on error
	}
	return dec
}

// resolveEncryptionKey returns the encryption key from config.
// Priority: cfg.EncryptionKey > cfg.OAuth.EncryptionKey.
func resolveEncryptionKey(cfg *Config) string {
	if cfg.EncryptionKey != "" {
		return cfg.EncryptionKey
	}
	return cfg.OAuth.EncryptionKey
}

// --- P27.2: Migration CLI ---

// cmdMigrateEncrypt encrypts existing plaintext rows in the DB.
func cmdMigrateEncrypt() {
	cfg := loadConfig(findConfigPath())
	key := resolveEncryptionKey(cfg)
	if key == "" {
		fmt.Fprintln(os.Stderr, "Error: no encryptionKey configured. Set it in config.json first.")
		os.Exit(1)
	}

	dbPath := cfg.HistoryDB
	if dbPath == "" {
		fmt.Fprintln(os.Stderr, "Error: no historyDB configured.")
		os.Exit(1)
	}

	total := 0

	// Encrypt session message content.
	rows, err := queryDB(dbPath, `SELECT id, content FROM session_messages WHERE content != ''`)
	if err == nil {
		for _, row := range rows {
			content := jsonStr(row["content"])
			if content == "" {
				continue
			}
			// Check if already encrypted (hex-decodable + decryptable = skip).
			if _, decErr := hex.DecodeString(content); decErr == nil {
				continue // likely already encrypted
			}
			enc, err := crypto.Encrypt(content, key)
			if err != nil {
				continue
			}
			id := int(jsonFloat(row["id"]))
			updateSQL := fmt.Sprintf(`UPDATE session_messages SET content = '%s' WHERE id = %d`,
				escapeSQLite(enc), id)
			queryDB(dbPath, updateSQL)
			total++
		}
	}
	fmt.Printf("Encrypted %d session messages\n", total)

	// Encrypt contact PII.
	contactCount := 0
	rows, err = queryDB(dbPath, `SELECT id, email, phone, notes FROM contacts`)
	if err == nil {
		for _, row := range rows {
			id := jsonStr(row["id"])
			email := jsonStr(row["email"])
			phone := jsonStr(row["phone"])
			notes := jsonStr(row["notes"])

			updates := []string{}
			if email != "" {
				if _, decErr := hex.DecodeString(email); decErr != nil {
					if enc, err := crypto.Encrypt(email, key); err == nil {
						updates = append(updates, fmt.Sprintf("email = '%s'", escapeSQLite(enc)))
					}
				}
			}
			if phone != "" {
				if _, decErr := hex.DecodeString(phone); decErr != nil {
					if enc, err := crypto.Encrypt(phone, key); err == nil {
						updates = append(updates, fmt.Sprintf("phone = '%s'", escapeSQLite(enc)))
					}
				}
			}
			if notes != "" {
				if _, decErr := hex.DecodeString(notes); decErr != nil {
					if enc, err := crypto.Encrypt(notes, key); err == nil {
						updates = append(updates, fmt.Sprintf("notes = '%s'", escapeSQLite(enc)))
					}
				}
			}
			if len(updates) > 0 {
				sql := fmt.Sprintf("UPDATE contacts SET %s WHERE id = '%s'",
					joinStrings(updates, ", "), escapeSQLite(id))
				queryDB(dbPath, sql)
				contactCount++
			}
		}
	}
	fmt.Printf("Encrypted %d contacts\n", contactCount)

	// Encrypt expense descriptions.
	expenseCount := 0
	rows, err = queryDB(dbPath, `SELECT rowid, description FROM expenses WHERE description != ''`)
	if err == nil {
		for _, row := range rows {
			desc := jsonStr(row["description"])
			if desc == "" {
				continue
			}
			if _, decErr := hex.DecodeString(desc); decErr == nil {
				continue
			}
			enc, err := crypto.Encrypt(desc, key)
			if err != nil {
				continue
			}
			id := int(jsonFloat(row["rowid"]))
			updateSQL := fmt.Sprintf(`UPDATE expenses SET description = '%s' WHERE rowid = %d`,
				escapeSQLite(enc), id)
			queryDB(dbPath, updateSQL)
			expenseCount++
		}
	}
	fmt.Printf("Encrypted %d expenses\n", expenseCount)

	// Encrypt habit log notes.
	habitCount := 0
	rows, err = queryDB(dbPath, `SELECT id, note FROM habit_logs WHERE note != ''`)
	if err == nil {
		for _, row := range rows {
			note := jsonStr(row["note"])
			if note == "" {
				continue
			}
			if _, decErr := hex.DecodeString(note); decErr == nil {
				continue
			}
			enc, err := crypto.Encrypt(note, key)
			if err != nil {
				continue
			}
			id := jsonStr(row["id"])
			updateSQL := fmt.Sprintf(`UPDATE habit_logs SET note = '%s' WHERE id = '%s'`,
				escapeSQLite(enc), escapeSQLite(id))
			queryDB(dbPath, updateSQL)
			habitCount++
		}
	}
	fmt.Printf("Encrypted %d habit logs\n", habitCount)

	fmt.Printf("\nTotal: %d rows encrypted\n", total+contactCount+expenseCount+habitCount)
}
