package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// --- P21.5: Sitemap Ingest Pipeline ---

// toolWebCrawl fetches a sitemap and imports pages into the notes vault.
func toolWebCrawl(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		URL         string   `json:"url"`
		Mode        string   `json:"mode"`        // "sitemap" (default), "single"
		Include     []string `json:"include"`      // glob patterns to include
		Exclude     []string `json:"exclude"`      // glob patterns to exclude
		Target      string   `json:"target"`       // "notes" (default)
		Prefix      string   `json:"prefix"`       // note path prefix
		Dedup       bool     `json:"dedup"`         // skip if same content hash exists
		MaxPages    int      `json:"max_pages"`
		Concurrency int      `json:"concurrency"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if args.Mode == "" {
		args.Mode = "sitemap"
	}
	if args.MaxPages <= 0 {
		args.MaxPages = 500
	}
	if args.Concurrency <= 0 {
		args.Concurrency = 3
	}
	if args.Target == "" {
		args.Target = "notes"
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service not enabled")
	}

	var urls []string
	switch args.Mode {
	case "sitemap":
		var err error
		urls, err = fetchSitemapURLs(ctx, args.URL)
		if err != nil {
			return "", fmt.Errorf("fetch sitemap: %w", err)
		}
	case "single":
		urls = []string{args.URL}
	default:
		return "", fmt.Errorf("unknown mode: %s", args.Mode)
	}

	// Apply filters.
	urls = filterURLs(urls, args.Include, args.Exclude)

	// Cap at max pages.
	if len(urls) > args.MaxPages {
		urls = urls[:args.MaxPages]
	}

	logInfoCtx(ctx, "web_crawl starting", "urls", len(urls), "prefix", args.Prefix)

	// Fetch pages concurrently.
	type pageResult struct {
		URL    string
		Status string // "imported", "skipped", "failed"
		Error  string
	}

	results := make([]pageResult, len(urls))
	sem := make(chan struct{}, args.Concurrency)
	var wg sync.WaitGroup

	for i, u := range urls {
		wg.Add(1)
		go func(idx int, pageURL string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			status, err := ingestPage(ctx, svc, pageURL, args.Prefix, args.Dedup)
			results[idx] = pageResult{URL: pageURL, Status: status}
			if err != nil {
				results[idx].Error = err.Error()
			}
		}(i, u)
	}
	wg.Wait()

	// Summarize.
	imported, skipped, failed := 0, 0, 0
	var errors []string
	for _, r := range results {
		switch r.Status {
		case "imported":
			imported++
		case "skipped":
			skipped++
		default:
			failed++
			if r.Error != "" {
				errors = append(errors, fmt.Sprintf("%s: %s", r.URL, r.Error))
			}
		}
	}

	summary := map[string]any{
		"total":    len(urls),
		"imported": imported,
		"skipped":  skipped,
		"failed":   failed,
	}
	if len(errors) > 0 {
		// Cap errors to avoid huge output.
		if len(errors) > 10 {
			errors = errors[:10]
		}
		summary["errors"] = errors
	}

	b, _ := json.Marshal(summary)
	return string(b), nil
}

// fetchSitemapURLs fetches and parses a sitemap (or sitemap index).
func fetchSitemapURLs(ctx context.Context, sitemapURL string) ([]string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", sitemapURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Tetora/2.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		return nil, err
	}

	content := string(body)

	// Check if this is a sitemap index.
	if strings.Contains(content, "<sitemapindex") {
		return parseSitemapIndex(ctx, content, client)
	}

	return parseSitemapURLs(content), nil
}

// parseSitemapIndex parses a <sitemapindex> and fetches child sitemaps.
func parseSitemapIndex(ctx context.Context, content string, client *http.Client) ([]string, error) {
	// Extract <loc> from <sitemap> entries.
	re := regexp.MustCompile(`<sitemap>[^<]*<loc>([^<]+)</loc>`)
	matches := re.FindAllStringSubmatch(content, -1)

	var allURLs []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		childURL := strings.TrimSpace(m[1])
		req, err := http.NewRequestWithContext(ctx, "GET", childURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Tetora/2.0")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		resp.Body.Close()
		if err != nil {
			continue
		}
		urls := parseSitemapURLs(string(body))
		allURLs = append(allURLs, urls...)
	}
	return allURLs, nil
}

// parseSitemapURLs extracts <loc> URLs from a <urlset> sitemap.
func parseSitemapURLs(content string) []string {
	re := regexp.MustCompile(`<url>[^<]*<loc>([^<]+)</loc>`)
	matches := re.FindAllStringSubmatch(content, -1)
	var urls []string
	for _, m := range matches {
		if len(m) >= 2 {
			urls = append(urls, strings.TrimSpace(m[1]))
		}
	}
	return urls
}

// filterURLs applies include/exclude glob patterns to a URL list.
func filterURLs(urls, include, exclude []string) []string {
	if len(include) == 0 && len(exclude) == 0 {
		return urls
	}
	var result []string
	for _, u := range urls {
		// Check exclude first.
		excluded := false
		for _, pat := range exclude {
			if matched, _ := filepath.Match(pat, u); matched {
				excluded = true
				break
			}
			// Also try matching just the path portion.
			if idx := strings.Index(u, "://"); idx >= 0 {
				pathPart := u[idx+3:]
				if matched, _ := filepath.Match(pat, pathPart); matched {
					excluded = true
					break
				}
			}
		}
		if excluded {
			continue
		}

		// Check include (if any patterns specified, URL must match at least one).
		if len(include) > 0 {
			included := false
			for _, pat := range include {
				if matched, _ := filepath.Match(pat, u); matched {
					included = true
					break
				}
				if idx := strings.Index(u, "://"); idx >= 0 {
					pathPart := u[idx+3:]
					if matched, _ := filepath.Match(pat, pathPart); matched {
						included = true
						break
					}
				}
			}
			if !included {
				continue
			}
		}
		result = append(result, u)
	}
	return result
}

// ingestPage fetches a URL, strips HTML, and writes to notes vault.
func ingestPage(ctx context.Context, svc *NotesService, pageURL, prefix string, dedup bool) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return "failed", err
	}
	req.Header.Set("User-Agent", "Tetora/2.0")

	resp, err := client.Do(req)
	if err != nil {
		return "failed", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "failed", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024)) // 5MB limit
	if err != nil {
		return "failed", err
	}

	text := stripHTMLTags(string(body))
	if strings.TrimSpace(text) == "" {
		return "skipped", nil
	}

	// Generate note name from URL.
	slug := urlToSlug(pageURL)
	noteName := slug
	if prefix != "" {
		noteName = prefix + "/" + slug
	}

	// Dedup check.
	if dedup {
		h := sha256.Sum256([]byte(text))
		hash := hex.EncodeToString(h[:16])
		// Check if note already exists with same hash.
		existing, err := svc.ReadNote(noteName)
		if err == nil && existing != "" {
			// Strip frontmatter before hashing to compare body only.
			body := stripFrontmatter(existing)
			existingH := sha256.Sum256([]byte(body))
			existingHash := hex.EncodeToString(existingH[:16])
			if existingHash == hash {
				return "skipped", nil
			}
		}
	}

	// Write as markdown with URL source header.
	content := fmt.Sprintf("---\nsource: %s\nimported: %s\n---\n\n%s", pageURL, time.Now().Format("2006-01-02"), text)
	if err := svc.CreateNote(noteName, content); err != nil {
		return "failed", err
	}

	return "imported", nil
}

// urlToSlug converts a URL to a filesystem-safe slug.
// The slug intentionally avoids dots so ensureExt appends .md reliably.
func urlToSlug(u string) string {
	// Remove scheme.
	slug := u
	if idx := strings.Index(slug, "://"); idx >= 0 {
		slug = slug[idx+3:]
	}
	// Remove query/fragment.
	if idx := strings.IndexAny(slug, "?#"); idx >= 0 {
		slug = slug[:idx]
	}
	// Replace path separators and special chars.
	slug = strings.TrimRight(slug, "/")
	slug = strings.ReplaceAll(slug, "/", "_")
	// Remove non-alphanumeric chars except - _
	// (dots excluded so filepath.Ext returns "" and ensureExt adds .md)
	re := regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
	slug = re.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "page"
	}
	// Cap length.
	if len(slug) > 100 {
		slug = slug[:100]
	}
	return slug
}

// toolSourceAuditURL compares a sitemap's URLs against imported notes.
func toolSourceAuditURL(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		SitemapURL string `json:"sitemap_url"`
		Prefix     string `json:"prefix"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.SitemapURL == "" {
		return "", fmt.Errorf("sitemap_url is required")
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service not enabled")
	}

	// Fetch sitemap URLs.
	urls, err := fetchSitemapURLs(ctx, args.SitemapURL)
	if err != nil {
		return "", fmt.Errorf("fetch sitemap: %w", err)
	}

	// Build expected note names.
	expectedNotes := make(map[string]string) // noteName -> URL
	for _, u := range urls {
		slug := urlToSlug(u)
		noteName := slug
		if args.Prefix != "" {
			noteName = args.Prefix + "/" + slug
		}
		expectedNotes[noteName] = u
	}

	// Check which exist.
	var missing []map[string]string
	existing := 0
	for name, url := range expectedNotes {
		content, err := svc.ReadNote(name)
		if err != nil || content == "" {
			missing = append(missing, map[string]string{"note": name, "url": url})
		} else {
			existing++
		}
	}

	result := map[string]any{
		"total":         len(urls),
		"existing":      existing,
		"missing_count": len(missing),
	}
	// Cap missing list.
	if len(missing) > 50 {
		result["missing"] = missing[:50]
		result["missing_truncated"] = true
	} else {
		result["missing"] = missing
	}

	b, _ := json.Marshal(result)
	return string(b), nil
}

// stripFrontmatter removes YAML frontmatter (--- delimited) from content.
func stripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---\n") {
		return content
	}
	// Find closing ---.
	end := strings.Index(content[4:], "\n---\n")
	if end < 0 {
		return content
	}
	// Skip frontmatter + trailing newlines.
	body := content[4+end+5:]
	return strings.TrimLeft(body, "\n")
}
