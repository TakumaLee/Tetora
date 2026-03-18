package main

// device.go — thin shim over internal/tools (device actions).
// Unexported shims kept for device_test.go (package main).

import (
	"context"
	"encoding/json"

	"tetora/internal/tools"
)

// Shims for test compatibility (device_test.go is package main).

func registerDeviceTools(r *ToolRegistry, cfg *Config) { tools.RegisterDeviceTools(r, cfg) }
func ensureDeviceOutputDir(cfg *Config)                 { tools.EnsureDeviceOutputDir(cfg) }

func deviceOutputPath(cfg *Config, filename, ext string) (string, error) {
	return tools.DeviceOutputPath(cfg, filename, ext)
}

func toolCameraSnap(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return tools.ToolCameraSnap(ctx, cfg, input)
}

func toolScreenCapture(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return tools.ToolScreenCapture(ctx, cfg, input)
}

func toolClipboardGet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return tools.ToolClipboardGet(ctx, cfg, input)
}

func toolClipboardSet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return tools.ToolClipboardSet(ctx, cfg, input)
}

func toolNotificationSend(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return tools.ToolNotificationSend(ctx, cfg, input)
}

func toolLocationGet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return tools.ToolLocationGet(ctx, cfg, input)
}

func validateRegion(region string) error                                                      { return tools.ValidateRegion(region) }
func runDeviceCommand(ctx context.Context, name string, args ...string) (string, error)       { return tools.RunDeviceCommand(ctx, name, args...) }
func runDeviceCommandWithStdin(ctx context.Context, input, name string, args ...string) error { return tools.RunDeviceCommandWithStdin(ctx, input, name, args...) }
