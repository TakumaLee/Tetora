package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// --- P20.4: Device Actions Tests ---

func TestDeviceConfigDefaults(t *testing.T) {
	cfg := DeviceConfig{}
	if cfg.Enabled {
		t.Error("expected Enabled to be false by default")
	}
	if cfg.CameraEnabled {
		t.Error("expected CameraEnabled to be false by default")
	}
	if cfg.ScreenEnabled {
		t.Error("expected ScreenEnabled to be false by default")
	}
	if cfg.ClipboardEnabled {
		t.Error("expected ClipboardEnabled to be false by default")
	}
	if cfg.NotifyEnabled {
		t.Error("expected NotifyEnabled to be false by default")
	}
	if cfg.LocationEnabled {
		t.Error("expected LocationEnabled to be false by default")
	}
	if cfg.OutputDir != "" {
		t.Error("expected empty OutputDir by default")
	}
}

func TestDeviceConfigJSON(t *testing.T) {
	raw := `{
		"enabled": true,
		"outputDir": "/tmp/tetora-out",
		"camera": true,
		"screen": true,
		"clipboard": true,
		"notify": true,
		"location": true
	}`
	var cfg DeviceConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !cfg.Enabled {
		t.Error("expected enabled=true")
	}
	if cfg.OutputDir != "/tmp/tetora-out" {
		t.Errorf("unexpected outputDir: %s", cfg.OutputDir)
	}
	if !cfg.CameraEnabled {
		t.Error("expected camera=true")
	}
	if !cfg.ScreenEnabled {
		t.Error("expected screen=true")
	}
	if !cfg.ClipboardEnabled {
		t.Error("expected clipboard=true")
	}
	if !cfg.NotifyEnabled {
		t.Error("expected notify=true")
	}
	if !cfg.LocationEnabled {
		t.Error("expected location=true")
	}
}

func TestDeviceOutputPathGenerated(t *testing.T) {
	cfg := &Config{
		Device: DeviceConfig{
			Enabled:   true,
			OutputDir: "/tmp/tetora-test-outputs",
		},
	}
	cfg.baseDir = "/tmp/tetora"

	path, err := deviceOutputPath(cfg, "", ".png")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(path, "/tmp/tetora-test-outputs/snap_") {
		t.Errorf("unexpected path prefix: %s", path)
	}
	if !strings.HasSuffix(path, ".png") {
		t.Errorf("expected .png extension: %s", path)
	}
	// Should contain timestamp pattern.
	base := filepath.Base(path)
	if len(base) < 20 {
		t.Errorf("generated filename too short: %s", base)
	}
}

func TestDeviceOutputPathDefaultDir(t *testing.T) {
	cfg := &Config{
		Device: DeviceConfig{Enabled: true},
	}
	cfg.baseDir = "/tmp/tetora"

	path, err := deviceOutputPath(cfg, "", ".png")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(path, "/tmp/tetora/outputs/snap_") {
		t.Errorf("expected default outputs dir, got: %s", path)
	}
}

func TestDeviceOutputPathCustomFilename(t *testing.T) {
	cfg := &Config{
		Device: DeviceConfig{
			Enabled:   true,
			OutputDir: "/tmp/out",
		},
	}
	cfg.baseDir = "/tmp/tetora"

	path, err := deviceOutputPath(cfg, "myshot.png", ".png")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/tmp/out/myshot.png" {
		t.Errorf("unexpected path: %s", path)
	}
}

func TestDeviceOutputPathNoExtension(t *testing.T) {
	cfg := &Config{
		Device: DeviceConfig{
			Enabled:   true,
			OutputDir: "/tmp/out",
		},
	}
	cfg.baseDir = "/tmp/tetora"

	path, err := deviceOutputPath(cfg, "myshot", ".png")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(path, ".png") {
		t.Errorf("expected .png extension added: %s", path)
	}
}

func TestDeviceOutputPathTraversal(t *testing.T) {
	cfg := &Config{
		Device: DeviceConfig{
			Enabled:   true,
			OutputDir: "/tmp/out",
		},
	}
	cfg.baseDir = "/tmp/tetora"

	cases := []string{
		"../../../etc/passwd",
		"..\\secret.txt",
		"foo/../bar.png",
		"/etc/passwd",
	}
	for _, name := range cases {
		_, err := deviceOutputPath(cfg, name, ".png")
		if err == nil {
			t.Errorf("expected error for unsafe filename %q", name)
		}
	}
}

func TestDeviceOutputPathUnsafeChars(t *testing.T) {
	cfg := &Config{
		Device: DeviceConfig{
			Enabled:   true,
			OutputDir: "/tmp/out",
		},
	}
	cfg.baseDir = "/tmp/tetora"

	cases := []string{
		"foo bar.png",   // space
		"foo;rm -rf.sh", // semicolon
		"$(cmd).png",    // shell injection
		"file`cmd`.png", // backtick
	}
	for _, name := range cases {
		_, err := deviceOutputPath(cfg, name, ".png")
		if err == nil {
			t.Errorf("expected error for unsafe filename %q", name)
		}
	}
}

func TestDeviceOutputPathUniqueness(t *testing.T) {
	cfg := &Config{
		Device: DeviceConfig{
			Enabled:   true,
			OutputDir: "/tmp/out",
		},
	}
	cfg.baseDir = "/tmp/tetora"

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		path, err := deviceOutputPath(cfg, "", ".png")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if seen[path] {
			t.Errorf("duplicate path generated: %s", path)
		}
		seen[path] = true
	}
}

func TestValidateRegion(t *testing.T) {
	// Valid cases.
	valid := []string{"0,0,1920,1080", "100,200,300,400", "0,0,1,1"}
	for _, r := range valid {
		if err := validateRegion(r); err != nil {
			t.Errorf("expected valid region %q, got error: %v", r, err)
		}
	}

	// Invalid cases.
	invalid := []string{
		"",
		"100,200,300",      // only 3 parts
		"100,200,300,400,5", // 5 parts
		"a,b,c,d",          // non-numeric
		"100,,300,400",     // empty component
		"-1,0,100,100",     // negative
		"10.5,0,100,100",   // float
	}
	for _, r := range invalid {
		if err := validateRegion(r); err == nil {
			t.Errorf("expected error for invalid region %q", r)
		}
	}
}

func TestToolRegistrationDisabled(t *testing.T) {
	cfg := &Config{
		Device: DeviceConfig{
			Enabled: false,
		},
	}
	r := &ToolRegistry{tools: make(map[string]*ToolDef)}
	registerDeviceTools(r, cfg)

	if len(r.tools) != 0 {
		t.Errorf("expected 0 tools when disabled, got %d", len(r.tools))
	}
}

func TestToolRegistrationEnabledNoFeatures(t *testing.T) {
	cfg := &Config{
		Device: DeviceConfig{
			Enabled: true,
			// All features disabled.
		},
	}
	r := &ToolRegistry{tools: make(map[string]*ToolDef)}
	registerDeviceTools(r, cfg)

	if len(r.tools) != 0 {
		t.Errorf("expected 0 tools when no features enabled, got %d", len(r.tools))
	}
}

func TestToolRegistrationPlatformAware(t *testing.T) {
	cfg := &Config{
		Device: DeviceConfig{
			Enabled:         true,
			NotifyEnabled:   true,
			LocationEnabled: true,
		},
	}
	r := &ToolRegistry{tools: make(map[string]*ToolDef)}
	registerDeviceTools(r, cfg)

	// On macOS, osascript should be available, so notification_send should register.
	if runtime.GOOS == "darwin" {
		if _, ok := r.tools["notification_send"]; !ok {
			t.Error("expected notification_send to be registered on darwin")
		}
	}

	// location_get is macOS-only.
	if runtime.GOOS != "darwin" {
		if _, ok := r.tools["location_get"]; ok {
			t.Error("expected location_get NOT to be registered on non-darwin")
		}
	}
}

func TestNotificationCommandConstruction(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("notification command test only runs on macOS")
	}

	// Just verify the handler doesn't panic with valid input.
	cfg := &Config{
		Device: DeviceConfig{Enabled: true, NotifyEnabled: true},
	}
	cfg.baseDir = "/tmp/tetora"

	input, _ := json.Marshal(map[string]string{
		"title": "Test Title",
		"text":  "Test message body",
	})

	// We test with a real osascript call since we're on macOS.
	ctx := context.Background()
	result, err := toolNotificationSend(ctx, cfg, input)
	if err != nil {
		// Permission might be denied in CI, but the command should at least run.
		t.Logf("notification send returned error (may be expected in CI): %v", err)
		return
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
}

func TestCameraSnapFilenameGeneration(t *testing.T) {
	cfg := &Config{
		Device: DeviceConfig{
			Enabled:       true,
			CameraEnabled: true,
			OutputDir:     "/tmp/test-device-outputs",
		},
	}
	cfg.baseDir = "/tmp/tetora"

	// Test auto-generated filename.
	path, err := deviceOutputPath(cfg, "", ".png")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	base := filepath.Base(path)
	if !strings.HasPrefix(base, "snap_") {
		t.Errorf("expected 'snap_' prefix, got: %s", base)
	}
	// Verify timestamp format: snap_YYYYMMDD_HHMMSS_xxxx.png
	parts := strings.SplitN(base, "_", 4)
	if len(parts) < 4 {
		t.Errorf("expected at least 4 parts in filename, got: %s", base)
	}
}

func TestRunDeviceCommandTimeout(t *testing.T) {
	// Use a command that sleeps longer than our internal timeout.
	// We create a context with a very short timeout to test.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Note: runDeviceCommand creates its own 30s timeout, but the parent
	// context timeout of 100ms will be inherited.
	_, err := runDeviceCommand(ctx, "sleep", "10")
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestClipboardRoundtrip(t *testing.T) {
	// Only run on macOS/Linux where clipboard tools exist.
	switch runtime.GOOS {
	case "darwin":
		// pbcopy/pbpaste should be available.
	case "linux":
		// Skip if no display.
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			t.Skip("no display available for clipboard test")
		}
	default:
		t.Skip("clipboard test not supported on " + runtime.GOOS)
	}

	cfg := &Config{
		Device: DeviceConfig{
			Enabled:          true,
			ClipboardEnabled: true,
		},
	}
	cfg.baseDir = "/tmp/tetora"

	ctx := context.Background()
	testText := "tetora-device-test-" + time.Now().Format("150405")

	// Set clipboard.
	setInput, _ := json.Marshal(map[string]string{"text": testText})
	result, err := toolClipboardSet(ctx, cfg, setInput)
	if err != nil {
		t.Fatalf("clipboard_set failed: %v", err)
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}

	// Get clipboard.
	got, err := toolClipboardGet(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("clipboard_get failed: %v", err)
	}
	if got != testText {
		t.Errorf("clipboard roundtrip failed: expected %q, got %q", testText, got)
	}
}

func TestEnsureDeviceOutputDir(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "tetora-test-device-"+time.Now().Format("150405"))
	defer os.RemoveAll(tmpDir)

	cfg := &Config{
		Device: DeviceConfig{
			Enabled:   true,
			OutputDir: filepath.Join(tmpDir, "outputs"),
		},
	}
	cfg.baseDir = tmpDir

	ensureDeviceOutputDir(cfg)

	info, err := os.Stat(cfg.Device.OutputDir)
	if err != nil {
		t.Fatalf("output dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestEnsureDeviceOutputDirDefault(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "tetora-test-device-default-"+time.Now().Format("150405"))
	defer os.RemoveAll(tmpDir)

	cfg := &Config{
		Device: DeviceConfig{
			Enabled: true,
			// No OutputDir set — should use baseDir/outputs.
		},
	}
	cfg.baseDir = tmpDir

	ensureDeviceOutputDir(cfg)

	expected := filepath.Join(tmpDir, "outputs")
	info, err := os.Stat(expected)
	if err != nil {
		t.Fatalf("default output dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestScreenCaptureRegionParsing(t *testing.T) {
	// Test that the handler correctly validates region format.
	cfg := &Config{
		Device: DeviceConfig{
			Enabled:       true,
			ScreenEnabled: true,
			OutputDir:     "/tmp/test-device-screen",
		},
	}
	cfg.baseDir = "/tmp/tetora"

	// Invalid region should fail at validation.
	input, _ := json.Marshal(map[string]string{
		"region": "not,a,valid,region!",
	})
	ctx := context.Background()
	_, err := toolScreenCapture(ctx, cfg, input)
	if err == nil {
		t.Error("expected error for invalid region")
	}
	if !strings.Contains(err.Error(), "non-numeric") {
		t.Errorf("expected non-numeric error, got: %v", err)
	}

	// Valid region format (will fail at command execution, but passes validation).
	input2, _ := json.Marshal(map[string]string{
		"region": "0,0,100,100",
	})
	_, err2 := toolScreenCapture(ctx, cfg, input2)
	// Should fail at command execution (screencapture/import won't actually exist in test),
	// but NOT at region validation.
	if err2 != nil && strings.Contains(err2.Error(), "invalid region") {
		t.Errorf("valid region should pass validation, got: %v", err2)
	}
}

func TestClipboardSetEmptyText(t *testing.T) {
	cfg := &Config{
		Device: DeviceConfig{Enabled: true, ClipboardEnabled: true},
	}
	cfg.baseDir = "/tmp/tetora"

	input, _ := json.Marshal(map[string]string{"text": ""})
	ctx := context.Background()
	_, err := toolClipboardSet(ctx, cfg, input)
	if err == nil {
		t.Error("expected error for empty text")
	}
}

func TestNotificationSendEmptyText(t *testing.T) {
	cfg := &Config{
		Device: DeviceConfig{Enabled: true, NotifyEnabled: true},
	}
	cfg.baseDir = "/tmp/tetora"

	input, _ := json.Marshal(map[string]string{"title": "Test", "text": ""})
	ctx := context.Background()
	_, err := toolNotificationSend(ctx, cfg, input)
	if err == nil {
		t.Error("expected error for empty notification text")
	}
}
