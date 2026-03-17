package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tetora/internal/log"
	"tetora/internal/tool"
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
			Handler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
				return tool.WeatherCurrent(ctx, cfg.Weather.Location, input)
			},
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
			Handler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
				return tool.WeatherForecast(ctx, cfg.Weather.Location, input)
			},
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
			Handler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
				return tool.CurrencyConvert(ctx, input)
			},
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
			Handler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
				return tool.CurrencyRates(ctx, input)
			},
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
			Handler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
				return tool.RSSRead(ctx, cfg.RSS.Feeds, input)
			},
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
			Handler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
				return tool.RSSList(ctx, cfg.RSS.Feeds, input)
			},
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
			Handler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
				return tool.Translate(ctx, cfg.Translate.Provider, cfg.Translate.APIKey, input)
			},
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
			Handler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
				return tool.DetectLanguage(ctx, cfg.Translate.Provider, cfg.Translate.APIKey, input)
			},
			Builtin: true,
		})
	}

	// Image generation tools are registered by tools.RegisterImageGenTools in registerBuiltins.

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
			Handler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
				return tool.AudioNormalize(ctx, input)
			},
			Builtin:     true,
			RequireAuth: true,
		})
	}
}

// --- Lesson Tool Handler ---

func toolStoreLesson(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Category string   `json:"category"`
		Lesson   string   `json:"lesson"`
		Source   string   `json:"source"`
		Tags     []string `json:"tags"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Category == "" {
		return "", fmt.Errorf("category is required")
	}
	if args.Lesson == "" {
		return "", fmt.Errorf("lesson is required")
	}

	category := sanitizeLessonCategory(args.Category)
	noteName := "lessons/" + category

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	now := time.Now().Format("2006-01-02 15:04")
	var entry strings.Builder
	entry.WriteString(fmt.Sprintf("\n## %s\n", now))
	entry.WriteString(fmt.Sprintf("- %s\n", args.Lesson))
	if args.Source != "" {
		entry.WriteString(fmt.Sprintf("- Source: %s\n", args.Source))
	}
	if len(args.Tags) > 0 {
		entry.WriteString(fmt.Sprintf("- Tags: %s\n", strings.Join(args.Tags, ", ")))
	}

	if err := svc.AppendNote(noteName, entry.String()); err != nil {
		return "", fmt.Errorf("append to vault: %w", err)
	}

	lessonsFile := "tasks/lessons.md"
	if _, err := os.Stat(lessonsFile); err == nil {
		sectionHeader := "## " + args.Category
		line := fmt.Sprintf("- %s", args.Lesson)
		if err := appendToLessonSection(lessonsFile, sectionHeader, line); err != nil {
			log.Warn("append to lessons.md failed", "error", err)
		}
	}

	if cfg.HistoryDB != "" {
		recordSkillEvent(cfg.HistoryDB, category, "lesson", args.Lesson, args.Source)
	}

	log.InfoCtx(ctx, "lesson stored", "category", category, "tags", args.Tags)

	result := map[string]any{
		"status":   "stored",
		"category": category,
		"vault":    noteName,
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

func sanitizeLessonCategory(cat string) string {
	cat = strings.ToLower(strings.TrimSpace(cat))
	re := regexp.MustCompile(`[^a-z0-9-]+`)
	cat = re.ReplaceAllString(cat, "-")
	cat = strings.Trim(cat, "-")
	if cat == "" {
		cat = "general"
	}
	return cat
}

func appendToLessonSection(filePath, sectionHeader, content string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	var result []string
	inserted := false

	for i, line := range lines {
		result = append(result, line)
		if strings.TrimSpace(line) == sectionHeader {
			j := i + 1
			for j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
				j++
			}
			insertIdx := j
			for insertIdx > i+1 && strings.TrimSpace(lines[insertIdx-1]) == "" {
				insertIdx--
			}
			for k := i + 1; k < insertIdx; k++ {
				result = append(result, lines[k])
			}
			result = append(result, content)
			for k := insertIdx; k < len(lines); k++ {
				result = append(result, lines[k])
			}
			inserted = true
			break
		}
	}

	if !inserted {
		result = append(result, "", sectionHeader, content)
	}

	return os.WriteFile(filePath, []byte(strings.Join(result, "\n")), 0o644)
}

// --- Note Dedup Tool Handler ---

func toolNoteDedup(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		AutoDelete bool   `json:"auto_delete"`
		Prefix     string `json:"prefix"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	vaultPath := svc.VaultPath()

	type fileHash struct {
		Path string
		Hash string
		Size int64
	}
	var files []fileHash
	hashMap := make(map[string][]string)

	filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		if args.Prefix != "" {
			rel, _ := filepath.Rel(vaultPath, path)
			if !strings.HasPrefix(rel, args.Prefix) {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		h := sha256.Sum256(data)
		hash := hex.EncodeToString(h[:16])
		rel, _ := filepath.Rel(vaultPath, path)
		files = append(files, fileHash{Path: rel, Hash: hash, Size: info.Size()})
		hashMap[hash] = append(hashMap[hash], rel)
		return nil
	})

	var duplicates []map[string]any
	deleted := 0
	for hash, paths := range hashMap {
		if len(paths) <= 1 {
			continue
		}
		if args.AutoDelete {
			for _, p := range paths[1:] {
				fullPath := filepath.Join(vaultPath, p)
				if err := os.Remove(fullPath); err == nil {
					deleted++
				}
			}
		}
		duplicates = append(duplicates, map[string]any{
			"hash":  hash,
			"files": paths,
			"count": len(paths),
		})
	}

	result := map[string]any{
		"total_files":      len(files),
		"duplicate_groups": len(duplicates),
		"duplicates":       duplicates,
	}
	if args.AutoDelete {
		result["deleted"] = deleted
	}

	b, _ := json.Marshal(result)
	log.InfoCtx(ctx, "note dedup scan complete", "total_files", len(files), "duplicate_groups", len(duplicates))
	return string(b), nil
}

// --- Source Audit Tool Handler ---

func toolSourceAudit(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Expected []string `json:"expected"`
		Prefix   string   `json:"prefix"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	vaultPath := svc.VaultPath()
	prefix := args.Prefix
	if prefix == "" {
		prefix = "."
	}

	actualSet := make(map[string]bool)
	scanDir := filepath.Join(vaultPath, prefix)
	filepath.Walk(scanDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(vaultPath, path)
		actualSet[rel] = true
		return nil
	})

	expectedSet := make(map[string]bool)
	for _, e := range args.Expected {
		expectedSet[e] = true
	}

	var missing, extra []string
	for e := range expectedSet {
		if !actualSet[e] {
			missing = append(missing, e)
		}
	}
	for a := range actualSet {
		if !expectedSet[a] {
			extra = append(extra, a)
		}
	}

	result := map[string]any{
		"expected_count": len(args.Expected),
		"actual_count":   len(actualSet),
		"missing_count":  len(missing),
		"extra_count":    len(extra),
		"missing":        missing,
		"extra":          extra,
	}
	b, _ := json.Marshal(result)
	log.InfoCtx(ctx, "source audit complete", "expected", len(args.Expected), "actual", len(actualSet))
	return string(b), nil
}

