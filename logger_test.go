package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLogger_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(LevelInfo, FormatText, &buf)
	l.out = &buf

	l.Debug("should not appear")
	l.Info("should appear")
	l.Warn("also appears")

	out := buf.String()
	if strings.Contains(out, "should not appear") {
		t.Error("debug message should be filtered at info level")
	}
	if !strings.Contains(out, "should appear") {
		t.Error("info message should appear at info level")
	}
	if !strings.Contains(out, "also appears") {
		t.Error("warn message should appear at info level")
	}
}

func TestLogger_LevelDebugPassesAll(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(LevelDebug, FormatText, &buf)
	l.out = &buf

	l.Debug("d")
	l.Info("i")
	l.Warn("w")
	l.Error("e")

	out := buf.String()
	for _, msg := range []string{"d", "i", "w", "e"} {
		if !strings.Contains(out, msg) {
			t.Errorf("message %q should appear at debug level", msg)
		}
	}
}

func TestLogger_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(LevelInfo, FormatJSON, &buf)
	l.out = &buf

	l.Info("test message", "key1", "val1", "key2", 42)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbuf: %s", err, buf.String())
	}
	if entry["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", entry["level"])
	}
	if entry["msg"] != "test message" {
		t.Errorf("msg = %v, want 'test message'", entry["msg"])
	}
	fields, ok := entry["fields"].(map[string]any)
	if !ok {
		t.Fatal("fields not present or not a map")
	}
	if fields["key1"] != "val1" {
		t.Errorf("fields.key1 = %v, want val1", fields["key1"])
	}
	if fields["key2"] != float64(42) {
		t.Errorf("fields.key2 = %v, want 42", fields["key2"])
	}
}

func TestLogger_TextFormat(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(LevelInfo, FormatText, &buf)
	l.out = &buf

	l.Info("server started", "addr", ":7777")

	out := buf.String()
	if !strings.Contains(out, "INFO") {
		t.Error("text output should contain INFO")
	}
	if !strings.Contains(out, "server started") {
		t.Error("text output should contain message")
	}
	if !strings.Contains(out, "addr=:7777") {
		t.Error("text output should contain fields")
	}
}

func TestLogger_TraceIDInOutput(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(LevelInfo, FormatText, &buf)
	l.out = &buf

	l.log(LevelInfo, "http-abc123", "test msg")

	out := buf.String()
	if !strings.Contains(out, "[http-abc123]") {
		t.Errorf("trace ID not in text output: %s", out)
	}

	// JSON format.
	buf.Reset()
	l.format = FormatJSON
	l.log(LevelInfo, "tg-def456", "test msg")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry["traceId"] != "tg-def456" {
		t.Errorf("traceId = %v, want tg-def456", entry["traceId"])
	}
}

func TestLogger_NoTraceID(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(LevelInfo, FormatJSON, &buf)
	l.out = &buf

	l.Info("no trace")

	var entry map[string]any
	json.Unmarshal(buf.Bytes(), &entry)
	if _, exists := entry["traceId"]; exists {
		t.Error("traceId should not be present when empty")
	}
}

func TestLogger_ContextMethods(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(LevelInfo, FormatJSON, &buf)
	l.out = &buf

	ctx := withTraceID(context.Background(), "ctx-aabbcc")
	l.InfoCtx(ctx, "from context")

	var entry map[string]any
	json.Unmarshal(buf.Bytes(), &entry)
	if entry["traceId"] != "ctx-aabbcc" {
		t.Errorf("traceId = %v, want ctx-aabbcc", entry["traceId"])
	}
}

func TestLogger_EmptyContextNoTrace(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(LevelInfo, FormatJSON, &buf)
	l.out = &buf

	l.InfoCtx(context.Background(), "bare context")

	var entry map[string]any
	json.Unmarshal(buf.Bytes(), &entry)
	if _, exists := entry["traceId"]; exists {
		t.Error("traceId should not be present for bare context")
	}
}

func TestLogger_ConcurrentWrites(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(LevelInfo, FormatText, &buf)
	l.out = &buf

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			l.Info("concurrent", "n", n)
		}(i)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 100 {
		t.Errorf("got %d lines, want 100", len(lines))
	}
}

func TestLogger_Rotation(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	l := newLogger(LevelInfo, FormatText, os.Stderr)
	l.maxSize = 200 // Tiny: rotate after 200 bytes
	l.maxFiles = 3
	l.setupFile(logPath)
	defer l.Close()

	// Write enough to trigger rotation.
	for i := 0; i < 20; i++ {
		l.Info("rotation test line with some padding to fill up space", "i", i)
	}

	// Check that rotated file exists.
	if _, err := os.Stat(logPath + ".1"); os.IsNotExist(err) {
		t.Error("rotated file .1 should exist")
	}
}

func TestLogger_RotationMaxFiles(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	l := newLogger(LevelInfo, FormatText, os.Stderr)
	l.maxSize = 100
	l.maxFiles = 2
	l.setupFile(logPath)
	defer l.Close()

	// Write lots of data to trigger multiple rotations.
	for i := 0; i < 50; i++ {
		l.Info("fill data fill data fill data fill data fill data", "i", i)
	}

	// .1 and .2 should exist, .3 should not.
	if _, err := os.Stat(logPath + ".1"); os.IsNotExist(err) {
		t.Error(".1 should exist")
	}
	if _, err := os.Stat(logPath + ".3"); !os.IsNotExist(err) {
		t.Error(".3 should NOT exist (maxFiles=2)")
	}
}

func TestLogger_ParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  LogLevel
	}{
		{"debug", LevelDebug},
		{"DEBUG", LevelDebug},
		{"info", LevelInfo},
		{"INFO", LevelInfo},
		{"warn", LevelWarn},
		{"warning", LevelWarn},
		{"error", LevelError},
		{"ERROR", LevelError},
		{"unknown", LevelInfo},
		{"", LevelInfo},
	}
	for _, tt := range tests {
		got := parseLevel(tt.input)
		if got != tt.want {
			t.Errorf("parseLevel(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestLogger_ParseFormat(t *testing.T) {
	if parseFormat("json") != FormatJSON {
		t.Error("parseFormat(json) should be FormatJSON")
	}
	if parseFormat("JSON") != FormatJSON {
		t.Error("parseFormat(JSON) should be FormatJSON")
	}
	if parseFormat("text") != FormatText {
		t.Error("parseFormat(text) should be FormatText")
	}
	if parseFormat("") != FormatText {
		t.Error("parseFormat('') should default to FormatText")
	}
}

func TestLogger_FieldsOddCount(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(LevelInfo, FormatJSON, &buf)
	l.out = &buf

	l.Info("odd fields", "key1", "val1", "orphan")

	var entry map[string]any
	json.Unmarshal(buf.Bytes(), &entry)
	fields := entry["fields"].(map[string]any)
	if fields["key1"] != "val1" {
		t.Error("key1 should be val1")
	}
	if fields["_extra"] != "orphan" {
		t.Errorf("_extra = %v, want orphan", fields["_extra"])
	}
}

func TestBuildFieldMap_Empty(t *testing.T) {
	m := buildFieldMap(nil)
	if m != nil {
		t.Error("nil fields should return nil map")
	}
	m = buildFieldMap([]any{})
	if m != nil {
		t.Error("empty fields should return nil map")
	}
}

func TestFormatJSON_NoFields(t *testing.T) {
	out := formatJSON("2026-01-01T00:00:00Z", "INFO", "", "hello", nil)
	var entry map[string]any
	if err := json.Unmarshal([]byte(out), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, exists := entry["fields"]; exists {
		t.Error("fields should not be present when nil")
	}
	if _, exists := entry["traceId"]; exists {
		t.Error("traceId should not be present when empty")
	}
}
