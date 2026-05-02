package notes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// LogFn is a structured logging function matching logInfo/logWarn/logDebug signatures.
type LogFn func(msg string, keyvals ...any)

// EmbedFn stores a note's content into semantic memory.
// If nil, auto-embed is disabled.
type EmbedFn func(ctx context.Context, name, content string, tags []string) error

// Config holds notes/Obsidian integration settings.
type Config struct {
	Enabled      bool   `json:"enabled"`
	VaultPath    string `json:"vaultPath,omitempty"`
	DefaultExt   string `json:"defaultExt,omitempty"`
	AutoEmbed    bool   `json:"autoEmbed,omitempty"`
	IndexOnStart bool   `json:"indexOnStart,omitempty"`
	Dedup        bool   `json:"dedup,omitempty"`
}

// DefaultExtOrMd returns the configured default extension or ".md".
func (c Config) DefaultExtOrMd() string {
	if c.DefaultExt != "" {
		return c.DefaultExt
	}
	return ".md"
}

// VaultPathResolved resolves the vault path, expanding ~ and relative paths against baseDir.
func (c Config) VaultPathResolved(baseDir string) string {
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

// SearchResult from a TF-IDF search.
type SearchResult struct {
	Filename  string  `json:"filename"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
	LineStart int     `json:"lineStart"`
}

// docEntry stores per-document TF-IDF data.
type docEntry struct {
	filename string
	lines    []string
	tf       map[string]float64
	size     int64
}

// index is a TF-IDF index for notes that supports nested directories via filepath.Walk.
type index struct {
	mu        sync.RWMutex
	docs      map[string]*docEntry // keyed by relative path from vaultPath
	idf       map[string]float64
	totalDocs int
	vaultPath string
}

// Service manages notes within a vault directory.
type Service struct {
	mu         sync.RWMutex
	vaultPath  string
	defaultExt string
	autoEmbed  bool
	embedFn    EmbedFn
	logInfo    LogFn
	logWarn    LogFn
	logDebug   LogFn
	idx        *index
}

// New creates a new notes Service.
// embeddingEnabled controls whether autoEmbed from config is actually activated.
func New(cfg Config, baseDir string, embeddingEnabled bool, embedFn EmbedFn, logInfo, logWarn, logDebug LogFn) *Service {
	noop := func(string, ...any) {}
	if logInfo == nil {
		logInfo = noop
	}
	if logWarn == nil {
		logWarn = noop
	}
	if logDebug == nil {
		logDebug = noop
	}

	vaultPath := cfg.VaultPathResolved(baseDir)
	os.MkdirAll(vaultPath, 0o755)

	svc := &Service{
		vaultPath:  vaultPath,
		defaultExt: cfg.DefaultExtOrMd(),
		autoEmbed:  cfg.AutoEmbed && embeddingEnabled && embedFn != nil,
		embedFn:    embedFn,
		logInfo:    logInfo,
		logWarn:    logWarn,
		logDebug:   logDebug,
		idx: &index{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: vaultPath,
		},
	}

	if cfg.IndexOnStart {
		if err := svc.idx.rebuild(); err != nil {
			logWarn("notes index build failed", "error", err)
		} else {
			logInfo("notes index built", "docs", svc.idx.totalDocs, "vault", vaultPath)
		}
	}

	return svc
}

// VaultPath returns the resolved vault path.
func (svc *Service) VaultPath() string { return svc.vaultPath }

// FullPath returns the absolute path for a note name within the vault.
func (svc *Service) FullPath(name string) string {
	return filepath.Join(svc.vaultPath, svc.ensureExt(name))
}

// ensureExt appends the default extension if the name has none.
func (svc *Service) ensureExt(name string) string {
	if filepath.Ext(name) == "" {
		return name + svc.defaultExt
	}
	return name
}

// CreateNote creates a new note file in the vault, creating subdirectories as needed.
func (svc *Service) CreateNote(name, content string) error {
	if err := ValidateNoteName(name); err != nil {
		return err
	}
	p := svc.FullPath(name)

	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write note: %w", err)
	}

	svc.logInfo("note created", "name", name, "path", p)

	go svc.rebuildIndex()

	if svc.autoEmbed {
		go svc.embedNote(name, content)
	}

	return nil
}

// ReadNote reads the content of a note file.
func (svc *Service) ReadNote(name string) (string, error) {
	if err := ValidateNoteName(name); err != nil {
		return "", err
	}
	p := svc.FullPath(name)

	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("note not found: %s", name)
		}
		return "", fmt.Errorf("read note: %w", err)
	}
	return string(data), nil
}

// AppendNote appends content to an existing note, or creates it if it does not exist.
func (svc *Service) AppendNote(name, content string) error {
	if err := ValidateNoteName(name); err != nil {
		return err
	}
	p := svc.FullPath(name)

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

	svc.logInfo("note appended", "name", name)

	go svc.rebuildIndex()

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
// Tags and wikilinks are extracted from each note's content.
func (svc *Service) ListNotes(prefix string) ([]NoteInfo, error) {
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

		if prefix != "" && !strings.HasPrefix(relPath, prefix) {
			return nil
		}

		data, readErr := os.ReadFile(path)
		var tags, links []string
		if readErr == nil {
			content := string(data)
			tags = ExtractTags(content)
			links = ExtractWikilinks(content)
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
// maxResults <= 0 defaults to 5.
func (svc *Service) SearchNotes(query string, maxResults int) []SearchResult {
	if maxResults <= 0 {
		maxResults = 5
	}
	return svc.idx.search(query, maxResults)
}

// rebuildIndex rebuilds the TF-IDF index, logging any error.
func (svc *Service) rebuildIndex() {
	if err := svc.idx.rebuild(); err != nil {
		svc.logWarn("notes index rebuild failed", "error", err)
	}
}

// embedNote stores a note's content into semantic memory via the injected EmbedFn.
func (svc *Service) embedNote(name, content string) {
	if svc.embedFn == nil {
		return
	}
	tags := ExtractTags(content)
	if err := svc.embedFn(context.Background(), name, content, tags); err != nil {
		svc.logWarn("notes auto-embed failed", "name", name, "error", err)
		return
	}
	svc.logDebug("note embedded", "name", name)
}

// --- index methods ---

// rebuild scans the vault directory recursively and rebuilds the TF-IDF index.
func (idx *index) rebuild() error {
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

// search returns notes ranked by TF-IDF score for the given query.
func (idx *index) search(query string, maxResults int) []SearchResult {
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

	// Sort by score descending (insertion sort — avoids importing sort).
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

// --- Exported helpers ---

var wikilinkRe = regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]+)?\]\]`)
var tagRe = regexp.MustCompile(`(?:^|\s)#([a-zA-Z][a-zA-Z0-9_/-]*)`)

// ExtractWikilinks parses [[wikilink]] and [[wikilink|alias]] references from content.
func ExtractWikilinks(content string) []string {
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

// ExtractTags parses #tag references from content.
func ExtractTags(content string) []string {
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

// ValidateNoteName checks that a note name is safe (no path traversal, no hidden files).
func ValidateNoteName(name string) error {
	if name == "" {
		return fmt.Errorf("note name is required")
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("note name must not be an absolute path")
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("note name must not contain path traversal (..) components")
	}
	cleaned := filepath.Clean(name)
	if strings.HasPrefix(filepath.Base(cleaned), ".") {
		return fmt.Errorf("note name must not start with a dot")
	}
	return nil
}

// --- TF-IDF utilities ---

// logIDF computes log(1 + totalDocs / (1 + df)) using the pure-Go ln function.
func logIDF(totalDocs, docFreq float64) float64 {
	x := 1.0 + totalDocs/(1.0+docFreq)
	return Ln(x)
}

// Ln computes the natural logarithm of x using the identity ln(x) = 2*atanh((x-1)/(x+1))
// with sufficient precision for TF-IDF scoring. Exported for test access.
func Ln(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Reduce x to [1, 2) range: ln(x * 2^exp) = ln(x) + exp * ln(2).
	exp := 0
	for x >= 2.0 {
		x /= 2.0
		exp++
	}
	for x < 1.0 {
		x *= 2.0
		exp--
	}
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

// tokenize splits text into lowercase tokens, filtering tokens shorter than 2 runes
// and common stop words.
func tokenize(text string) []string {
	var tokens []string
	current := strings.Builder{}
	for _, r := range strings.ToLower(text) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			current.WriteRune(r)
		} else {
			if current.Len() >= 2 {
				w := current.String()
				if !isStopWord(w) {
					tokens = append(tokens, w)
				}
			}
			current.Reset()
		}
	}
	if current.Len() >= 2 {
		w := current.String()
		if !isStopWord(w) {
			tokens = append(tokens, w)
		}
	}
	return tokens
}

func isStopWord(w string) bool {
	switch w {
	case "the", "be", "to", "of", "and", "in", "that", "have", "it",
		"for", "not", "on", "with", "he", "as", "you", "do", "at",
		"this", "but", "his", "by", "from", "they", "we", "say", "her",
		"she", "or", "an", "will", "my", "one", "all", "would", "there",
		"their", "what", "so", "up", "out", "if", "about", "who", "get",
		"which", "go", "me", "when", "make", "can", "like", "time", "no",
		"just", "him", "know", "take", "people", "into", "year", "your",
		"good", "some", "could", "them", "see", "other", "than", "then",
		"now", "look", "only", "come", "its", "over", "think", "also",
		"back", "after", "use", "two", "how", "our", "work", "first",
		"well", "way", "even", "new", "want", "because", "any", "these",
		"give", "day", "most", "us", "is", "are", "was", "were", "been",
		"being", "has", "had", "did", "does", "am":
		return true
	}
	return false
}

// findBestMatchLine returns the 0-based index of the line with the most query token hits.
func findBestMatchLine(lines []string, queryTokens []string) int {
	bestLine := 0
	bestCount := 0
	for i, line := range lines {
		lower := strings.ToLower(line)
		count := 0
		for _, qt := range queryTokens {
			if strings.Contains(lower, qt) {
				count++
			}
		}
		if count > bestCount {
			bestCount = count
			bestLine = i
		}
	}
	return bestLine
}

// buildSnippet extracts a snippet from lines around matchLine with contextLines on each side.
func buildSnippet(lines []string, matchLine, contextLines int) string {
	start := matchLine - contextLines
	if start < 0 {
		start = 0
	}
	end := matchLine + contextLines + 1
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start:end], "\n")
}
