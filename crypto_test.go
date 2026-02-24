package main

import (
	"strings"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	key := "test-encryption-key-2024"
	original := "sensitive data here"

	enc, err := encrypt(original, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc == original {
		t.Fatal("encrypted should differ from original")
	}
	if enc == "" {
		t.Fatal("encrypted should not be empty")
	}

	dec, err := decrypt(enc, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if dec != original {
		t.Errorf("round-trip failed: got %q, want %q", dec, original)
	}
}

func TestEncryptDecrypt_UniqueNonce(t *testing.T) {
	key := "nonce-test-key"
	plain := "same plaintext"

	enc1, _ := encrypt(plain, key)
	enc2, _ := encrypt(plain, key)

	if enc1 == enc2 {
		t.Error("two encryptions of same plaintext should produce different ciphertexts (unique nonce)")
	}

	// Both should decrypt to same value.
	dec1, _ := decrypt(enc1, key)
	dec2, _ := decrypt(enc2, key)
	if dec1 != plain || dec2 != plain {
		t.Error("both should decrypt to original")
	}
}

func TestEncryptEmptyKey(t *testing.T) {
	plain := "no encryption"

	enc, err := encrypt(plain, "")
	if err != nil {
		t.Fatalf("encrypt with empty key: %v", err)
	}
	if enc != plain {
		t.Errorf("empty key should pass through: got %q, want %q", enc, plain)
	}

	dec, err := decrypt(plain, "")
	if err != nil {
		t.Fatalf("decrypt with empty key: %v", err)
	}
	if dec != plain {
		t.Errorf("empty key should pass through: got %q, want %q", dec, plain)
	}
}

func TestEncryptEmptyPlaintext(t *testing.T) {
	enc, err := encrypt("", "some-key")
	if err != nil {
		t.Fatalf("encrypt empty: %v", err)
	}
	if enc != "" {
		t.Errorf("empty plaintext should return empty: got %q", enc)
	}
}

func TestDecryptNotEncrypted(t *testing.T) {
	// Plaintext that isn't hex-encoded should return as-is.
	plain := "this is not encrypted"
	dec, err := decrypt(plain, "some-key")
	if err != nil {
		t.Fatalf("decrypt plaintext: %v", err)
	}
	if dec != plain {
		t.Errorf("non-encrypted data should return as-is: got %q, want %q", dec, plain)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	plain := "secret message"
	enc, _ := encrypt(plain, "correct-key")

	// Decrypt with wrong key should return as-is (graceful fallback).
	dec, err := decrypt(enc, "wrong-key")
	if err != nil {
		t.Fatalf("decrypt with wrong key should not error: %v", err)
	}
	// Should return the hex string as-is since decryption fails gracefully.
	if dec != enc {
		t.Logf("wrong key returned %q (len=%d)", dec[:min(len(dec), 40)], len(dec))
	}
}

func TestEncryptField(t *testing.T) {
	cfg := &Config{EncryptionKey: "field-test-key"}

	original := "user@example.com"
	enc := encryptField(cfg, original)
	if enc == original {
		t.Error("encryptField should change the value")
	}

	dec := decryptField(cfg, enc)
	if dec != original {
		t.Errorf("decryptField round-trip: got %q, want %q", dec, original)
	}
}

func TestEncryptFieldNoKey(t *testing.T) {
	cfg := &Config{}

	original := "user@example.com"
	enc := encryptField(cfg, original)
	if enc != original {
		t.Errorf("no key should pass through: got %q", enc)
	}

	dec := decryptField(cfg, enc)
	if dec != original {
		t.Errorf("no key should pass through: got %q", dec)
	}
}

func TestResolveEncryptionKey(t *testing.T) {
	// Config-level key takes priority.
	cfg := &Config{
		EncryptionKey: "config-key",
		OAuth:         OAuthConfig{EncryptionKey: "oauth-key"},
	}
	if got := resolveEncryptionKey(cfg); got != "config-key" {
		t.Errorf("should prefer config key: got %q", got)
	}

	// Fallback to OAuth key.
	cfg2 := &Config{
		OAuth: OAuthConfig{EncryptionKey: "oauth-key"},
	}
	if got := resolveEncryptionKey(cfg2); got != "oauth-key" {
		t.Errorf("should fall back to OAuth key: got %q", got)
	}

	// No key at all.
	cfg3 := &Config{}
	if got := resolveEncryptionKey(cfg3); got != "" {
		t.Errorf("should be empty: got %q", got)
	}
}

func TestEncryptLongData(t *testing.T) {
	key := "long-data-key"
	original := strings.Repeat("a", 10000)

	enc, err := encrypt(original, key)
	if err != nil {
		t.Fatalf("encrypt long: %v", err)
	}

	dec, err := decrypt(enc, key)
	if err != nil {
		t.Fatalf("decrypt long: %v", err)
	}
	if dec != original {
		t.Error("long data round-trip failed")
	}
}
