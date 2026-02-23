package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSitemapURLs(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>https://example.com/page1</loc>
  </url>
  <url>
    <loc>https://example.com/page2</loc>
  </url>
  <url>
    <loc>https://example.com/about</loc>
  </url>
</urlset>`

	urls := parseSitemapURLs(xml)
	if len(urls) != 3 {
		t.Fatalf("expected 3 urls, got %d", len(urls))
	}
	expected := []string{
		"https://example.com/page1",
		"https://example.com/page2",
		"https://example.com/about",
	}
	for i, u := range urls {
		if u != expected[i] {
			t.Errorf("url[%d]: want %q, got %q", i, expected[i], u)
		}
	}
}

func TestParseSitemapURLsEmpty(t *testing.T) {
	urls := parseSitemapURLs("")
	if len(urls) != 0 {
		t.Errorf("expected 0 urls from empty content, got %d", len(urls))
	}
}

func TestParseSitemapIndex(t *testing.T) {
	// Create a child sitemap server.
	childXML := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/child1</loc></url>
  <url><loc>https://example.com/child2</loc></url>
</urlset>`

	childSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, childXML)
	}))
	defer childSrv.Close()

	indexXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap>
    <loc>%s/sitemap1.xml</loc>
  </sitemap>
</sitemapindex>`, childSrv.URL)

	// Create index server.
	indexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, indexXML)
	}))
	defer indexSrv.Close()

	ctx := context.Background()
	urls, err := fetchSitemapURLs(ctx, indexSrv.URL)
	if err != nil {
		t.Fatalf("fetchSitemapURLs: %v", err)
	}
	if len(urls) != 2 {
		t.Fatalf("expected 2 urls from sitemapindex, got %d", len(urls))
	}
	if urls[0] != "https://example.com/child1" {
		t.Errorf("url[0]: want https://example.com/child1, got %s", urls[0])
	}
}

func TestFilterURLs(t *testing.T) {
	urls := []string{
		"https://example.com/docs/api",
		"https://example.com/docs/guide",
		"https://example.com/blog/post1",
		"https://example.com/about",
	}

	t.Run("no_filters", func(t *testing.T) {
		result := filterURLs(urls, nil, nil)
		if len(result) != 4 {
			t.Errorf("expected 4, got %d", len(result))
		}
	})

	t.Run("include_only", func(t *testing.T) {
		result := filterURLs(urls, []string{"example.com/docs/*"}, nil)
		if len(result) != 2 {
			t.Errorf("expected 2 docs urls, got %d: %v", len(result), result)
		}
	})

	t.Run("exclude_only", func(t *testing.T) {
		result := filterURLs(urls, nil, []string{"example.com/blog/*"})
		if len(result) != 3 {
			t.Errorf("expected 3 non-blog urls, got %d: %v", len(result), result)
		}
	})

	t.Run("include_and_exclude", func(t *testing.T) {
		result := filterURLs(urls, []string{"example.com/docs/*"}, []string{"example.com/docs/api"})
		if len(result) != 1 {
			t.Errorf("expected 1 (docs minus api), got %d: %v", len(result), result)
		}
		if len(result) > 0 && !strings.Contains(result[0], "guide") {
			t.Errorf("expected guide url, got %s", result[0])
		}
	})
}

func TestURLToSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://example.com/page", "example-com_page"},
		{"https://example.com/docs/api/v2", "example-com_docs_api_v2"},
		{"https://example.com/", "example-com"},
		{"https://example.com/page?q=1#section", "example-com_page"},
		{"http://test.org/a/b/c/", "test-org_a_b_c"},
		{"", "page"},
	}

	for _, tt := range tests {
		got := urlToSlug(tt.input)
		if got != tt.want {
			t.Errorf("urlToSlug(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestURLToSlugLongURL(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("a", 200)
	slug := urlToSlug(long)
	if len(slug) > 100 {
		t.Errorf("slug too long: %d chars", len(slug))
	}
}

func TestWebCrawlSingle(t *testing.T) {
	// Set up a local HTTP server serving an HTML page.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h1>Hello World</h1><p>Test content here.</p></body></html>")
	}))
	defer srv.Close()

	tmp := t.TempDir()
	svc := &NotesService{
		cfg:        &Config{},
		vaultPath:  tmp,
		defaultExt: ".md",
		idx: &notesIndex{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: tmp,
		},
	}
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"url":  srv.URL + "/test-page",
		"mode": "single",
	})
	out, err := toolWebCrawl(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolWebCrawl: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if int(result["total"].(float64)) != 1 {
		t.Errorf("total: want 1, got %v", result["total"])
	}
	if int(result["imported"].(float64)) != 1 {
		t.Errorf("imported: want 1, got %v", result["imported"])
	}

	// Verify note was created.
	slug := urlToSlug(srv.URL + "/test-page")
	notePath := filepath.Join(tmp, slug+".md")
	data, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("note file not found: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "source:") {
		t.Errorf("note should contain source header")
	}
	if !strings.Contains(content, "Hello World") {
		t.Errorf("note should contain stripped text 'Hello World'")
	}
}

func TestWebCrawlSitemap(t *testing.T) {
	// Two page servers.
	pageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<html><body><h1>Page: %s</h1></body></html>", r.URL.Path)
	}))
	defer pageSrv.Close()

	sitemapXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>%s/page1</loc></url>
  <url><loc>%s/page2</loc></url>
</urlset>`, pageSrv.URL, pageSrv.URL)

	sitemapSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, sitemapXML)
	}))
	defer sitemapSrv.Close()

	tmp := t.TempDir()
	svc := &NotesService{
		cfg:        &Config{},
		vaultPath:  tmp,
		defaultExt: ".md",
		idx: &notesIndex{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: tmp,
		},
	}
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"url":    sitemapSrv.URL + "/sitemap.xml",
		"prefix": "docs",
	})
	out, err := toolWebCrawl(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolWebCrawl sitemap: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)

	if int(result["total"].(float64)) != 2 {
		t.Errorf("total: want 2, got %v", result["total"])
	}
	if int(result["imported"].(float64)) != 2 {
		t.Errorf("imported: want 2, got %v", result["imported"])
	}

	// Verify notes were created under prefix.
	docsDir := filepath.Join(tmp, "docs")
	entries, err := os.ReadDir(docsDir)
	if err != nil {
		t.Fatalf("docs dir not found: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 notes in docs/, got %d", len(entries))
	}
}

func TestWebCrawlDedup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>Same content every time</body></html>")
	}))
	defer srv.Close()

	tmp := t.TempDir()
	svc := &NotesService{
		cfg:        &Config{},
		vaultPath:  tmp,
		defaultExt: ".md",
		idx: &notesIndex{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: tmp,
		},
	}
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	ctx := context.Background()
	cfg := &Config{}

	// First import.
	input, _ := json.Marshal(map[string]any{
		"url":   srv.URL + "/page",
		"mode":  "single",
		"dedup": true,
	})
	out, err := toolWebCrawl(ctx, cfg, input)
	if err != nil {
		t.Fatalf("first crawl: %v", err)
	}

	var result1 map[string]any
	json.Unmarshal([]byte(out), &result1)
	if int(result1["imported"].(float64)) != 1 {
		t.Errorf("first crawl: expected 1 imported, got %v", result1["imported"])
	}

	// Second import with dedup - should skip.
	out2, err := toolWebCrawl(ctx, cfg, input)
	if err != nil {
		t.Fatalf("second crawl: %v", err)
	}

	var result2 map[string]any
	json.Unmarshal([]byte(out2), &result2)
	if int(result2["skipped"].(float64)) != 1 {
		t.Errorf("second crawl: expected 1 skipped, got %v (result: %v)", result2["skipped"], result2)
	}
}

func TestWebCrawlEmptyPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body></body></html>")
	}))
	defer srv.Close()

	tmp := t.TempDir()
	svc := &NotesService{
		cfg:        &Config{},
		vaultPath:  tmp,
		defaultExt: ".md",
		idx: &notesIndex{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: tmp,
		},
	}
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"url":  srv.URL + "/empty",
		"mode": "single",
	})
	out, err := toolWebCrawl(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolWebCrawl: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)
	if int(result["skipped"].(float64)) != 1 {
		t.Errorf("expected 1 skipped for empty page, got %v", result["skipped"])
	}
}

func TestWebCrawlMaxPages(t *testing.T) {
	pageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<html><body>Page %s</body></html>", r.URL.Path)
	}))
	defer pageSrv.Close()

	// Sitemap with 5 URLs but max_pages=2.
	var locs strings.Builder
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&locs, "<url><loc>%s/p%d</loc></url>\n", pageSrv.URL, i)
	}
	sitemapXML := fmt.Sprintf(`<?xml version="1.0"?><urlset>%s</urlset>`, locs.String())

	sitemapSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, sitemapXML)
	}))
	defer sitemapSrv.Close()

	tmp := t.TempDir()
	svc := &NotesService{
		cfg:        &Config{},
		vaultPath:  tmp,
		defaultExt: ".md",
		idx: &notesIndex{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: tmp,
		},
	}
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"url":       sitemapSrv.URL,
		"max_pages": 2,
	})
	out, err := toolWebCrawl(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolWebCrawl: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)
	if int(result["total"].(float64)) != 2 {
		t.Errorf("expected total=2 with max_pages, got %v", result["total"])
	}
}

func TestSourceAuditURL(t *testing.T) {
	// Create a sitemap server.
	sitemapXML := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/page1</loc></url>
  <url><loc>https://example.com/page2</loc></url>
  <url><loc>https://example.com/page3</loc></url>
</urlset>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, sitemapXML)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	svc := &NotesService{
		cfg:        &Config{},
		vaultPath:  tmp,
		defaultExt: ".md",
		idx: &notesIndex{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: tmp,
		},
	}
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	// Pre-create notes for page1 and page2 (but not page3).
	slug1 := urlToSlug("https://example.com/page1")
	slug2 := urlToSlug("https://example.com/page2")
	os.WriteFile(filepath.Join(tmp, slug1+".md"), []byte("content1"), 0o644)
	os.WriteFile(filepath.Join(tmp, slug2+".md"), []byte("content2"), 0o644)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"sitemap_url": srv.URL + "/sitemap.xml",
	})
	out, err := toolSourceAuditURL(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolSourceAuditURL: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)

	if int(result["total"].(float64)) != 3 {
		t.Errorf("total: want 3, got %v", result["total"])
	}
	if int(result["existing"].(float64)) != 2 {
		t.Errorf("existing: want 2, got %v", result["existing"])
	}
	if int(result["missing_count"].(float64)) != 1 {
		t.Errorf("missing_count: want 1, got %v", result["missing_count"])
	}
}

func TestWebCrawlNoNotesService(t *testing.T) {
	setGlobalNotesService(nil)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"url":  "https://example.com/sitemap.xml",
		"mode": "single",
	})
	_, err := toolWebCrawl(ctx, cfg, input)
	if err == nil {
		t.Fatal("expected error when notes service is nil")
	}
	if !strings.Contains(err.Error(), "notes service not enabled") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSourceAuditURLNoNotesService(t *testing.T) {
	setGlobalNotesService(nil)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"sitemap_url": "https://example.com/sitemap.xml",
	})
	_, err := toolSourceAuditURL(ctx, cfg, input)
	if err == nil {
		t.Fatal("expected error when notes service is nil")
	}
	if !strings.Contains(err.Error(), "notes service not enabled") {
		t.Errorf("unexpected error: %v", err)
	}
}
