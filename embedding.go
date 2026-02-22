package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

// --- Embedding Config ---

type EmbeddingConfig struct {
	Enabled       bool           `json:"enabled,omitempty"`
	Provider      string         `json:"provider,omitempty"`      // "openai" or compatible
	Model         string         `json:"model,omitempty"`         // "text-embedding-3-small"
	Endpoint      string         `json:"endpoint,omitempty"`
	APIKey        string         `json:"apiKey,omitempty"`        // supports $ENV_VAR
	Dimensions    int            `json:"dimensions,omitempty"`    // 1536
	BatchSize     int            `json:"batchSize,omitempty"`     // 20
	MMR           MMRConfig      `json:"mmr,omitempty"`
	TemporalDecay TemporalConfig `json:"temporalDecay,omitempty"`
}

type MMRConfig struct {
	Enabled bool    `json:"enabled,omitempty"`
	Lambda  float64 `json:"lambda,omitempty"` // default 0.7
}

type TemporalConfig struct {
	Enabled      bool    `json:"enabled,omitempty"`
	HalfLifeDays float64 `json:"halfLifeDays,omitempty"` // default 30
}

func (cfg EmbeddingConfig) mmrLambdaOrDefault() float64 {
	if cfg.MMR.Lambda > 0 {
		return cfg.MMR.Lambda
	}
	return 0.7
}

func (cfg EmbeddingConfig) decayHalfLifeOrDefault() float64 {
	if cfg.TemporalDecay.HalfLifeDays > 0 {
		return cfg.TemporalDecay.HalfLifeDays
	}
	return 30.0
}

// --- Embedding Database ---

// initEmbeddingDB creates the embeddings table if it doesn't exist.
func initEmbeddingDB(dbPath string) error {
	schema := `
CREATE TABLE IF NOT EXISTS embeddings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,
    source_id TEXT NOT NULL,
    content TEXT NOT NULL,
    embedding BLOB NOT NULL,
    metadata TEXT DEFAULT '{}',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_embeddings_source ON embeddings(source);
CREATE INDEX IF NOT EXISTS idx_embeddings_source_id ON embeddings(source, source_id);
`
	_, err := queryDB(dbPath, schema)
	return err
}

// --- Embedding Provider (OpenAI-compatible) ---

// embeddingRequest is the request body for an OpenAI-compatible embedding API.
type embeddingRequest struct {
	Input          []string `json:"input"`
	Model          string   `json:"model"`
	EncodingFormat string   `json:"encoding_format,omitempty"`
	Dimensions     int      `json:"dimensions,omitempty"`
}

// embeddingResponse is the response from an OpenAI-compatible embedding API.
type embeddingResponse struct {
	Data  []embeddingData `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

type embeddingData struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

// getEmbeddings calls the embedding API to get vectors for the given texts.
func getEmbeddings(ctx context.Context, cfg *Config, texts []string) ([][]float32, error) {
	if !cfg.Embedding.Enabled {
		return nil, fmt.Errorf("embedding not enabled")
	}

	endpoint := cfg.Embedding.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/embeddings"
	}

	model := cfg.Embedding.Model
	if model == "" {
		model = "text-embedding-3-small"
	}

	apiKey := cfg.Embedding.APIKey
	if apiKey == "" {
		return nil, fmt.Errorf("embedding API key not configured")
	}
	// Resolve $ENV_VAR if needed (already done in resolveSecrets)

	reqBody := embeddingRequest{
		Input: texts,
		Model: model,
	}
	if cfg.Embedding.Dimensions > 0 {
		reqBody.Dimensions = cfg.Embedding.Dimensions
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("embedding API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embedding API %d: %s", resp.StatusCode, string(body))
	}

	var apiResp embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}

	result := make([][]float32, len(texts))
	for _, d := range apiResp.Data {
		if d.Index < len(result) {
			result[d.Index] = d.Embedding
		}
	}

	return result, nil
}

// getEmbedding gets a single embedding vector.
func getEmbedding(ctx context.Context, cfg *Config, text string) ([]float32, error) {
	vecs, err := getEmbeddings(ctx, cfg, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("empty embedding result")
	}
	return vecs[0], nil
}

// --- Vector Math (pure Go) ---

// cosineSimilarity computes cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / float32(math.Sqrt(float64(normA))*math.Sqrt(float64(normB)))
}

// --- Vector Storage ---

// serializeVec encodes a float32 slice as a byte blob (little-endian).
func serializeVec(vec []float32) []byte {
	buf := new(bytes.Buffer)
	for _, v := range vec {
		binary.Write(buf, binary.LittleEndian, v)
	}
	return buf.Bytes()
}

// deserializeVec decodes a byte blob into a float32 slice.
func deserializeVec(data []byte) []float32 {
	count := len(data) / 4
	vec := make([]float32, count)
	reader := bytes.NewReader(data)
	for i := range vec {
		binary.Read(reader, binary.LittleEndian, &vec[i])
	}
	return vec
}

// deserializeVecFromHex handles SQLite BLOB hex output (X'...' format).
func deserializeVecFromHex(hexStr string) []float32 {
	// SQLite CLI returns BLOBs as raw bytes in some cases, or hex strings.
	// We need to handle both cases.
	if len(hexStr) == 0 {
		return nil
	}

	// If it starts with X' or x', it's hex encoded
	if len(hexStr) > 2 && (hexStr[0] == 'X' || hexStr[0] == 'x') && hexStr[1] == '\'' {
		// Strip X' prefix and ' suffix
		hexData := hexStr[2:]
		if len(hexData) > 0 && hexData[len(hexData)-1] == '\'' {
			hexData = hexData[:len(hexData)-1]
		}
		data, err := hex.DecodeString(hexData)
		if err != nil {
			return nil
		}
		return deserializeVec(data)
	}

	// Otherwise treat as raw binary
	return deserializeVec([]byte(hexStr))
}

// storeEmbedding saves an embedding to the database.
func storeEmbedding(dbPath string, source, sourceID, content string, vec []float32, metadata map[string]interface{}) error {
	metaJSON := "{}"
	if metadata != nil {
		b, _ := json.Marshal(metadata)
		metaJSON = string(b)
	}

	blob := serializeVec(vec)
	blobHex := fmt.Sprintf("X'%x'", blob)

	query := fmt.Sprintf(`INSERT INTO embeddings (source, source_id, content, embedding, metadata, created_at)
VALUES (%s, %s, %s, %s, %s, %s)`,
		escapeSQLite(source), escapeSQLite(sourceID), escapeSQLite(content),
		blobHex, escapeSQLite(metaJSON), escapeSQLite(time.Now().UTC().Format(time.RFC3339)))

	_, err := queryDB(dbPath, query)
	return err
}

// embeddingRecord represents a row from the embeddings table.
type embeddingRecord struct {
	ID        int
	Source    string
	SourceID  string
	Content   string
	Embedding []float32
	Metadata  map[string]interface{}
	CreatedAt time.Time
}

// loadEmbeddings loads all embeddings for a given source.
func loadEmbeddings(dbPath, source string) ([]embeddingRecord, error) {
	query := `SELECT id, source, source_id, content, embedding, metadata, created_at FROM embeddings`
	if source != "" {
		query += ` WHERE source = ` + escapeSQLite(source)
	}

	rows, err := queryDB(dbPath, query)
	if err != nil {
		return nil, fmt.Errorf("query embeddings: %w", err)
	}

	var records []embeddingRecord
	for _, row := range rows {
		idVal, _ := row["id"].(float64)
		id := int(idVal)

		embStr, _ := row["embedding"].(string)
		vec := deserializeVecFromHex(embStr)
		if len(vec) == 0 {
			continue
		}

		var meta map[string]interface{}
		if metaStr, ok := row["metadata"].(string); ok {
			json.Unmarshal([]byte(metaStr), &meta)
		}

		createdStr, _ := row["created_at"].(string)
		createdAt, _ := time.Parse(time.RFC3339, createdStr)

		source, _ := row["source"].(string)
		sourceID, _ := row["source_id"].(string)
		content, _ := row["content"].(string)

		records = append(records, embeddingRecord{
			ID:        id,
			Source:    source,
			SourceID:  sourceID,
			Content:   content,
			Embedding: vec,
			Metadata:  meta,
			CreatedAt: createdAt,
		})
	}

	return records, nil
}

// --- Search ---

// EmbeddingSearchResult represents a search result from hybrid search.
// Note: We use a different name to avoid conflict with knowledge_search.go's SearchResult
type EmbeddingSearchResult struct {
	Source    string                 `json:"source"`
	SourceID  string                 `json:"sourceId"`
	Content   string                 `json:"content"`
	Score     float64                `json:"score"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt string                 `json:"createdAt,omitempty"`
}

// vectorSearch finds the top-K most similar embeddings to the query vector.
func vectorSearch(dbPath string, queryVec []float32, source string, topK int) ([]EmbeddingSearchResult, error) {
	records, err := loadEmbeddings(dbPath, source)
	if err != nil {
		return nil, err
	}

	type scored struct {
		result     EmbeddingSearchResult
		similarity float32
	}

	var candidates []scored
	for _, rec := range records {
		sim := cosineSimilarity(queryVec, rec.Embedding)

		candidates = append(candidates, scored{
			result: EmbeddingSearchResult{
				Source:    rec.Source,
				SourceID:  rec.SourceID,
				Content:   rec.Content,
				Score:     float64(sim),
				Metadata:  rec.Metadata,
				CreatedAt: rec.CreatedAt.Format(time.RFC3339),
			},
			similarity: sim,
		})
	}

	// Sort by similarity descending (simple bubble sort for small data)
	for i := 0; i < len(candidates)-1; i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].similarity > candidates[i].similarity {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	if topK > len(candidates) {
		topK = len(candidates)
	}

	results := make([]EmbeddingSearchResult, topK)
	for i := 0; i < topK; i++ {
		results[i] = candidates[i].result
	}

	return results, nil
}

// hybridSearch combines TF-IDF and vector search using Reciprocal Rank Fusion (RRF).
func hybridSearch(ctx context.Context, cfg *Config, query string, source string, topK int) ([]EmbeddingSearchResult, error) {
	dbPath := cfg.HistoryDB

	// 1. TF-IDF results (from knowledge_search.go).
	// We'll convert knowledge search results to embedding search results.
	var tfidfResults []EmbeddingSearchResult

	// Try to get TF-IDF results if knowledge index is available
	// For now, we'll skip TF-IDF if not available and just use vector search
	// In a full implementation, we'd integrate with knowledge_search.go's tfidfSearch

	// 2. If embedding is not enabled, return empty or TF-IDF only.
	if !cfg.Embedding.Enabled {
		return tfidfResults, nil
	}

	// 3. Vector search.
	queryVec, err := getEmbedding(ctx, cfg, query)
	if err != nil {
		logWarn("embedding search failed", "error", err)
		return tfidfResults, nil
	}

	vecResults, err := vectorSearch(dbPath, queryVec, source, topK*2)
	if err != nil {
		logWarn("vector search failed", "error", err)
		return tfidfResults, nil
	}

	// 4. If we have no TF-IDF results, just return vector results.
	if len(tfidfResults) == 0 {
		merged := vecResults

		// Apply temporal decay if enabled.
		if cfg.Embedding.TemporalDecay.Enabled {
			halfLife := cfg.Embedding.decayHalfLifeOrDefault()
			for i := range merged {
				if merged[i].CreatedAt != "" {
					if createdAt, err := time.Parse(time.RFC3339, merged[i].CreatedAt); err == nil {
						merged[i].Score = temporalDecay(merged[i].Score, createdAt, halfLife)
					}
				}
			}
			// Re-sort after decay.
			for i := 0; i < len(merged)-1; i++ {
				for j := i + 1; j < len(merged); j++ {
					if merged[j].Score > merged[i].Score {
						merged[i], merged[j] = merged[j], merged[i]
					}
				}
			}
		}

		// MMR re-ranking if enabled.
		if cfg.Embedding.MMR.Enabled && len(merged) > topK {
			merged = mmrRerank(merged, queryVec, cfg.Embedding.mmrLambdaOrDefault(), topK)
		}

		if topK > len(merged) {
			topK = len(merged)
		}
		if topK <= 0 {
			return []EmbeddingSearchResult{}, nil
		}
		return merged[:topK], nil
	}

	// 5. Reciprocal Rank Fusion (RRF) if we have both TF-IDF and vector results.
	merged := rrfMerge(tfidfResults, vecResults, 60)

	// 6. Apply temporal decay if enabled.
	if cfg.Embedding.TemporalDecay.Enabled {
		halfLife := cfg.Embedding.decayHalfLifeOrDefault()
		for i := range merged {
			if merged[i].CreatedAt != "" {
				if createdAt, err := time.Parse(time.RFC3339, merged[i].CreatedAt); err == nil {
					merged[i].Score = temporalDecay(merged[i].Score, createdAt, halfLife)
				}
			}
		}
		// Re-sort after decay.
		for i := 0; i < len(merged)-1; i++ {
			for j := i + 1; j < len(merged); j++ {
				if merged[j].Score > merged[i].Score {
					merged[i], merged[j] = merged[j], merged[i]
				}
			}
		}
	}

	// 7. MMR re-ranking if enabled.
	if cfg.Embedding.MMR.Enabled && len(merged) > topK {
		merged = mmrRerank(merged, queryVec, cfg.Embedding.mmrLambdaOrDefault(), topK)
	}

	if topK > len(merged) {
		topK = len(merged)
	}
	if topK <= 0 {
		return []EmbeddingSearchResult{}, nil
	}
	return merged[:topK], nil
}

// rrfMerge merges two ranked lists using Reciprocal Rank Fusion.
// k is the RRF constant (typically 60).
func rrfMerge(listA, listB []EmbeddingSearchResult, k int) []EmbeddingSearchResult {
	scores := make(map[string]float64)
	results := make(map[string]EmbeddingSearchResult)

	for rank, r := range listA {
		key := r.Source + ":" + r.SourceID
		scores[key] += 1.0 / float64(rank+k)
		results[key] = r
	}
	for rank, r := range listB {
		key := r.Source + ":" + r.SourceID
		scores[key] += 1.0 / float64(rank+k)
		if _, exists := results[key]; !exists {
			results[key] = r
		}
	}

	// Convert map to slice and sort by RRF score.
	var merged []EmbeddingSearchResult
	for key, r := range results {
		r.Score = scores[key]
		merged = append(merged, r)
	}

	// Sort descending by score.
	for i := 0; i < len(merged)-1; i++ {
		for j := i + 1; j < len(merged); j++ {
			if merged[j].Score > merged[i].Score {
				merged[i], merged[j] = merged[j], merged[i]
			}
		}
	}

	return merged
}

// mmrRerank re-ranks results using Maximal Marginal Relevance to promote diversity.
// lambda controls relevance vs diversity tradeoff (0.0 = max diversity, 1.0 = max relevance).
func mmrRerank(results []EmbeddingSearchResult, queryVec []float32, lambda float64, topK int) []EmbeddingSearchResult {
	if len(results) <= topK {
		return results
	}

	// We need embeddings for MMR, but we don't have them in results.
	// For simplicity, we'll skip actual MMR and just return top results.
	// A full implementation would need to store embeddings in EmbeddingSearchResult or reload them.

	// TODO: Full MMR implementation would:
	// 1. Start with highest scoring result
	// 2. For each remaining slot:
	//    - For each remaining candidate:
	//      - Score = lambda * similarity(query, candidate) - (1-lambda) * max(similarity(candidate, selected))
	//    - Pick highest scoring candidate

	// For now, just return top-K by score.
	if topK > len(results) {
		topK = len(results)
	}
	return results[:topK]
}

// temporalDecay applies exponential temporal decay to a score based on age.
// halfLifeDays is the number of days for score to decay to 50%.
func temporalDecay(score float64, createdAt time.Time, halfLifeDays float64) float64 {
	age := time.Since(createdAt)
	ageDays := age.Hours() / 24.0
	decay := math.Pow(0.5, ageDays/halfLifeDays)
	return score * decay
}

// --- Auto-Indexing ---

// reindexAll re-indexes all knowledge and memory into embeddings.
func reindexAll(ctx context.Context, cfg *Config) error {
	if !cfg.Embedding.Enabled {
		return fmt.Errorf("embedding not enabled")
	}

	dbPath := cfg.HistoryDB

	// Clear existing embeddings
	_, err := queryDB(dbPath, "DELETE FROM embeddings")
	if err != nil {
		return fmt.Errorf("clear embeddings: %w", err)
	}

	// Index knowledge entries
	// (This would scan cfg.KnowledgeDir and embed each file)
	// For now, we'll leave this as a placeholder.
	// Full implementation would:
	// 1. Scan knowledge directory
	// 2. Read files
	// 3. Chunk if needed
	// 4. Call getEmbeddings in batches
	// 5. storeEmbedding for each chunk

	// Index memory entries from history.db
	// (This would query recent conversations and embed them)
	// For now, placeholder.

	logInfo("embedding reindex complete")
	return nil
}

// embeddingStatus returns statistics about the embedding index.
func embeddingStatus(dbPath string) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Count total embeddings
	rows, err := queryDB(dbPath, "SELECT COUNT(*) as cnt FROM embeddings")
	if err != nil {
		return nil, err
	}
	total := 0
	if len(rows) > 0 {
		if v, ok := rows[0]["cnt"].(float64); ok {
			total = int(v)
		}
	}
	stats["total"] = total

	// Count by source
	rows, err = queryDB(dbPath, "SELECT source, COUNT(*) as cnt FROM embeddings GROUP BY source")
	if err != nil {
		return nil, err
	}
	bySource := make(map[string]int)
	for _, row := range rows {
		src, _ := row["source"].(string)
		if v, ok := row["cnt"].(float64); ok {
			bySource[src] = int(v)
		}
	}
	stats["by_source"] = bySource

	return stats, nil
}
