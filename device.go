package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// --- P20.4: Device Actions ---

// DeviceConfig configures device action tools (camera, screen, clipboard, etc.).
type DeviceConfig struct {
	Enabled          bool   `json:"enabled"`
	OutputDir        string `json:"outputDir,omitempty"`   // default: baseDir + "/outputs"
	CameraEnabled    bool   `json:"camera,omitempty"`      // enable camera_snap tool
	ScreenEnabled    bool   `json:"screen,omitempty"`      // enable screen_capture tool
	ClipboardEnabled bool   `json:"clipboard,omitempty"`   // enable clipboard tools
	NotifyEnabled    bool   `json:"notify,omitempty"`      // enable notification_send tool
	LocationEnabled  bool   `json:"location,omitempty"`    // enable location_get tool (macOS only)
}

// safeFilenameRe matches only safe filename characters: alphanumeric, dash, underscore, dot.
var safeFilenameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// deviceOutputPath generates a safe output path in the configured outputDir.
// If filename is empty, a timestamped name with random suffix is generated.
// The ext parameter should include the leading dot (e.g., ".png").
func deviceOutputPath(cfg *Config, filename, ext string) (string, error) {
	outDir := cfg.Device.OutputDir
	if outDir == "" {
		outDir = filepath.Join(cfg.baseDir, "outputs")
	}

	if filename == "" {
		// Generate timestamp + random suffix.
		now := time.Now().Format("20060102_150405")
		b := make([]byte, 4)
		rand.Read(b)
		suffix := hex.EncodeToString(b)
		filename = fmt.Sprintf("snap_%s_%s%s", now, suffix, ext)
	} else {
		// Validate filename: no path separators, no traversal, only safe chars.
		if strings.Contains(filename, "/") || strings.Contains(filename, "\\") || strings.Contains(filename, "..") {
			return "", fmt.Errorf("unsafe filename: %q", filename)
		}
		// Strip any directory component just in case.
		filename = filepath.Base(filename)
		if !safeFilenameRe.MatchString(filename) {
			return "", fmt.Errorf("unsafe filename characters: %q", filename)
		}
		// Ensure extension.
		if filepath.Ext(filename) == "" {
			filename += ext
		}
	}

	return filepath.Join(outDir, filename), nil
}

// runDeviceCommand executes an external command with a 30-second timeout.
func runDeviceCommand(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("command timed out after 30s: %s", name)
	}
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("command %s failed: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// runDeviceCommandWithStdin executes an external command piping input to stdin.
func runDeviceCommandWithStdin(ctx context.Context, input, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("command timed out after 30s: %s", name)
	}
	if err != nil {
		return fmt.Errorf("command %s failed: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// --- Tool Handlers ---

// toolCameraSnap takes a photo using the device camera.
func toolCameraSnap(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Filename string `json:"filename"`
	}
	json.Unmarshal(input, &args)

	outPath, err := deviceOutputPath(cfg, args.Filename, ".png")
	if err != nil {
		return "", err
	}

	switch runtime.GOOS {
	case "darwin":
		_, err = runDeviceCommand(ctx, "imagesnap", "-w", "1", outPath)
	case "linux":
		_, err = runDeviceCommand(ctx, "fswebcam", "-r", "1280x720", "--no-banner", outPath)
	default:
		return "", fmt.Errorf("camera_snap not supported on %s", runtime.GOOS)
	}
	if err != nil {
		return "", fmt.Errorf("camera capture failed: %w", err)
	}

	result, _ := json.Marshal(map[string]string{
		"path":     outPath,
		"platform": runtime.GOOS,
	})
	logInfo("camera snap taken", "path", outPath)
	return string(result), nil
}

// toolScreenCapture takes a screenshot of the desktop.
func toolScreenCapture(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Filename string `json:"filename"`
		Region   string `json:"region"` // "x,y,w,h" optional
	}
	json.Unmarshal(input, &args)

	outPath, err := deviceOutputPath(cfg, args.Filename, ".png")
	if err != nil {
		return "", err
	}

	switch runtime.GOOS {
	case "darwin":
		cmdArgs := []string{"-x"} // silent (no shutter sound)
		if args.Region != "" {
			// Parse region "x,y,w,h" → screencapture -R x,y,w,h
			if err := validateRegion(args.Region); err != nil {
				return "", err
			}
			cmdArgs = append(cmdArgs, "-R", args.Region)
		}
		cmdArgs = append(cmdArgs, outPath)
		_, err = runDeviceCommand(ctx, "screencapture", cmdArgs...)
	case "linux":
		if args.Region != "" {
			if err := validateRegion(args.Region); err != nil {
				return "", err
			}
			// ImageMagick import with crop geometry.
			parts := strings.Split(args.Region, ",")
			geom := fmt.Sprintf("%sx%s+%s+%s", parts[2], parts[3], parts[0], parts[1])
			_, err = runDeviceCommand(ctx, "import", "-window", "root", "-crop", geom, outPath)
		} else {
			// Try import first, fall back to gnome-screenshot.
			if _, lookErr := exec.LookPath("import"); lookErr == nil {
				_, err = runDeviceCommand(ctx, "import", "-window", "root", outPath)
			} else {
				_, err = runDeviceCommand(ctx, "gnome-screenshot", "-f", outPath)
			}
		}
	default:
		return "", fmt.Errorf("screen_capture not supported on %s", runtime.GOOS)
	}
	if err != nil {
		return "", fmt.Errorf("screen capture failed: %w", err)
	}

	result, _ := json.Marshal(map[string]string{
		"path":     outPath,
		"platform": runtime.GOOS,
	})
	logInfo("screen capture taken", "path", outPath)
	return string(result), nil
}

// validateRegion checks that a region string is in "x,y,w,h" format with positive integers.
func validateRegion(region string) error {
	parts := strings.Split(region, ",")
	if len(parts) != 4 {
		return fmt.Errorf("invalid region format %q: expected 'x,y,w,h'", region)
	}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return fmt.Errorf("invalid region format %q: empty component", region)
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return fmt.Errorf("invalid region format %q: non-numeric component %q", region, p)
			}
		}
	}
	return nil
}

// toolClipboardGet reads text from the system clipboard.
func toolClipboardGet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return runDeviceCommand(ctx, "pbpaste")
	case "linux":
		if _, err := exec.LookPath("xclip"); err == nil {
			return runDeviceCommand(ctx, "xclip", "-selection", "clipboard", "-o")
		}
		return runDeviceCommand(ctx, "xsel", "--clipboard", "--output")
	default:
		return "", fmt.Errorf("clipboard_get not supported on %s", runtime.GOOS)
	}
}

// toolClipboardSet writes text to the system clipboard.
func toolClipboardSet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	switch runtime.GOOS {
	case "darwin":
		if err := runDeviceCommandWithStdin(ctx, args.Text, "pbcopy"); err != nil {
			return "", err
		}
	case "linux":
		if _, lookErr := exec.LookPath("xclip"); lookErr == nil {
			if err := runDeviceCommandWithStdin(ctx, args.Text, "xclip", "-selection", "clipboard"); err != nil {
				return "", err
			}
		} else {
			if err := runDeviceCommandWithStdin(ctx, args.Text, "xsel", "--clipboard", "--input"); err != nil {
				return "", err
			}
		}
	default:
		return "", fmt.Errorf("clipboard_set not supported on %s", runtime.GOOS)
	}

	logInfo("clipboard set", "length", len(args.Text))
	return "ok", nil
}

// toolNotificationSend shows a desktop notification.
func toolNotificationSend(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Title string `json:"title"`
		Text  string `json:"text"`
		Sound string `json:"sound"` // macOS only
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Title == "" {
		args.Title = "Tetora"
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display notification %q with title %q`, args.Text, args.Title)
		if args.Sound != "" {
			script += fmt.Sprintf(` sound name %q`, args.Sound)
		}
		_, err := runDeviceCommand(ctx, "osascript", "-e", script)
		if err != nil {
			return "", fmt.Errorf("notification failed: %w", err)
		}
	case "linux":
		_, err := runDeviceCommand(ctx, "notify-send", args.Title, args.Text)
		if err != nil {
			return "", fmt.Errorf("notification failed: %w", err)
		}
	default:
		return "", fmt.Errorf("notification_send not supported on %s", runtime.GOOS)
	}

	logInfo("notification sent", "title", args.Title)
	return "ok", nil
}

// toolLocationGet gets the device location (macOS only, via CoreLocation).
func toolLocationGet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("location_get is only supported on macOS")
	}

	// Swift helper that uses CoreLocation to get a single location fix.
	swiftCode := `
import CoreLocation
import Foundation

class Delegate: NSObject, CLLocationManagerDelegate {
    let sem = DispatchSemaphore(value: 0)
    var result: String = ""
    func locationManager(_ m: CLLocationManager, didUpdateLocations locs: [CLLocation]) {
        if let loc = locs.last {
            let ts = ISO8601DateFormatter().string(from: loc.timestamp)
            result = "{\"latitude\":\(loc.coordinate.latitude),\"longitude\":\(loc.coordinate.longitude),\"accuracy\":\(loc.horizontalAccuracy),\"timestamp\":\"\(ts)\"}"
        }
        sem.signal()
    }
    func locationManager(_ m: CLLocationManager, didFailWithError e: Error) {
        result = "{\"error\":\"\(e.localizedDescription)\"}"
        sem.signal()
    }
}
let d = Delegate()
let m = CLLocationManager()
m.delegate = d
m.desiredAccuracy = kCLLocationAccuracyBest
m.requestLocation()
_ = d.sem.wait(timeout: .now() + 15)
print(d.result)
`
	out, err := runDeviceCommand(ctx, "swift", "-e", swiftCode)
	if err != nil {
		return "", fmt.Errorf("location_get failed: %w", err)
	}
	if out == "" {
		return "", fmt.Errorf("location_get: no result (timeout or permission denied)")
	}
	return out, nil
}

// registerDeviceTools registers all device action tools that are available on the current platform.
// This is called from registerBuiltins in tool.go.
func registerDeviceTools(r *ToolRegistry, cfg *Config) {
	if !cfg.Device.Enabled {
		return
	}

	goos := runtime.GOOS

	// camera_snap
	if cfg.Device.CameraEnabled {
		var cmdAvail bool
		switch goos {
		case "darwin":
			_, err := exec.LookPath("imagesnap")
			cmdAvail = err == nil
		case "linux":
			_, err := exec.LookPath("fswebcam")
			cmdAvail = err == nil
		}
		if cmdAvail {
			r.Register(&ToolDef{
				Name:        "camera_snap",
				Description: "Take a photo using the device camera and save to the output directory",
				InputSchema: json.RawMessage(`{
					"type": "object",
					"properties": {
						"filename": {"type": "string", "description": "Output filename (optional, auto-generated if omitted)"}
					}
				}`),
				Handler:     toolCameraSnap,
				Builtin:     true,
				RequireAuth: true,
			})
			logDebug("device tool registered", "tool", "camera_snap", "platform", goos)
		}
	}

	// screen_capture
	if cfg.Device.ScreenEnabled {
		var cmdAvail bool
		switch goos {
		case "darwin":
			_, err := exec.LookPath("screencapture")
			cmdAvail = err == nil
		case "linux":
			_, err1 := exec.LookPath("import")
			_, err2 := exec.LookPath("gnome-screenshot")
			cmdAvail = err1 == nil || err2 == nil
		}
		if cmdAvail {
			r.Register(&ToolDef{
				Name:        "screen_capture",
				Description: "Take a screenshot of the desktop, optionally capturing a specific region",
				InputSchema: json.RawMessage(`{
					"type": "object",
					"properties": {
						"filename": {"type": "string", "description": "Output filename (optional, auto-generated if omitted)"},
						"region": {"type": "string", "description": "Capture region as 'x,y,w,h' (optional, full screen if omitted)"}
					}
				}`),
				Handler:     toolScreenCapture,
				Builtin:     true,
				RequireAuth: true,
			})
			logDebug("device tool registered", "tool", "screen_capture", "platform", goos)
		}
	}

	// clipboard_get
	if cfg.Device.ClipboardEnabled {
		var cmdAvail bool
		switch goos {
		case "darwin":
			_, err := exec.LookPath("pbpaste")
			cmdAvail = err == nil
		case "linux":
			_, err1 := exec.LookPath("xclip")
			_, err2 := exec.LookPath("xsel")
			cmdAvail = err1 == nil || err2 == nil
		}
		if cmdAvail {
			r.Register(&ToolDef{
				Name:        "clipboard_get",
				Description: "Read the current text content from the system clipboard",
				InputSchema: json.RawMessage(`{
					"type": "object",
					"properties": {}
				}`),
				Handler: toolClipboardGet,
				Builtin: true,
			})
			logDebug("device tool registered", "tool", "clipboard_get", "platform", goos)
		}
	}

	// clipboard_set
	if cfg.Device.ClipboardEnabled {
		var cmdAvail bool
		switch goos {
		case "darwin":
			_, err := exec.LookPath("pbcopy")
			cmdAvail = err == nil
		case "linux":
			_, err1 := exec.LookPath("xclip")
			_, err2 := exec.LookPath("xsel")
			cmdAvail = err1 == nil || err2 == nil
		}
		if cmdAvail {
			r.Register(&ToolDef{
				Name:        "clipboard_set",
				Description: "Write text content to the system clipboard",
				InputSchema: json.RawMessage(`{
					"type": "object",
					"properties": {
						"text": {"type": "string", "description": "Text to copy to clipboard"}
					},
					"required": ["text"]
				}`),
				Handler: toolClipboardSet,
				Builtin: true,
			})
			logDebug("device tool registered", "tool", "clipboard_set", "platform", goos)
		}
	}

	// notification_send
	if cfg.Device.NotifyEnabled {
		var cmdAvail bool
		switch goos {
		case "darwin":
			_, err := exec.LookPath("osascript")
			cmdAvail = err == nil
		case "linux":
			_, err := exec.LookPath("notify-send")
			cmdAvail = err == nil
		}
		if cmdAvail {
			r.Register(&ToolDef{
				Name:        "notification_send",
				Description: "Show a desktop notification with a title and message text",
				InputSchema: json.RawMessage(`{
					"type": "object",
					"properties": {
						"title": {"type": "string", "description": "Notification title (default: 'Tetora')"},
						"text": {"type": "string", "description": "Notification body text"},
						"sound": {"type": "string", "description": "Sound name (macOS only, optional)"}
					},
					"required": ["text"]
				}`),
				Handler: toolNotificationSend,
				Builtin: true,
			})
			logDebug("device tool registered", "tool", "notification_send", "platform", goos)
		}
	}

	// location_get (macOS only)
	if cfg.Device.LocationEnabled && goos == "darwin" {
		if _, err := exec.LookPath("swift"); err == nil {
			r.Register(&ToolDef{
				Name:        "location_get",
				Description: "Get the device's current geographic location (macOS only, uses CoreLocation)",
				InputSchema: json.RawMessage(`{
					"type": "object",
					"properties": {}
				}`),
				Handler: toolLocationGet,
				Builtin: true,
			})
			logDebug("device tool registered", "tool", "location_get", "platform", goos)
		}
	}
}

// Ensure outputDir exists. Called from main.go during daemon startup.
func ensureDeviceOutputDir(cfg *Config) {
	outDir := cfg.Device.OutputDir
	if outDir == "" {
		outDir = filepath.Join(cfg.baseDir, "outputs")
	}
	os.MkdirAll(outDir, 0o755)
	logInfo("device actions enabled", "outputDir", outDir, "platform", runtime.GOOS)
}
