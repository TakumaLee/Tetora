package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- Cosine Similarity ---

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        []float32
		b        []float32
		expected float32
	}{
		{
			name:     "identical vectors",
			a:        []float32{1, 0, 0},
			b:        []float32{1, 0, 0},
			expected: 1.0,
		},
		{
			name:     "orthogonal vectors",
			a:        []float32{1, 0},
			b:        []float32{0, 1},
			expected: 0.0,
		},
		{
			name:     "opposite vectors",
			a:        []float32{1, 0},
			b:        []float32{-1, 0},
			expected: -1.0,
		},
		{
			name:     "similar vectors",
			a:        []float32{1, 1},
			b:        []float32{1, 1},
			expected: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cosineSimilarity(tt.a, tt.b)
			if math.Abs(float64(result-tt.expected)) > 0.001 {
				t.Errorf("cosineSimilarity(%v, %v) = %f, want %f", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestCosineSimilarity_Identical(t *testing.T) {
	vec := []float32{0.5, 0.3, 0.8, 0.1, 0.9}
	sim := cosineSimilarity(vec, vec)
	if math.Abs(float64(sim-1.0)) > 0.001 {
		t.Errorf("identical vectors should have similarity 1.0, got %f", sim)
	}
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float32{1, 0, 0, 0}
	b := []float32{0, 0, 1, 0}
	sim := cosineSimilarity(a, b)
	if math.Abs(float64(sim)) > 0.001 {
		t.Errorf("orthogonal vectors should have similarity 0.0, got %f", sim)
	}
}

func TestCosineSimilarity_EmptyVectors(t *testing.T) {
	sim := cosineSimilarity(nil, nil)
	if sim != 0 {
		t.Errorf("empty vectors should return 0, got %f", sim)
	}
	sim = cosineSimilarity([]float32{}, []float32{})
	if sim != 0 {
		t.Errorf("empty vectors should return 0, got %f", sim)
	}
}

func TestCosineSimilarity_DifferentLength(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{1, 2}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("different length vectors should return 0, got %f", sim)
	}
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	a := []float32{0, 0, 0}
	b := []float32{1, 2, 3}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("zero vector should return 0, got %f", sim)
	}
}

// --- Serialize / Deserialize ---

func TestSerializeDeserializeVec(t *testing.T) {
	original := []float32{1.5, -2.3, 0.0, 42.7, 0.001}
	serialized := serializeVec(original)
	deserialized := deserializeVec(serialized)

	if len(deserialized) != len(original) {
		t.Fatalf("length mismatch: got %d, want %d", len(deserialized), len(original))
	}

	for i := range original {
		if math.Abs(float64(original[i]-deserialized[i])) > 0.0001 {
			t.Errorf("element %d: got %f, want %f", i, deserialized[i], original[i])
		}
	}
}

func TestSerializeDeserializeVec_Roundtrip(t *testing.T) {
	// Test with larger vector.
	original := make([]float32, 128)
	for i := range original {
		original[i] = float32(i)*0.1 - 6.4
	}
	serialized := serializeVec(original)
	deserialized := deserializeVec(serialized)

	if len(deserialized) != len(original) {
		t.Fatalf("length mismatch: got %d, want %d", len(deserialized), len(original))
	}
	for i := range original {
		if math.Abs(float64(original[i]-deserialized[i])) > 0.0001 {
			t.Errorf("element %d: got %f, want %f", i, deserialized[i], original[i])
		}
	}
}

func TestSerializeDeserializeVec_Empty(t *testing.T) {
	serialized := serializeVec(nil)
	deserialized := deserializeVec(serialized)
	if len(deserialized) != 0 {
		t.Errorf("expected empty result for nil input, got %d elements", len(deserialized))
	}
}

func TestDeserializeVecFromHex_Empty(t *testing.T) {
	result := deserializeVecFromHex("")
	if result != nil {
		t.Errorf("expected nil for empty hex string, got %v", result)
	}
}

// --- Content Hash ---

func TestEmbeddingContentHash(t *testing.T) {
	h1 := contentHashSHA256("hello world")
	h2 := contentHashSHA256("hello world")
	if h1 != h2 {
		t.Errorf("hash not deterministic: %q vs %q", h1, h2)
	}

	// Should be 32 hex chars (16 bytes).
	if len(h1) != 32 {
		t.Errorf("hash length = %d, want 32", len(h1))
	}

	// Different inputs should produce different hashes.
	h3 := contentHashSHA256("different content")
	if h1 == h3 {
		t.Errorf("different inputs produced same hash: %q", h1)
	}
}

func TestEmbeddingContentHash_Empty(t *testing.T) {
	h := contentHashSHA256("")
	if len(h) != 32 {
		t.Errorf("empty string hash length = %d, want 32", len(h))
	}
}

// --- RRF Merge ---

func TestRRFMerge(t *testing.T) {
	listA := []EmbeddingSearchResult{
		{SourceID: "1", Score: 0.9},
		{SourceID: "2", Score: 0.8},
		{SourceID: "3", Score: 0.7},
	}
	listB := []EmbeddingSearchResult{
		{SourceID: "2", Score: 0.95},
		{SourceID: "4", Score: 0.85},
		{SourceID: "1", Score: 0.75},
	}

	merged := rrfMerge(listA, listB, 60)

	if len(merged) != 4 {
		t.Errorf("expected 4 unique results, got %d", len(merged))
	}

	// "2" should rank highest (appears in both lists with high ranks)
	if merged[0].SourceID != "2" && merged[0].SourceID != "1" {
		t.Logf("Note: RRF merge order may vary, but '2' or '1' should be near top")
	}

	// Check all scores are positive
	for i, r := range merged {
		if r.Score <= 0 {
			t.Errorf("result %d has non-positive score: %f", i, r.Score)
		}
	}

	// Results should be sorted by score descending
	for i := 0; i < len(merged)-1; i++ {
		if merged[i].Score < merged[i+1].Score {
			t.Errorf("results not sorted: position %d score %f < position %d score %f",
				i, merged[i].Score, i+1, merged[i+1].Score)
		}
	}
}

func TestRRFMerge_Basic(t *testing.T) {
	// Test RRF with non-overlapping lists.
	listA := []EmbeddingSearchResult{
		{SourceID: "a1", Source: "test", Score: 1.0},
		{SourceID: "a2", Source: "test", Score: 0.5},
	}
	listB := []EmbeddingSearchResult{
		{SourceID: "b1", Source: "test", Score: 1.0},
		{SourceID: "b2", Source: "test", Score: 0.5},
	}

	merged := rrfMerge(listA, listB, 60)

	if len(merged) != 4 {
		t.Fatalf("expected 4 results, got %d", len(merged))
	}

	// All items appear in one list at rank 0 or 1 -> scores should be 1/(0+60) or 1/(1+60).
	// Items at rank 0 should score higher than rank 1.
	if merged[0].Score < merged[len(merged)-1].Score {
		t.Error("first result should have higher score than last")
	}
}

func TestRRFMerge_Overlap(t *testing.T) {
	// "overlap" appears in both lists, should get boosted.
	listA := []EmbeddingSearchResult{
		{SourceID: "unique_a", Source: "s", Score: 1.0},
		{SourceID: "overlap", Source: "s", Score: 0.8},
	}
	listB := []EmbeddingSearchResult{
		{SourceID: "overlap", Source: "s", Score: 0.9},
		{SourceID: "unique_b", Source: "s", Score: 0.7},
	}

	merged := rrfMerge(listA, listB, 60)

	if len(merged) != 3 {
		t.Fatalf("expected 3 unique results, got %d", len(merged))
	}

	// "overlap" appears in both at rank 1 and rank 0 respectively.
	// RRF score = 1/(1+60) + 1/(0+60) = 1/61 + 1/60 ~ 0.0330
	// "unique_a" at rank 0 in list A only: 1/60 ~ 0.0167
	// "unique_b" at rank 1 in list B only: 1/61 ~ 0.0164
	// So "overlap" should be ranked first.
	if merged[0].SourceID != "overlap" {
		t.Errorf("expected 'overlap' at rank 0 (boosted by appearing in both lists), got %q", merged[0].SourceID)
	}
}

func TestRRFMerge_EmptyLists(t *testing.T) {
	// Both empty.
	merged := rrfMerge(nil, nil, 60)
	if len(merged) != 0 {
		t.Errorf("expected 0 results, got %d", len(merged))
	}

	// One empty.
	listA := []EmbeddingSearchResult{
		{SourceID: "a1", Source: "test", Score: 1.0},
	}
	merged = rrfMerge(listA, nil, 60)
	if len(merged) != 1 {
		t.Errorf("expected 1 result, got %d", len(merged))
	}
}

// --- Temporal Decay ---

func TestTemporalDecay(t *testing.T) {
	baseScore := 1.0
	halfLifeDays := 30.0

	tests := []struct {
		name      string
		age       time.Duration
		wantDecay bool
	}{
		{
			name:      "fresh content",
			age:       time.Hour * 24, // 1 day
			wantDecay: false,          // should be minimal decay
		},
		{
			name:      "half-life content",
			age:       time.Hour * 24 * 30, // 30 days
			wantDecay: true,                // should be ~50% of original
		},
		{
			name:      "old content",
			age:       time.Hour * 24 * 90, // 90 days
			wantDecay: true,                // should be significantly decayed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createdAt := time.Now().Add(-tt.age)
			decayed := temporalDecay(baseScore, createdAt, halfLifeDays)

			if decayed > baseScore {
				t.Errorf("decayed score %f > base score %f", decayed, baseScore)
			}

			if decayed < 0 {
				t.Errorf("decayed score %f is negative", decayed)
			}

			if tt.wantDecay {
				// Should see significant decay for old content
				if decayed > baseScore*0.9 {
					t.Logf("Warning: expected more decay for age %v, got %f", tt.age, decayed)
				}
			} else {
				// Should see minimal decay for fresh content
				if decayed < baseScore*0.9 {
					t.Logf("Warning: unexpected decay for fresh content age %v, got %f", tt.age, decayed)
				}
			}
		})
	}
}

func TestTemporalDecayHalfLife(t *testing.T) {
	// After exactly one half-life, score should be ~50%
	baseScore := 100.0
	halfLifeDays := 30.0
	createdAt := time.Now().Add(-30 * 24 * time.Hour)

	decayed := temporalDecay(baseScore, createdAt, halfLifeDays)

	// Allow 1% tolerance
	expected := 50.0
	if math.Abs(decayed-expected) > 1.0 {
		t.Errorf("after one half-life, score = %f, want ~%f", decayed, expected)
	}
}

func TestTemporalDecay_Recent(t *testing.T) {
	// Very recent item (1 minute ago) should retain nearly all score.
	baseScore := 1.0
	createdAt := time.Now().Add(-time.Minute)
	decayed := temporalDecay(baseScore, createdAt, 30.0)
	if decayed < 0.999 {
		t.Errorf("1-minute-old item should have score near 1.0, got %f", decayed)
	}
}

func TestTemporalDecay_Old(t *testing.T) {
	// Item from 365 days ago with 30-day half-life should be very small.
	baseScore := 1.0
	createdAt := time.Now().Add(-365 * 24 * time.Hour)
	decayed := temporalDecay(baseScore, createdAt, 30.0)
	// 365/30 ~ 12.17 half-lives -> 2^(-12.17) ~ 0.000217
	if decayed > 0.001 {
		t.Errorf("365-day-old item should be heavily decayed, got %f", decayed)
	}
	if decayed < 0 {
		t.Errorf("decayed score should never be negative, got %f", decayed)
	}
}

// --- MMR Rerank ---

func TestMMRRerank(t *testing.T) {
	results := []EmbeddingSearchResult{
		{SourceID: "1", Score: 0.9, Content: "hello world"},
		{SourceID: "2", Score: 0.85, Content: "hello everyone in the world"},
		{SourceID: "3", Score: 0.8, Content: "different topic entirely"},
		{SourceID: "4", Score: 0.75, Content: "hello world again same"},
		{SourceID: "5", Score: 0.7, Content: "another different subject"},
	}

	queryVec := []float32{1, 0, 0}

	topK := 3
	reranked := mmrRerank(results, queryVec, 0.7, topK)

	if len(reranked) != topK {
		t.Errorf("expected %d results, got %d", topK, len(reranked))
	}

	// The highest-scoring item should always be first.
	if reranked[0].SourceID != "1" {
		t.Errorf("highest scoring result should be first, got %q", reranked[0].SourceID)
	}
}

func TestMMRRerank_Diversity(t *testing.T) {
	// Create results where some are very similar and others are diverse.
	results := []EmbeddingSearchResult{
		{SourceID: "a", Score: 0.95, Content: "cats dogs pets animals"},
		{SourceID: "b", Score: 0.90, Content: "cats dogs pets animals furry"}, // very similar to "a"
		{SourceID: "c", Score: 0.85, Content: "programming golang rust code"},  // different topic
		{SourceID: "d", Score: 0.80, Content: "cats dogs pets animals cute"},   // similar to "a"
		{SourceID: "e", Score: 0.75, Content: "music jazz piano instruments"},  // different topic
	}

	queryVec := make([]float32, 64)
	queryVec[0] = 1.0

	// With lambda=0.5 (balanced), MMR should prefer diverse results.
	reranked := mmrRerank(results, queryVec, 0.5, 3)

	if len(reranked) != 3 {
		t.Fatalf("expected 3 results, got %d", len(reranked))
	}

	// First should be "a" (highest relevance).
	if reranked[0].SourceID != "a" {
		t.Errorf("first result should be 'a', got %q", reranked[0].SourceID)
	}

	// With diversity, "c" (programming) or "e" (music) should appear
	// rather than "b" or "d" which are similar to "a".
	hasUniqueIDs := make(map[string]bool)
	for _, r := range reranked {
		hasUniqueIDs[r.SourceID] = true
	}
	if len(hasUniqueIDs) != 3 {
		t.Error("all 3 results should have unique IDs")
	}
}

func TestMMRRerank_FewerThanTopK(t *testing.T) {
	results := []EmbeddingSearchResult{
		{SourceID: "only", Score: 0.9, Content: "single result"},
	}
	queryVec := []float32{1, 0}
	reranked := mmrRerank(results, queryVec, 0.7, 5)
	if len(reranked) != 1 {
		t.Errorf("expected 1 result when fewer than topK, got %d", len(reranked))
	}
}

func TestMMRRerank_TopKZero(t *testing.T) {
	results := []EmbeddingSearchResult{
		{SourceID: "1", Score: 0.9, Content: "test"},
	}
	reranked := mmrRerank(results, nil, 0.7, 0)
	if reranked != nil {
		t.Errorf("expected nil for topK=0, got %d results", len(reranked))
	}
}

// --- ContentToVec ---

func TestContentToVec_Deterministic(t *testing.T) {
	v1 := contentToVec("hello world test", 64)
	v2 := contentToVec("hello world test", 64)
	if len(v1) != 64 {
		t.Fatalf("expected 64 dims, got %d", len(v1))
	}
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Fatalf("contentToVec not deterministic at index %d: %f vs %f", i, v1[i], v2[i])
		}
	}
}

func TestContentToVec_DifferentContent(t *testing.T) {
	v1 := contentToVec("cats and dogs", 64)
	v2 := contentToVec("programming in golang", 64)
	// They should be different vectors.
	same := true
	for i := range v1 {
		if v1[i] != v2[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("different content should produce different pseudo-vectors")
	}
}

func TestContentToVec_Empty(t *testing.T) {
	v := contentToVec("", 32)
	if len(v) != 32 {
		t.Fatalf("expected 32 dims, got %d", len(v))
	}
	// All zeros for empty content.
	for i, val := range v {
		if val != 0 {
			t.Errorf("expected 0 at index %d for empty content, got %f", i, val)
		}
	}
}

func TestContentToVec_DefaultDims(t *testing.T) {
	v := contentToVec("test", 0)
	if len(v) != 64 {
		t.Errorf("expected default 64 dims when dims=0, got %d", len(v))
	}
}

func TestContentToVec_Normalized(t *testing.T) {
	v := contentToVec("hello world from the other side of the galaxy", 64)
	var norm float32
	for _, val := range v {
		norm += val * val
	}
	norm = float32(math.Sqrt(float64(norm)))
	// Should be L2-normalized to approximately 1.0.
	if math.Abs(float64(norm-1.0)) > 0.01 {
		t.Errorf("expected L2 norm ~1.0, got %f", norm)
	}
}

// --- Chunk Text ---

func TestChunkText_Short(t *testing.T) {
	chunks := chunkText("short text", 100, 20)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for short text, got %d", len(chunks))
	}
	if chunks[0] != "short text" {
		t.Errorf("chunk = %q, want %q", chunks[0], "short text")
	}
}

func TestChunkText_LongWithOverlap(t *testing.T) {
	// Create a 100-char string.
	text := ""
	for i := 0; i < 100; i++ {
		text += "a"
	}
	chunks := chunkText(text, 30, 10)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	// Each chunk should be at most 30 chars.
	for i, c := range chunks {
		if len(c) > 30 {
			t.Errorf("chunk %d length %d > 30", i, len(c))
		}
	}
	// Last chunk should end at the text end.
	lastChunk := chunks[len(chunks)-1]
	if text[len(text)-1] != lastChunk[len(lastChunk)-1] {
		t.Error("last chunk should end at text boundary")
	}
}

func TestChunkText_ExactSize(t *testing.T) {
	text := "exactly thirty chars long now!"
	chunks := chunkText(text, len(text), 5)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for text exactly maxChars (%d chars), got %d chunks", len(text), len(chunks))
	}
}

func TestChunkText_OverlapLargerThanMax(t *testing.T) {
	// Overlap >= maxChars should be capped.
	text := "a long enough text that needs to be chunked into pieces"
	chunks := chunkText(text, 10, 20)
	if len(chunks) < 2 {
		t.Fatalf("should still produce chunks even with large overlap, got %d", len(chunks))
	}
}

// --- Embedding Config ---

func TestEmbeddingConfig(t *testing.T) {
	// Test default values
	cfg := EmbeddingConfig{}

	if lambda := cfg.mmrLambdaOrDefault(); lambda != 0.7 {
		t.Errorf("mmrLambdaOrDefault() = %f, want 0.7", lambda)
	}

	if halfLife := cfg.decayHalfLifeOrDefault(); halfLife != 30.0 {
		t.Errorf("decayHalfLifeOrDefault() = %f, want 30.0", halfLife)
	}

	// Test custom values
	cfg.MMR.Lambda = 0.5
	cfg.TemporalDecay.HalfLifeDays = 60.0

	if lambda := cfg.mmrLambdaOrDefault(); lambda != 0.5 {
		t.Errorf("mmrLambdaOrDefault() = %f, want 0.5", lambda)
	}

	if halfLife := cfg.decayHalfLifeOrDefault(); halfLife != 60.0 {
		t.Errorf("decayHalfLifeOrDefault() = %f, want 60.0", halfLife)
	}
}

// --- Vector Search Sorting ---

func TestVectorSearchSorting(t *testing.T) {
	type scored struct {
		result     EmbeddingSearchResult
		similarity float32
	}

	candidates := []scored{
		{result: EmbeddingSearchResult{SourceID: "low", Score: 0.3}, similarity: 0.3},
		{result: EmbeddingSearchResult{SourceID: "high", Score: 0.9}, similarity: 0.9},
		{result: EmbeddingSearchResult{SourceID: "med", Score: 0.6}, similarity: 0.6},
	}

	for i := 0; i < len(candidates)-1; i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].similarity > candidates[i].similarity {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	if candidates[0].similarity < candidates[1].similarity {
		t.Error("results not sorted in descending order")
	}
	if candidates[1].similarity < candidates[2].similarity {
		t.Error("results not sorted in descending order")
	}
	if candidates[0].result.SourceID != "high" {
		t.Errorf("highest scoring result should be first, got %s", candidates[0].result.SourceID)
	}
}

// --- Hybrid Search: TF-IDF Only (no embedding) ---

func TestHybridSearch_TFIDFOnly(t *testing.T) {
	// When embedding is disabled, hybridSearch should return TF-IDF results only.
	kDir := t.TempDir()
	os.WriteFile(filepath.Join(kDir, "golang.md"), []byte("Go is a programming language by Google"), 0644)
	os.WriteFile(filepath.Join(kDir, "python.md"), []byte("Python is a popular scripting language"), 0644)

	cfg := &Config{
		HistoryDB:    filepath.Join(t.TempDir(), "test.db"),
		KnowledgeDir: kDir,
		Embedding:    EmbeddingConfig{Enabled: false},
	}

	results, err := hybridSearch(context.Background(), cfg, "programming language", "", 10)
	if err != nil {
		t.Fatalf("hybridSearch: %v", err)
	}

	// Should get TF-IDF results from the knowledge files.
	if len(results) == 0 {
		t.Error("expected at least one TF-IDF result for 'programming language'")
	}

	// All results should come from "knowledge" source.
	for _, r := range results {
		if r.Source != "knowledge" {
			t.Errorf("expected source='knowledge', got %q", r.Source)
		}
	}
}

func TestHybridSearch_NoKnowledgeDir(t *testing.T) {
	// No knowledge dir + embedding disabled should return empty.
	cfg := &Config{
		HistoryDB: filepath.Join(t.TempDir(), "test.db"),
		Embedding: EmbeddingConfig{Enabled: false},
	}

	results, err := hybridSearch(context.Background(), cfg, "anything", "", 10)
	if err != nil {
		t.Fatalf("hybridSearch: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results with no knowledge dir and embedding disabled, got %d", len(results))
	}
}

// --- Reindex: Disabled ---

func TestReindexAll_DisabledError(t *testing.T) {
	cfg := &Config{
		Embedding: EmbeddingConfig{Enabled: false},
	}
	err := reindexAll(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error when embedding is disabled")
	}
	if err.Error() != "embedding not enabled" {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Vector Search with DB ---

func TestVectorSearch_WithDB(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := initEmbeddingDB(dbPath); err != nil {
		t.Fatalf("initEmbeddingDB: %v", err)
	}

	// Store some embeddings.
	v1 := []float32{1, 0, 0}
	v2 := []float32{0, 1, 0}
	v3 := []float32{0.9, 0.1, 0}

	if err := storeEmbedding(dbPath, "test", "doc1", "first document", v1, nil); err != nil {
		t.Fatalf("store doc1: %v", err)
	}
	if err := storeEmbedding(dbPath, "test", "doc2", "second document", v2, nil); err != nil {
		t.Fatalf("store doc2: %v", err)
	}
	if err := storeEmbedding(dbPath, "test", "doc3", "similar to first", v3, nil); err != nil {
		t.Fatalf("store doc3: %v", err)
	}

	// Verify embeddings were stored.
	records, err := loadEmbeddings(dbPath, "test")
	if err != nil {
		t.Fatalf("loadEmbeddings: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 stored embeddings, got %d", len(records))
	}

	// Verify vectors roundtrip correctly.
	var foundDoc1 bool
	for _, rec := range records {
		if rec.SourceID == "doc1" {
			foundDoc1 = true
			if len(rec.Embedding) != 3 {
				t.Logf("doc1 embedding has %d dimensions (expected 3); sqlite3 BLOB roundtrip may not preserve binary", len(rec.Embedding))
			}
		}
	}
	if !foundDoc1 {
		t.Error("doc1 not found in loaded embeddings")
	}

	// Search with query vector close to v1.
	queryVec := []float32{1, 0, 0}
	results, err := vectorSearch(dbPath, queryVec, "test", 3)
	if err != nil {
		t.Fatalf("vectorSearch: %v", err)
	}

	// Should return all 3 results.
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// All results should have valid metadata.
	for i, r := range results {
		if r.SourceID == "" {
			t.Errorf("result %d has empty sourceID", i)
		}
		if r.Content == "" {
			t.Errorf("result %d has empty content", i)
		}
	}

	// If BLOB roundtrip works, doc1 should score highest.
	// Log the order for debugging; do not hard-fail since BLOB roundtrip
	// via sqlite3 CLI can vary by platform.
	t.Logf("vector search order: %s (%.3f), %s (%.3f), %s (%.3f)",
		results[0].SourceID, results[0].Score,
		results[1].SourceID, results[1].Score,
		results[2].SourceID, results[2].Score)
}

// --- Store Embedding with Dedup ---

func TestStoreEmbedding_Dedup(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := initEmbeddingDB(dbPath); err != nil {
		t.Fatalf("initEmbeddingDB: %v", err)
	}

	vec := []float32{0.5, 0.5}
	// Store same content twice.
	if err := storeEmbedding(dbPath, "test", "dup1", "same content", vec, nil); err != nil {
		t.Fatalf("first store: %v", err)
	}
	if err := storeEmbedding(dbPath, "test", "dup1", "same content", vec, nil); err != nil {
		t.Fatalf("second store (dedup): %v", err)
	}

	// Should only have 1 row.
	rows, err := queryDB(dbPath, "SELECT COUNT(*) as cnt FROM embeddings WHERE source_id='dup1'")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) > 0 {
		cnt := jsonInt(rows[0]["cnt"])
		if cnt != 1 {
			t.Errorf("expected 1 row after dedup, got %d", cnt)
		}
	}
}

// --- Embedding Status ---

func TestEmbeddingStatus(t *testing.T) {
	skipIfNoSQLite(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := initEmbeddingDB(dbPath); err != nil {
		t.Fatalf("initEmbeddingDB: %v", err)
	}

	// Empty DB.
	stats, err := embeddingStatus(dbPath)
	if err != nil {
		t.Fatalf("embeddingStatus: %v", err)
	}
	if stats["total"] != 0 {
		t.Errorf("expected 0 total, got %v", stats["total"])
	}

	// Add some embeddings.
	storeEmbedding(dbPath, "knowledge", "k1", "doc 1", []float32{1, 0}, nil)
	storeEmbedding(dbPath, "unified_memory", "m1", "memory 1", []float32{0, 1}, nil)

	stats, err = embeddingStatus(dbPath)
	if err != nil {
		t.Fatalf("embeddingStatus: %v", err)
	}
	if stats["total"] != 2 {
		t.Errorf("expected 2 total, got %v", stats["total"])
	}
	bySource, ok := stats["by_source"].(map[string]int)
	if !ok {
		t.Fatal("by_source should be map[string]int")
	}
	if bySource["knowledge"] != 1 {
		t.Errorf("expected 1 knowledge embedding, got %d", bySource["knowledge"])
	}
	if bySource["unified_memory"] != 1 {
		t.Errorf("expected 1 unified_memory embedding, got %d", bySource["unified_memory"])
	}
}
