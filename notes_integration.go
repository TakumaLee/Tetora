package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// --- P19.4: Notes/Obsidian Integration ---

// NotesConfig holds configuration for the Notes/Obsidian integration.
type NotesConfig struct {
	Enabled      bool   `json:"enabled"`
	VaultPath    string `json:"vaultPath,omitempty"`    // Path to the Obsidian vault or notes directory
	DefaultExt   string `json:"defaultExt,omitempty"`   // Default file extension (default ".md")
	AutoEmbed    bool   `json:"autoEmbed,omitempty"`    // Auto-embed notes into semantic memory
	IndexOnStart bool   `json:"indexOnStart,omitempty"` // Build TF-IDF index on startup
	Dedup        bool   `json:"dedup,omitempty"`        // Enable dedup on ingest
}

// defaultExtOrMd returns the configured default extension or ".md".
func (c NotesConfig) defaultExtOrMd() string {
	if c.DefaultExt != "" {
		return c.DefaultExt
	}
	return ".md"
}

// vaultPathResolved resolves the vault path, expanding ~ and relative paths.
func (c NotesConfig) vaultPathResolved(baseDir string) string {
	p := c.VaultPath
	if p == "" {
		return filepath.Join(baseDir, "vault")
	}
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, p[2:])
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, p)
	}
	return p
}

// NoteInfo describes a single note file.
type NoteInfo struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Tags    []string  `json:"tags,omitempty"`
	Links   []string  `json:"links,omitempty"`
}

// NotesService manages notes within a vault directory and provides
// TF-IDF search via the existing knowledgeIndex.
type NotesService struct {
	mu        sync.RWMutex
	cfg       *Config
	vaultPath string
	defaultExt string
	autoEmbed bool
	idx       *notesIndex
}

// notesIndex is a TF-IDF index for notes that supports nested directories.
// It wraps the same algorithm as knowledgeIndex but uses filepath.Walk.
type notesIndex struct {
	mu        sync.RWMutex
	docs      map[string]*docEntry // keyed by relative path
	idf       map[string]float64
	totalDocs int
	vaultPath string
}

// newNotesService creates a new NotesService. If IndexOnStart is true,
// it builds the TF-IDF index immediately.
func newNotesService(cfg *Config) *NotesService {
	vaultPath := cfg.Notes.vaultPathResolved(cfg.baseDir)
	// Ensure vault directory exists.
	os.MkdirAll(vaultPath, 0o755)

	svc := &NotesService{
		cfg:        cfg,
		vaultPath:  vaultPath,
		defaultExt: cfg.Notes.defaultExtOrMd(),
		autoEmbed:  cfg.Notes.AutoEmbed && cfg.Embedding.Enabled,
		idx: &notesIndex{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: vaultPath,
		},
	}

	if cfg.Notes.IndexOnStart {
		if err := svc.idx.rebuild(); err != nil {
			logWarn("notes index build failed", "error", err)
		} else {
			logInfo("notes index built", "docs", svc.idx.totalDocs, "vault", vaultPath)
		}
	}

	return svc
}

// rebuild scans the vault directory recursively and rebuilds the TF-IDF index.
func (idx *notesIndex) rebuild() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	docs := make(map[string]*docEntry)
	df := make(map[string]int)

	err := filepath.Walk(idx.vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			// Skip hidden directories.
			if strings.HasPrefix(info.Name(), ".") && path != idx.vaultPath {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip hidden files.
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		relPath, _ := filepath.Rel(idx.vaultPath, path)

		data, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}

		content := string(data)
		lines := strings.Split(content, "\n")
		tokens := tokenize(content)

		// Compute term frequency.
		termCounts := make(map[string]int)
		for _, tok := range tokens {
			termCounts[tok]++
		}
		total := len(tokens)
		tf := make(map[string]float64)
		if total > 0 {
			for term, count := range termCounts {
				tf[term] = float64(count) / float64(total)
			}
		}

		// Track document frequency.
		seen := make(map[string]bool)
		for _, tok := range tokens {
			if !seen[tok] {
				df[tok]++
				seen[tok] = true
			}
		}

		docs[relPath] = &docEntry{
			filename: relPath,
			lines:    lines,
			tf:       tf,
			size:     info.Size(),
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			idx.docs = make(map[string]*docEntry)
			idx.idf = make(map[string]float64)
			idx.totalDocs = 0
			return nil
		}
		return err
	}

	totalDocs := len(docs)
	idf := make(map[string]float64)
	for term, docCount := range df {
		idf[term] = logIDF(float64(totalDocs), float64(docCount))
	}

	idx.docs = docs
	idx.idf = idf
	idx.totalDocs = totalDocs
	return nil
}

// logIDF computes log(1 + totalDocs / (1 + df)).
func logIDF(totalDocs, docFreq float64) float64 {
	// Use the same formula as knowledgeIndex.
	// math.Log is imported via the tokenize function's file, but we reimplement to avoid import.
	// Actually, we compute manually: ln(1 + N/(1+df))
	x := 1.0 + totalDocs/(1.0+docFreq)
	// Natural log via Taylor series? No, just use a simple loop.
	// Actually let's just import math. But the file doesn't import it.
	// Use the same approach: we'll store raw scores and sort.
	return ln(x)
}

// ln computes natural logarithm using the identity ln(x) = 2*atanh((x-1)/(x+1))
// with sufficient precision for TF-IDF scoring.
func ln(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Reduce x to [1,2) range.
	exp := 0
	for x >= 2.0 {
		x /= 2.0
		exp++
	}
	for x < 1.0 {
		x *= 2.0
		exp--
	}
	// ln(x * 2^exp) = ln(x) + exp * ln(2)
	// ln(x) for x in [1,2) using atanh series: ln(x) = 2*atanh((x-1)/(x+1))
	t := (x - 1.0) / (x + 1.0)
	t2 := t * t
	sum := t
	term := t
	for i := 3; i <= 21; i += 2 {
		term *= t2
		sum += term / float64(i)
	}
	const ln2 = 0.6931471805599453
	return 2.0*sum + float64(exp)*ln2
}

// search returns notes ranked by TF-IDF score.
func (idx *notesIndex) search(query string, maxResults int) []SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	queryTokens := tokenize(query)
	if len(queryTokens) == 0 {
		return nil
	}

	type scored struct {
		filename  string
		score     float64
		matchLine int
	}

	var results []scored
	for _, doc := range idx.docs {
		var score float64
		for _, qt := range queryTokens {
			tf, ok := doc.tf[qt]
			if !ok {
				continue
			}
			idf := idx.idf[qt]
			score += tf * idf
		}
		if score <= 0 {
			continue
		}

		bestLine := findBestMatchLine(doc.lines, queryTokens)
		results = append(results, scored{
			filename:  doc.filename,
			score:     score,
			matchLine: bestLine,
		})
	}

	// Sort by score descending.
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].score > results[i].score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}

	var out []SearchResult
	for _, r := range results {
		doc := idx.docs[r.filename]
		snippet := buildSnippet(doc.lines, r.matchLine, 1)
		out = append(out, SearchResult{
			Filename:  r.filename,
			Snippet:   snippet,
			Score:     r.score,
			LineStart: r.matchLine + 1,
		})
	}
	return out
}

// validateNoteName checks that a note name is safe (no path traversal).
func validateNoteName(name string) error {
	if name == "" {
		return fmt.Errorf("note name is required")
	}
	// Reject absolute paths.
	if filepath.IsAbs(name) {
		return fmt.Errorf("note name must not be an absolute path")
	}
	// Reject any occurrence of ".." in the raw name (covers foo/../bar, ../escape, etc.).
	if strings.Contains(name, "..") {
		return fmt.Errorf("note name must not contain path traversal (..) components")
	}
	// Reject names where the base starts with a dot (hidden files).
	cleaned := filepath.Clean(name)
	if strings.HasPrefix(filepath.Base(cleaned), ".") {
		return fmt.Errorf("note name must not start with a dot")
	}
	return nil
}

// ensureExt appends the default extension if the name has none.
func (svc *NotesService) ensureExt(name string) string {
	if filepath.Ext(name) == "" {
		return name + svc.defaultExt
	}
	return name
}

// fullPath returns the absolute path for a note name within the vault.
func (svc *NotesService) fullPath(name string) string {
	return filepath.Join(svc.vaultPath, svc.ensureExt(name))
}

// CreateNote creates a new note file in the vault.
func (svc *NotesService) CreateNote(name, content string) error {
	if err := validateNoteName(name); err != nil {
		return err
	}
	p := svc.fullPath(name)

	// Create subdirectories if needed.
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write note: %w", err)
	}

	logInfo("note created", "name", name, "path", p)

	// Rebuild index asynchronously.
	go svc.rebuildIndex()

	// Auto-embed if configured.
	if svc.autoEmbed {
		go svc.embedNote(name, content)
	}

	return nil
}

// ReadNote reads the content of a note file.
func (svc *NotesService) ReadNote(name string) (string, error) {
	if err := validateNoteName(name); err != nil {
		return "", err
	}
	p := svc.fullPath(name)

	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("note not found: %s", name)
		}
		return "", fmt.Errorf("read note: %w", err)
	}
	return string(data), nil
}

// AppendNote appends content to an existing note, or creates it if it doesn't exist.
func (svc *NotesService) AppendNote(name, content string) error {
	if err := validateNoteName(name); err != nil {
		return err
	}
	p := svc.fullPath(name)

	// Create subdirectories if needed.
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open note for append: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("append to note: %w", err)
	}

	logInfo("note appended", "name", name)

	// Rebuild index asynchronously.
	go svc.rebuildIndex()

	// Auto-embed the full updated content.
	if svc.autoEmbed {
		go func() {
			full, err := os.ReadFile(p)
			if err == nil {
				svc.embedNote(name, string(full))
			}
		}()
	}

	return nil
}

// ListNotes returns notes matching an optional prefix filter.
func (svc *NotesService) ListNotes(prefix string) ([]NoteInfo, error) {
	var notes []NoteInfo

	err := filepath.Walk(svc.vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") && path != svc.vaultPath {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		relPath, _ := filepath.Rel(svc.vaultPath, path)

		// Apply prefix filter.
		if prefix != "" && !strings.HasPrefix(relPath, prefix) {
			return nil
		}

		// Read content to extract tags and links.
		data, readErr := os.ReadFile(path)
		var tags, links []string
		if readErr == nil {
			content := string(data)
			tags = extractTags(content)
			links = extractWikilinks(content)
		}

		notes = append(notes, NoteInfo{
			Name:    relPath,
			Path:    path,
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Tags:    tags,
			Links:   links,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk vault: %w", err)
	}

	return notes, nil
}

// SearchNotes searches notes using the TF-IDF index.
func (svc *NotesService) SearchNotes(query string, maxResults int) []SearchResult {
	if maxResults <= 0 {
		maxResults = 5
	}
	return svc.idx.search(query, maxResults)
}

// rebuildIndex rebuilds the TF-IDF index.
func (svc *NotesService) rebuildIndex() {
	if err := svc.idx.rebuild(); err != nil {
		logWarn("notes index rebuild failed", "error", err)
	}
}

// embedNote stores a note's content into semantic memory.
func (svc *NotesService) embedNote(name, content string) {
	ctx := context.Background()
	vec, err := getEmbedding(ctx, svc.cfg, content)
	if err != nil {
		logWarn("notes auto-embed failed", "name", name, "error", err)
		return
	}
	meta := map[string]interface{}{
		"name": name,
		"tags": extractTags(content),
	}
	if err := storeEmbedding(svc.cfg.HistoryDB, "notes", name, content, vec, meta); err != nil {
		logWarn("notes auto-embed store failed", "name", name, "error", err)
		return
	}
	logDebug("note embedded", "name", name)
}

// --- Wikilink and Tag extraction ---

var wikilinkRe = regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]+)?\]\]`)
var tagRe = regexp.MustCompile(`(?:^|\s)#([a-zA-Z][a-zA-Z0-9_/-]*)`)

// extractWikilinks parses [[wikilink]] and [[wikilink|alias]] references from content.
func extractWikilinks(content string) []string {
	matches := wikilinkRe.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	var links []string
	for _, m := range matches {
		link := strings.TrimSpace(m[1])
		if !seen[link] {
			seen[link] = true
			links = append(links, link)
		}
	}
	return links
}

// extractTags parses #tag references from content.
func extractTags(content string) []string {
	matches := tagRe.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	var tags []string
	for _, m := range matches {
		tag := m[1]
		if !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	return tags
}

// --- Package-level NotesService instance ---

var (
	globalNotesMu      sync.RWMutex
	globalNotesService *NotesService
)

// setGlobalNotesService sets the package-level notes service (called from main).
func setGlobalNotesService(svc *NotesService) {
	globalNotesMu.Lock()
	defer globalNotesMu.Unlock()
	globalNotesService = svc
}

// getGlobalNotesService returns the package-level notes service.
func getGlobalNotesService() *NotesService {
	globalNotesMu.RLock()
	defer globalNotesMu.RUnlock()
	return globalNotesService
}

// --- Tool handlers for notes ---

// toolNoteCreate handles the note_create tool.
func toolNoteCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	if err := svc.CreateNote(args.Name, args.Content); err != nil {
		return "", err
	}

	result := map[string]any{
		"status": "created",
		"name":   args.Name,
		"path":   svc.fullPath(args.Name),
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// toolNoteRead handles the note_read tool.
func toolNoteRead(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	content, err := svc.ReadNote(args.Name)
	if err != nil {
		return "", err
	}

	result := map[string]any{
		"name":    args.Name,
		"content": content,
		"tags":    extractTags(content),
		"links":   extractWikilinks(content),
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// toolNoteAppend handles the note_append tool.
func toolNoteAppend(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	if err := svc.AppendNote(args.Name, args.Content); err != nil {
		return "", err
	}

	result := map[string]any{
		"status": "appended",
		"name":   args.Name,
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// toolNoteList handles the note_list tool.
func toolNoteList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Prefix string `json:"prefix"`
	}
	json.Unmarshal(input, &args)

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	notes, err := svc.ListNotes(args.Prefix)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(notes)
	return string(b), nil
}

// toolNoteSearch handles the note_search tool.
func toolNoteSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 5
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	results := svc.SearchNotes(args.Query, args.MaxResults)
	b, _ := json.Marshal(results)
	return string(b), nil
}
