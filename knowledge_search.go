package main

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// SearchResult represents a matched knowledge chunk.
type SearchResult struct {
	Filename  string  `json:"filename"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
	LineStart int     `json:"lineStart"`
}

// knowledgeIndex is an in-memory TF-IDF index.
type knowledgeIndex struct {
	mu        sync.RWMutex
	docs      map[string]*docEntry
	idf       map[string]float64
	totalDocs int
}

type docEntry struct {
	filename string
	lines    []string
	tf       map[string]float64
	size     int64
}

// buildKnowledgeIndex scans all files in dir, reads their content,
// tokenizes, and builds a TF-IDF index.
func buildKnowledgeIndex(dir string) (*knowledgeIndex, error) {
	idx := &knowledgeIndex{
		docs: make(map[string]*docEntry),
		idf:  make(map[string]float64),
	}
	if err := idx.rebuild(dir); err != nil {
		return nil, err
	}
	return idx, nil
}

// rebuild re-scans the directory and rebuilds the entire index.
func (idx *knowledgeIndex) rebuild(dir string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			idx.docs = make(map[string]*docEntry)
			idx.idf = make(map[string]float64)
			idx.totalDocs = 0
			return nil
		}
		return err
	}

	docs := make(map[string]*docEntry)
	// df tracks how many documents contain each term.
	df := make(map[string]int)

	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}

		content := string(data)
		lines := strings.Split(content, "\n")
		tokens := tokenize(content)

		// Compute term frequency: count(term) / total tokens.
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

		// Track document frequency per term.
		seen := make(map[string]bool)
		for _, tok := range tokens {
			if !seen[tok] {
				df[tok]++
				seen[tok] = true
			}
		}

		docs[e.Name()] = &docEntry{
			filename: e.Name(),
			lines:    lines,
			tf:       tf,
			size:     info.Size(),
		}
	}

	totalDocs := len(docs)

	// Compute IDF: log(1 + totalDocs / (1 + df))
	idf := make(map[string]float64)
	for term, docCount := range df {
		idf[term] = math.Log(1.0 + float64(totalDocs)/float64(1+docCount))
	}

	idx.docs = docs
	idx.idf = idf
	idx.totalDocs = totalDocs
	return nil
}

// search returns documents ranked by TF-IDF score for the given query.
func (idx *knowledgeIndex) search(query string, maxResults int) []SearchResult {
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

		// Find the best matching line for snippet extraction.
		bestLine := findBestMatchLine(doc.lines, queryTokens)

		results = append(results, scored{
			filename:  doc.filename,
			score:     score,
			matchLine: bestLine,
		})
	}

	// Sort by score descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

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
			LineStart: r.matchLine + 1, // 1-based line number
		})
	}
	return out
}

// findBestMatchLine returns the 0-based line index with the most query term hits.
func findBestMatchLine(lines []string, queryTokens []string) int {
	bestLine := 0
	bestHits := 0

	for i, line := range lines {
		lineTokens := tokenize(line)
		lineSet := make(map[string]bool)
		for _, lt := range lineTokens {
			lineSet[lt] = true
		}
		hits := 0
		for _, qt := range queryTokens {
			if lineSet[qt] {
				hits++
			}
		}
		if hits > bestHits {
			bestHits = hits
			bestLine = i
		}
	}
	return bestLine
}

// tokenize splits text into lowercase Latin words and CJK unigrams/bigrams.
func tokenize(text string) []string {
	var tokens []string

	// Extract Latin words.
	var word []rune
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if isCJK(r) {
			// Flush any pending Latin word.
			if len(word) > 0 {
				tokens = append(tokens, strings.ToLower(string(word)))
				word = word[:0]
			}
			// CJK unigram.
			tokens = append(tokens, string(r))
			// CJK bigram: pair with next CJK character.
			if i+1 < len(runes) && isCJK(runes[i+1]) {
				tokens = append(tokens, string(runes[i])+string(runes[i+1]))
			}
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word = append(word, unicode.ToLower(r))
		} else {
			if len(word) > 0 {
				tokens = append(tokens, string(word))
				word = word[:0]
			}
		}
	}
	if len(word) > 0 {
		tokens = append(tokens, string(word))
	}
	return tokens
}

// isCJK returns true if the rune is a CJK character (Chinese, Japanese Hiragana/Katakana).
func isCJK(r rune) bool {
	return (r >= '\u4e00' && r <= '\u9fff') || // CJK Unified Ideographs
		(r >= '\u3040' && r <= '\u309f') || // Hiragana
		(r >= '\u30a0' && r <= '\u30ff') // Katakana
}

// buildSnippet extracts a snippet from lines around the matchLine.
// contextLines specifies how many lines before and after to include.
func buildSnippet(lines []string, matchLine, contextLines int) string {
	if len(lines) == 0 {
		return ""
	}
	if matchLine < 0 {
		matchLine = 0
	}
	if matchLine >= len(lines) {
		matchLine = len(lines) - 1
	}

	start := matchLine - contextLines
	if start < 0 {
		start = 0
	}
	end := matchLine + contextLines + 1
	if end > len(lines) {
		end = len(lines)
	}

	selected := lines[start:end]
	snippet := strings.Join(selected, "\n")

	// Truncate long snippets.
	const maxSnippetLen = 200
	if len(snippet) > maxSnippetLen {
		snippet = snippet[:maxSnippetLen] + "..."
	}
	return snippet
}
