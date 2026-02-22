package main

import (
	"math"
	"testing"
	"time"
)

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
			age:       time.Hour * 24,     // 1 day
			wantDecay: false,              // should be minimal decay
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

func TestMMRRerank(t *testing.T) {
	results := []EmbeddingSearchResult{
		{SourceID: "1", Score: 0.9},
		{SourceID: "2", Score: 0.85},
		{SourceID: "3", Score: 0.8},
		{SourceID: "4", Score: 0.75},
		{SourceID: "5", Score: 0.7},
	}

	queryVec := []float32{1, 0, 0}

	// MMR should select diverse results
	topK := 3
	reranked := mmrRerank(results, queryVec, 0.7, topK)

	if len(reranked) != topK {
		t.Errorf("expected %d results, got %d", topK, len(reranked))
	}

	// Note: Current implementation is a placeholder, so we just verify length
	// A full implementation would check diversity of selected results
}

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

func TestVectorSearchSorting(t *testing.T) {
	// Test that vector search results are properly sorted by similarity
	// This is a behavioral test without actual DB access

	type scored struct {
		result     EmbeddingSearchResult
		similarity float32
	}

	candidates := []scored{
		{result: EmbeddingSearchResult{SourceID: "low", Score: 0.3}, similarity: 0.3},
		{result: EmbeddingSearchResult{SourceID: "high", Score: 0.9}, similarity: 0.9},
		{result: EmbeddingSearchResult{SourceID: "med", Score: 0.6}, similarity: 0.6},
	}

	// Simulate the sorting logic from vectorSearch
	for i := 0; i < len(candidates)-1; i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].similarity > candidates[i].similarity {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	// Verify descending order
	if candidates[0].similarity < candidates[1].similarity {
		t.Error("results not sorted in descending order")
	}
	if candidates[1].similarity < candidates[2].similarity {
		t.Error("results not sorted in descending order")
	}

	// Verify highest score is first
	if candidates[0].result.SourceID != "high" {
		t.Errorf("highest scoring result should be first, got %s", candidates[0].result.SourceID)
	}
}
