package main

import (
	"testing"
)

// ---------------------------------------------------------------------------
// resolveEnvRef
// ---------------------------------------------------------------------------

func TestResolveEnvRef_NoDollarPrefix(t *testing.T) {
	got := resolveEnvRef("plaintext", "testField")
	if got != "plaintext" {
		t.Errorf("resolveEnvRef(%q) = %q, want %q", "plaintext", got, "plaintext")
	}
}

func TestResolveEnvRef_WithSetEnvVar(t *testing.T) {
	t.Setenv("TETORA_TEST_SECRET", "mysecret")

	got := resolveEnvRef("$TETORA_TEST_SECRET", "testField")
	if got != "mysecret" {
		t.Errorf("resolveEnvRef(%q) = %q, want %q", "$TETORA_TEST_SECRET", got, "mysecret")
	}
}

func TestResolveEnvRef_WithUnsetEnvVar(t *testing.T) {
	got := resolveEnvRef("$TETORA_UNSET_VAR_12345", "testField")
	if got != "" {
		t.Errorf("resolveEnvRef(%q) = %q, want %q", "$TETORA_UNSET_VAR_12345", got, "")
	}
}

func TestResolveEnvRef_DollarOnly(t *testing.T) {
	got := resolveEnvRef("$", "testField")
	if got != "$" {
		t.Errorf("resolveEnvRef(%q) = %q, want %q", "$", got, "$")
	}
}

func TestResolveEnvRef_EmptyString(t *testing.T) {
	got := resolveEnvRef("", "testField")
	if got != "" {
		t.Errorf("resolveEnvRef(%q) = %q, want %q", "", got, "")
	}
}
