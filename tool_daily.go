package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// registerDailyTools registers daily utility tools (weather, currency, RSS,
// translate, image gen, audio normalize, notes, knowledge ingest).
// Note: most handler functions are defined in their own files.
func registerDailyTools(r *ToolRegistry, cfg *Config, enabled func(string) bool) {
	// --- P22.2: Weather Tools ---
	if enabled("weather_current") && cfg.Weather.Enabled {
		r.Register(&ToolDef{
			Name:        "weather_current",
			Description: "Get current weather for a location using Open-Meteo (free, no API key)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"location": {"type": "string", "description": "City name (e.g. 'Tokyo', 'New York')"}
				}
			}`),
			Handler: toolWeatherCurrent,
			Builtin: true,
		})
	}
	if enabled("weather_forecast") && cfg.Weather.Enabled {
		r.Register(&ToolDef{
			Name:        "weather_forecast",
			Description: "Get weather forecast for a location (up to 7 days) using Open-Meteo",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"location": {"type": "string", "description": "City name"},
					"days": {"type": "integer", "description": "Forecast days (1-7, default 3)"}
				}
			}`),
			Handler: toolWeatherForecast,
			Builtin: true,
		})
	}

	// --- P22.2: Currency Tools ---
	if enabled("currency_convert") && cfg.Currency.Enabled {
		r.Register(&ToolDef{
			Name:        "currency_convert",
			Description: "Convert currency using Frankfurter API (free, no API key)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"amount": {"type": "number", "description": "Amount to convert"},
					"from": {"type": "string", "description": "Source currency code (e.g. 'USD')"},
					"to": {"type": "string", "description": "Target currency code (e.g. 'JPY')"}
				},
				"required": ["amount", "from", "to"]
			}`),
			Handler: toolCurrencyConvert,
			Builtin: true,
		})
	}
	if enabled("currency_rates") && cfg.Currency.Enabled {
		r.Register(&ToolDef{
			Name:        "currency_rates",
			Description: "Get latest exchange rates from Frankfurter API",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"base": {"type": "string", "description": "Base currency code (default 'USD')"},
					"currencies": {"type": "string", "description": "Comma-separated target currencies (e.g. 'JPY,EUR,TWD')"}
				}
			}`),
			Handler: toolCurrencyRates,
			Builtin: true,
		})
	}

	// --- P22.2: RSS Tools ---
	if enabled("rss_read") && cfg.RSS.Enabled {
		r.Register(&ToolDef{
			Name:        "rss_read",
			Description: "Read items from an RSS/Atom feed",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "Feed URL to read"},
					"limit": {"type": "integer", "description": "Max items to return (default 10)"}
				}
			}`),
			Handler: toolRSSRead,
			Builtin: true,
		})
	}
	if enabled("rss_list") && cfg.RSS.Enabled {
		r.Register(&ToolDef{
			Name:        "rss_list",
			Description: "List configured default RSS feeds",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler: toolRSSList,
			Builtin: true,
		})
	}

	// --- P22.2: Translate Tools ---
	if enabled("translate") && cfg.Translate.Enabled {
		r.Register(&ToolDef{
			Name:        "translate",
			Description: "Translate text between languages (via Lingva or DeepL)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"text": {"type": "string", "description": "Text to translate"},
					"from": {"type": "string", "description": "Source language code (e.g. 'en', 'ja', 'auto')"},
					"to": {"type": "string", "description": "Target language code (e.g. 'ja', 'en')"}
				},
				"required": ["text", "to"]
			}`),
			Handler: toolTranslate,
			Builtin: true,
		})
	}
	if enabled("detect_language") && cfg.Translate.Enabled {
		r.Register(&ToolDef{
			Name:        "detect_language",
			Description: "Detect the language of input text",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"text": {"type": "string", "description": "Text to detect language for"}
				},
				"required": ["text"]
			}`),
			Handler: toolDetectLanguage,
			Builtin: true,
		})
	}

	// --- P22.3: Image Generation ---
	if enabled("image_generate") && cfg.ImageGen.Enabled {
		r.Register(&ToolDef{
			Name:        "image_generate",
			Description: "Generate an image using DALL-E (costs $0.04-0.12 per image)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"prompt": {"type": "string", "description": "Image description prompt"},
					"size": {"type": "string", "description": "Image size: 1024x1024 (default), 1024x1792, 1792x1024"},
					"quality": {"type": "string", "description": "Quality: standard (default) or hd"}
				},
				"required": ["prompt"]
			}`),
			Handler: toolImageGenerate,
			Builtin: true,
		})
	}
	if enabled("image_generate_status") && cfg.ImageGen.Enabled {
		r.Register(&ToolDef{
			Name:        "image_generate_status",
			Description: "Check today's image generation usage and remaining quota",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler: toolImageGenerateStatus,
			Builtin: true,
		})
	}

	// --- P19.4: Notes/Obsidian Integration ---
	if enabled("note_create") && cfg.Notes.Enabled {
		r.Register(&ToolDef{
			Name:        "note_create",
			Description: "Create a new note in the Obsidian vault. Supports nested paths (e.g. 'daily/2024-01-15'). Auto-appends .md if no extension given.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Note name or path (e.g. 'meeting-notes', 'project/ideas')"},
					"content": {"type": "string", "description": "Note content (markdown)"}
				},
				"required": ["name", "content"]
			}`),
			Handler: toolNoteCreate,
			Builtin: true,
		})
	}

	if enabled("note_read") && cfg.Notes.Enabled {
		r.Register(&ToolDef{
			Name:        "note_read",
			Description: "Read a note from the Obsidian vault. Returns content, tags, and wikilinks.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Note name or path"}
				},
				"required": ["name"]
			}`),
			Handler: toolNoteRead,
			Builtin: true,
		})
	}

	if enabled("note_append") && cfg.Notes.Enabled {
		r.Register(&ToolDef{
			Name:        "note_append",
			Description: "Append content to an existing note (creates if not exists).",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Note name or path"},
					"content": {"type": "string", "description": "Content to append"}
				},
				"required": ["name", "content"]
			}`),
			Handler: toolNoteAppend,
			Builtin: true,
		})
	}

	if enabled("note_list") && cfg.Notes.Enabled {
		r.Register(&ToolDef{
			Name:        "note_list",
			Description: "List notes in the vault. Optionally filter by path prefix.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"prefix": {"type": "string", "description": "Path prefix to filter (e.g. 'daily/', 'project/')"}
				}
			}`),
			Handler: toolNoteList,
			Builtin: true,
		})
	}

	if enabled("note_search") && cfg.Notes.Enabled {
		r.Register(&ToolDef{
			Name:        "note_search",
			Description: "Search notes using TF-IDF full-text search. Returns ranked results with snippets.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"max_results": {"type": "number", "description": "Maximum results to return (default 5)"}
				},
				"required": ["query"]
			}`),
			Handler: toolNoteSearch,
			Builtin: true,
		})
	}

	// --- Learning Loop: store_lesson ---
	if enabled("store_lesson") && cfg.Notes.Enabled {
		r.Register(&ToolDef{
			Name:        "store_lesson",
			Description: "Store a lesson learned into the vault and lessons file. Triggers auto-embedding into semantic memory for future retrieval.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"category": {"type": "string", "description": "Lesson category (e.g. 'go', 'workflow', 'git', 'debugging')"},
					"lesson": {"type": "string", "description": "The lesson learned (concise, actionable)"},
					"source": {"type": "string", "description": "Where the lesson came from (e.g. agent name, user correction)"},
					"tags": {"type": "array", "items": {"type": "string"}, "description": "Optional tags for searchability"}
				},
				"required": ["category", "lesson"]
			}`),
			Handler: toolStoreLesson,
			Builtin: true,
		})
	}

	// --- P21.3: Note Dedup & Source Audit ---
	if enabled("note_dedup") && cfg.Notes.Enabled {
		r.Register(&ToolDef{
			Name:        "note_dedup",
			Description: "Scan notes vault for duplicate files by content hash. Returns duplicate groups and optionally auto-deletes.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"auto_delete": {"type": "boolean", "description": "If true, delete duplicate files keeping the first occurrence (default false)"},
					"prefix": {"type": "string", "description": "Only scan notes under this path prefix (optional)"}
				}
			}`),
			Handler:     toolNoteDedup,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("source_audit") {
		r.Register(&ToolDef{
			Name:        "source_audit",
			Description: "Compare expected sources against actual notes in the vault. Reports missing, extra, and counts.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"expected": {"type": "array", "items": {"type": "string"}, "description": "List of expected note paths relative to vault"},
					"prefix": {"type": "string", "description": "Notes directory prefix to scan (optional)"}
				},
				"required": ["expected"]
			}`),
			Handler: toolSourceAudit,
			Builtin: true,
		})
	}

	// --- P21.5: Sitemap Ingest Pipeline ---
	if enabled("web_crawl") && cfg.Notes.Enabled {
		r.Register(&ToolDef{
			Name:        "web_crawl",
			Description: "Fetch a sitemap and import web pages into the notes vault. Supports sitemap.xml, sitemapindex, and single URL mode. Content is stripped of HTML and saved as markdown.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "URL of sitemap.xml or single page to ingest"},
					"mode": {"type": "string", "description": "Mode: 'sitemap' (parse sitemap, default) or 'single' (single page)"},
					"include": {"type": "array", "items": {"type": "string"}, "description": "Glob patterns to include (applied to URL path)"},
					"exclude": {"type": "array", "items": {"type": "string"}, "description": "Glob patterns to exclude"},
					"prefix": {"type": "string", "description": "Note path prefix (e.g. 'docs/example')"},
					"dedup": {"type": "boolean", "description": "Skip pages with identical content hash (default false)"},
					"max_pages": {"type": "number", "description": "Maximum pages to import (default 500)"},
					"concurrency": {"type": "number", "description": "Concurrent fetch workers (default 3)"}
				},
				"required": ["url"]
			}`),
			Handler:     toolWebCrawl,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("source_audit_url") && cfg.Notes.Enabled {
		r.Register(&ToolDef{
			Name:        "source_audit_url",
			Description: "Compare a sitemap's URLs against imported notes to find missing pages",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"sitemap_url": {"type": "string", "description": "URL of the sitemap to audit against"},
					"prefix": {"type": "string", "description": "Note path prefix to check"}
				},
				"required": ["sitemap_url"]
			}`),
			Handler: toolSourceAuditURL,
			Builtin: true,
		})
	}

	// --- P27.0: Audio Normalize ---
	if enabled("audio_normalize") {
		r.Register(&ToolDef{
			Name:        "audio_normalize",
			Description: "Normalize audio file volume using ffmpeg loudnorm (LUFS). Supports WAV, MP3, FLAC, etc.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Path to audio file"},
					"target_lufs": {"type": "number", "description": "Target loudness in LUFS (default -14)"},
					"output": {"type": "string", "description": "Output path (default: overwrite original)"}
				},
				"required": ["path"]
			}`),
			Handler:     toolAudioNormalize,
			Builtin:     true,
			RequireAuth: true,
		})
	}
}

// --- P27.0: Audio Normalize Handler ---

// toolAudioNormalize normalizes audio volume using ffmpeg loudnorm filter.
func toolAudioNormalize(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Path       string  `json:"path"`
		TargetLUFS float64 `json:"target_lufs"`
		Output     string  `json:"output"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if args.TargetLUFS == 0 {
		args.TargetLUFS = -14
	}

	// Validate ffmpeg is available.
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH — install it first")
	}

	// Validate input file exists.
	if _, err := os.Stat(args.Path); err != nil {
		return "", fmt.Errorf("input file not found: %w", err)
	}

	// Determine output path.
	outputPath := args.Output
	inPlace := false
	if outputPath == "" {
		// In-place: normalize to temp, then copy back.
		tmpDir, err := os.MkdirTemp("", "tetora-audio-*")
		if err != nil {
			return "", fmt.Errorf("create temp dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)
		outputPath = filepath.Join(tmpDir, filepath.Base(args.Path))
		inPlace = true
	}

	// Build ffmpeg command.
	loudnormFilter := fmt.Sprintf("loudnorm=I=%.1f:TP=-1:LRA=11", args.TargetLUFS)
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, ffmpegPath, "-i", args.Path,
		"-af", loudnormFilter, "-y", outputPath)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errStr := stderr.String()
		if len(errStr) > 500 {
			errStr = errStr[:500] + "..."
		}
		return "", fmt.Errorf("ffmpeg failed: %w\nstderr: %s", err, errStr)
	}

	// If in-place, copy back.
	if inPlace {
		data, err := os.ReadFile(outputPath)
		if err != nil {
			return "", fmt.Errorf("read normalized file: %w", err)
		}
		if err := os.WriteFile(args.Path, data, 0o644); err != nil {
			return "", fmt.Errorf("write back: %w", err)
		}
		outputPath = args.Path
	}

	result := map[string]any{
		"original_path": args.Path,
		"output_path":   outputPath,
		"target_lufs":   args.TargetLUFS,
		"message":       fmt.Sprintf("normalized to %.1f LUFS", args.TargetLUFS),
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}
