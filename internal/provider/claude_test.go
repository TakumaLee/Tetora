package provider

import (
	"encoding/json"
	"strings"
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

func TestBuildResultFromStream_QuotaExhaustion_ErrorSurfaced(t *testing.T) {
	// Claude CLI returns is_error=true with subtype="error_during_execution" and
	// the quota message in result. The message must be surfaced to Error so the
	// dispatch layer can detect it via IsTransientError.
	msg := &claudeStreamMsg{
		Type:    "result",
		Subtype: "error_during_execution",
		IsError: true,
		Result:  "You've hit your limit. Resets at 2026-04-20T12:00:00Z.",
	}
	r := buildResultFromStream(msg, nil, 0)
	if !r.IsError {
		t.Fatal("expected IsError=true")
	}
	if !strings.Contains(strings.ToLower(r.Error), "hit your limit") {
		t.Errorf("expected quota message surfaced in Error, got %q", r.Error)
	}
	if r.Error == "error_during_execution" {
		t.Error("quota message must replace the generic sentinel")
	}
}

func TestBuildResultFromParsed_QuotaExhaustion_ErrorSurfaced(t *testing.T) {
	co := claudeOutput{
		Type:    "result",
		Subtype: "error_during_execution",
		IsError: true,
		Result:  "You've hit your limit. Resets at 2026-04-20T12:00:00Z.",
	}
	r := buildResultFromParsed(co)
	if !r.IsError {
		t.Fatal("expected IsError=true")
	}
	if !strings.Contains(strings.ToLower(r.Error), "hit your limit") {
		t.Errorf("expected quota message surfaced in Error, got %q", r.Error)
	}
	if r.Error == "error_during_execution" {
		t.Error("quota message must replace the generic sentinel")
	}
}

func TestIsTransientError_QuotaMessage(t *testing.T) {
	if !IsTransientError("You've hit your limit. Resets at 2026-04-20T12:00:00Z.") {
		t.Error("quota exhaustion message must be classified as transient")
	}
}

func TestIsTransientError_ErrorDuringExecutionNonTransient(t *testing.T) {
	if IsTransientError("error_during_execution") {
		t.Error("bare 'error_during_execution' sentinel must not be transient (no retry signal)")
	}
}
