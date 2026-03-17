package main

import (
	"testing"

	"tetora/internal/config"
)

// ---------------------------------------------------------------------------
// config.ResolveEnvRef
// ---------------------------------------------------------------------------

func TestResolveEnvRef_NoDollarPrefix(t *testing.T) {
	got := config.ResolveEnvRef("plaintext", "testField")
	if got != "plaintext" {
		t.Errorf("config.ResolveEnvRef(%q) = %q, want %q", "plaintext", got, "plaintext")
	}
}

func TestResolveEnvRef_WithSetEnvVar(t *testing.T) {
	t.Setenv("TETORA_TEST_SECRET", "mysecret")

	got := config.ResolveEnvRef("$TETORA_TEST_SECRET", "testField")
	if got != "mysecret" {
		t.Errorf("config.ResolveEnvRef(%q) = %q, want %q", "$TETORA_TEST_SECRET", got, "mysecret")
	}
}

func TestResolveEnvRef_WithUnsetEnvVar(t *testing.T) {
	got := config.ResolveEnvRef("$TETORA_UNSET_VAR_12345", "testField")
	if got != "" {
		t.Errorf("config.ResolveEnvRef(%q) = %q, want %q", "$TETORA_UNSET_VAR_12345", got, "")
	}
}

func TestResolveEnvRef_DollarOnly(t *testing.T) {
	got := config.ResolveEnvRef("$", "testField")
	if got != "$" {
		t.Errorf("config.ResolveEnvRef(%q) = %q, want %q", "$", got, "$")
	}
}

func TestResolveEnvRef_EmptyString(t *testing.T) {
	got := config.ResolveEnvRef("", "testField")
	if got != "" {
		t.Errorf("config.ResolveEnvRef(%q) = %q, want %q", "", got, "")
	}
}
