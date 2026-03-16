package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

// KnowledgeFile represents a file in the knowledge base directory.
type KnowledgeFile struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

func CmdKnowledge(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora knowledge <list|add|remove|search|path> [options]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list              List files in knowledge base")
		fmt.Println("  add <file>        Copy file to knowledge base")
		fmt.Println("  remove <name>     Remove file from knowledge base")
		fmt.Println("  search <query>    Search knowledge base (TF-IDF)")
		fmt.Println("  path              Show knowledge base directory path")
		return
	}
	switch args[0] {
	case "list", "ls":
		knowledgeList()
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora knowledge add <file>")
			os.Exit(1)
		}
		knowledgeAdd(args[1])
	case "remove", "rm":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora knowledge remove <name>")
			os.Exit(1)
		}
		knowledgeRemove(args[1])
	case "search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora knowledge search <query>")
			os.Exit(1)
		}
		knowledgeSearch(strings.Join(args[1:], " "))
	case "path":
		knowledgePath()
	default:
		fmt.Fprintf(os.Stderr, "Unknown knowledge action: %s\n", args[0])
		os.Exit(1)
	}
}

func knowledgeList() {
	cfg := LoadCLIConfig(FindConfigPath())
	dir := knowledgeDir(cfg)

	files, err := listKnowledgeFiles(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Println("No files in knowledge base.")
		fmt.Printf("Add files with: tetora knowledge add <file>\n")
		fmt.Printf("Directory: %s\n", dir)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSIZE\tMODIFIED")
	for _, f := range files {
		fmt.Fprintf(w, "%s\t%s\t%s\n", f.Name, formatSize(f.Size), f.ModTime)
	}
	w.Flush()
	fmt.Printf("\n%d files in %s\n", len(files), dir)
}

func knowledgeAdd(filePath string) {
	cfg := LoadCLIConfig(FindConfigPath())
	dir := knowledgeDir(cfg)

	if err := addKnowledgeFile(dir, filePath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Added %q to knowledge base.\n", filePath)
}

func knowledgeRemove(name string) {
	cfg := LoadCLIConfig(FindConfigPath())
	dir := knowledgeDir(cfg)

	if err := removeKnowledgeFile(dir, name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Removed %q from knowledge base.\n", name)
}

func knowledgeSearch(query string) {
	cfg := LoadCLIConfig(FindConfigPath())
	dir := knowledgeDir(cfg)

	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building index: %v\n", err)
		os.Exit(1)
	}

	results := idx.search(query, 10)
	if len(results) == 0 {
		fmt.Printf("No results for %q in knowledge base.\n", query)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "RANK\tFILE\tSCORE\tSNIPPET")
	for i, r := range results {
		snippet := strings.ReplaceAll(r.Snippet, "\n", " ")
		if len(snippet) > 80 {
			snippet = snippet[:80] + "..."
		}
		fmt.Fprintf(w, "%d\t%s\t%.4f\t%s\n", i+1, r.Filename, r.Score, snippet)
	}
	w.Flush()
	fmt.Printf("\n%d results for %q\n", len(results), query)
}

func knowledgePath() {
	cfg := LoadCLIConfig(FindConfigPath())
	fmt.Println(knowledgeDir(cfg))
}

// --- Knowledge FS operations (replicated from root knowledge.go) ---

func knowledgeDir(cfg *CLIConfig) string {
	if cfg.KnowledgeDir != "" {
		return cfg.KnowledgeDir
	}
	return filepath.Join(cfg.BaseDir, "knowledge")
}

func listKnowledgeFiles(dir string) ([]KnowledgeFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read knowledge dir: %w", err)
	}

	var files []KnowledgeFile
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, KnowledgeFile{
			Name:    e.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
		})
	}
	return files, nil
}

func addKnowledgeFile(dir, sourcePath string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("source file: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("source is a directory, not a file")
	}

	name := filepath.Base(sourcePath)
	if err := validateKnowledgeFilename(name); err != nil {
		return err
	}

	os.MkdirAll(dir, 0o755)

	src, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	dstPath := filepath.Join(dir, name)
	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(dstPath)
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

func removeKnowledgeFile(dir, name string) error {
	if err := validateKnowledgeFilename(name); err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file %q not found in knowledge base", name)
		}
		return err
	}
	return os.Remove(path)
}

func validateKnowledgeFilename(name string) error {
	if name == "" {
		return fmt.Errorf("filename is empty")
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("hidden files not allowed: %q", name)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("path separators not allowed in filename: %q", name)
	}
	if name == ".." || name == "." {
		return fmt.Errorf("invalid filename: %q", name)
	}
	if filepath.Clean(name) != name {
		return fmt.Errorf("unsafe filename: %q", name)
	}
	return nil
}

// formatSize returns a human-readable file size string.
func formatSize(size int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case size >= GB:
		return fmt.Sprintf("%.1fGB", float64(size)/GB)
	case size >= MB:
		return fmt.Sprintf("%.1fMB", float64(size)/MB)
	case size >= KB:
		return fmt.Sprintf("%.1fKB", float64(size)/KB)
	default:
		return fmt.Sprintf("%dB", size)
	}
}

// --- TF-IDF knowledge index (replicated from root knowledge_search.go) ---

type knowledgeSearchResult struct {
	Filename string
	Score    float64
	Snippet  string
}

type knowledgeIndex struct {
	docs map[string]string // filename → content
}

func buildKnowledgeIndex(dir string) (*knowledgeIndex, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return &knowledgeIndex{docs: make(map[string]string)}, nil
		}
		return nil, err
	}

	idx := &knowledgeIndex{docs: make(map[string]string)}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		idx.docs[e.Name()] = string(data)
	}
	return idx, nil
}

func (idx *knowledgeIndex) search(query string, limit int) []knowledgeSearchResult {
	query = strings.ToLower(query)
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return nil
	}

	type scored struct {
		name    string
		score   float64
		content string
	}
	var results []scored

	for name, content := range idx.docs {
		lower := strings.ToLower(content)
		score := 0.0
		for _, term := range terms {
			count := strings.Count(lower, term)
			if count > 0 {
				score += float64(count)
			}
		}
		if score > 0 {
			results = append(results, scored{name: name, score: score, content: content})
		}
	}

	// Sort by score descending.
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].score > results[i].score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	out := make([]knowledgeSearchResult, 0, len(results))
	for _, r := range results {
		snippet := extractSnippet(r.content, query)
		out = append(out, knowledgeSearchResult{
			Filename: r.name,
			Score:    r.score,
			Snippet:  snippet,
		})
	}
	return out
}

func extractSnippet(content, query string) string {
	lower := strings.ToLower(content)
	idx := strings.Index(lower, strings.ToLower(strings.Fields(query)[0]))
	if idx < 0 {
		if len(content) > 200 {
			return content[:200]
		}
		return content
	}
	start := idx - 50
	if start < 0 {
		start = 0
	}
	end := idx + 150
	if end > len(content) {
		end = len(content)
	}
	return content[start:end]
}
