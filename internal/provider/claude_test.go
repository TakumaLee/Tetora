package provider

import (
	"encoding/json"
	"testing"
)

func TestBuildResultFromParsed_SuccessSubtypeNormalized(t *testing.T) {
	// CLI edge case: is_error=true but subtype="success" — should not display "success" as error.
	co := claudeOutput{
		Type:    "result",
		Subtype: "success",
		IsError: true,
	}
	r := buildResultFromParsed(co)
	if !r.IsError {
		t.Fatal("expected IsError=true")
	}
	if r.Error == "success" {
		t.Error("error message must not be 'success' (confusing display)")
	}
	if r.Error != "error_during_execution" {
		t.Errorf("expected 'error_during_execution' sentinel, got %q", r.Error)
	}
}

func TestBuildResultFromParsed_EmptySubtypeNormalized(t *testing.T) {
	co := claudeOutput{
		Type:    "result",
		Subtype: "",
		IsError: true,
	}
	r := buildResultFromParsed(co)
	if !r.IsError {
		t.Fatal("expected IsError=true")
	}
	if r.Error == "" {
		t.Error("error message must not be empty")
	}
}

func TestBuildResultFromParsed_RealErrorSubtypePreserved(t *testing.T) {
	co := claudeOutput{
		Type:    "result",
		Subtype: "error_during_execution",
		IsError: true,
	}
	r := buildResultFromParsed(co)
	if !r.IsError {
		t.Fatal("expected IsError=true")
	}
	if r.Error != "error_during_execution" {
		t.Errorf("expected 'error_during_execution', got %q", r.Error)
	}
}

func TestBuildResultFromStream_SuccessSubtypeNormalized(t *testing.T) {
	// Streaming path: same edge case.
	// Must normalize to "error_during_execution" — executeStreaming uses this exact
	// sentinel to decide whether to replace with non-JSON CLI output (e.g. "api 400").
	msg := &claudeStreamMsg{
		Type:    "result",
		Subtype: "success",
		IsError: true,
	}
	r := buildResultFromStream(msg, nil, 0)
	if !r.IsError {
		t.Fatal("expected IsError=true")
	}
	if r.Error == "success" {
		t.Error("error message must not be 'success' (confusing display)")
	}
	if r.Error != "error_during_execution" {
		t.Errorf("expected 'error_during_execution' sentinel, got %q", r.Error)
	}
}

func TestParseClaudeOutput_IsErrorWithSuccessSubtype(t *testing.T) {
	// End-to-end: parse full JSON with is_error=true, subtype="success".
	raw, _ := json.Marshal(map[string]any{
		"type":     "result",
		"subtype":  "success",
		"is_error": true,
	})
	r := ParseClaudeOutput(raw, nil, 0)
	if !r.IsError {
		t.Fatal("expected IsError=true")
	}
	if r.Error == "success" {
		t.Errorf("error message must not be 'success', got %q", r.Error)
	}
}
