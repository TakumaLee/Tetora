package tools

import (
	"encoding/json"

	"tetora/internal/config"
)

// DailyDeps holds pre-built handler functions for daily utility tools.
// The root package constructs these closures (which capture root-only types
// like Config.Weather.Location and NotesService) and passes them in; this
// package only owns the registration logic and JSON schemas.
type DailyDeps struct {
	// Weather
	WeatherCurrent  Handler
	WeatherForecast Handler

	// Currency
	CurrencyConvert Handler
	CurrencyRates   Handler

	// RSS
	RSSRead Handler
	RSSList Handler

	// Translate
	Translate      Handler
	DetectLanguage Handler

	// Notes / Obsidian
	NoteCreate Handler
	NoteRead   Handler
	NoteAppend Handler
	NoteList   Handler
	NoteSearch Handler

	// Learning loop
	StoreLesson Handler

	// Note dedup & source audit
	NoteDedup   Handler
	SourceAudit Handler

	// Sitemap ingest pipeline
	WebCrawl      Handler
	SourceAuditURL Handler

	// Audio
	AudioNormalize Handler
}

// RegisterDailyTools registers daily utility tools (weather, currency, RSS,
// translate, audio normalize, notes, knowledge ingest) into r.
// Note: image generation tools are registered separately by RegisterImageGenTools.
func RegisterDailyTools(r *Registry, cfg *config.Config, enabled func(string) bool, deps DailyDeps) {
	// --- Weather Tools ---
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
			Handler: deps.WeatherCurrent,
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
			Handler: deps.WeatherForecast,
			Builtin: true,
		})
	}

	// --- Currency Tools ---
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
			Handler: deps.CurrencyConvert,
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
			Handler: deps.CurrencyRates,
			Builtin: true,
		})
	}

	// --- RSS Tools ---
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
			Handler: deps.RSSRead,
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
			Handler: deps.RSSList,
			Builtin: true,
		})
	}

	// --- Translate Tools ---
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
			Handler: deps.Translate,
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
			Handler: deps.DetectLanguage,
			Builtin: true,
		})
	}

	// Image generation tools are registered by RegisterImageGenTools in registerBuiltins.

	// --- Notes/Obsidian Integration ---
	if enabled("note_create") && false {
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
			Handler: deps.NoteCreate,
			Builtin: true,
		})
	}

	if enabled("note_read") && false {
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
			Handler: deps.NoteRead,
			Builtin: true,
		})
	}

	if enabled("note_append") && false {
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
			Handler: deps.NoteAppend,
			Builtin: true,
		})
	}

	if enabled("note_list") && false {
		r.Register(&ToolDef{
			Name:        "note_list",
			Description: "List notes in the vault. Optionally filter by path prefix.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"prefix": {"type": "string", "description": "Path prefix to filter (e.g. 'daily/', 'project/')"}
				}
			}`),
			Handler: deps.NoteList,
			Builtin: true,
		})
	}

	if enabled("note_search") && false {
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
			Handler: deps.NoteSearch,
			Builtin: true,
		})
	}

	// --- Learning Loop: store_lesson ---
	if enabled("store_lesson") && false {
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
			Handler: deps.StoreLesson,
			Builtin: true,
		})
	}

	// --- Note Dedup & Source Audit ---
	if enabled("note_dedup") && false {
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
			Handler:     deps.NoteDedup,
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
			Handler: deps.SourceAudit,
			Builtin: true,
		})
	}

	// --- Sitemap Ingest Pipeline ---
	if enabled("web_crawl") && false {
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
			Handler:     deps.WebCrawl,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("source_audit_url") && false {
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
			Handler: deps.SourceAuditURL,
			Builtin: true,
		})
	}

	// --- Audio Normalize ---
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
			Handler:     deps.AudioNormalize,
			Builtin:     true,
			RequireAuth: true,
		})
	}
}
