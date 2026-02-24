package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
    created_at TEXT NOT NULL,
    content_hash TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_embeddings_source ON embeddings(source);
CREATE INDEX IF NOT EXISTS idx_embeddings_source_id ON embeddings(source, source_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_embeddings_dedup ON embeddings(source, source_id, content_hash);
`
	_, err := queryDB(dbPath, schema)
	if err != nil {
		return err
	}
	// Migration: add content_hash column if missing (tolerate "duplicate column" error).
	if _, err := queryDB(dbPath, `ALTER TABLE embeddings ADD COLUMN content_hash TEXT DEFAULT ''`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			logWarn("embedding migration: add content_hash", "error", err)
		}
	}
	// Migration: add dedup index if missing.
	if _, err := queryDB(dbPath, `CREATE UNIQUE INDEX IF NOT EXISTS idx_embeddings_dedup ON embeddings(source, source_id, content_hash)`); err != nil {
		logWarn("embedding migration: add dedup index", "error", err)
	}
	return nil
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
// Uses INSERT OR REPLACE with content_hash for dedup.
func storeEmbedding(dbPath string, source, sourceID, content string, vec []float32, metadata map[string]interface{}) error {
	metaJSON := "{}"
	if metadata != nil {
		b, _ := json.Marshal(metadata)
		metaJSON = string(b)
	}

	// Compute content hash for dedup.
	contentHash := contentHashSHA256(content)

	blob := serializeVec(vec)
	blobHex := fmt.Sprintf("X'%x'", blob)

	query := fmt.Sprintf(`INSERT OR REPLACE INTO embeddings (source, source_id, content, embedding, metadata, created_at, content_hash)
VALUES ('%s', '%s', '%s', %s, '%s', '%s', '%s')`,
		escapeSQLite(source), escapeSQLite(sourceID), escapeSQLite(content),
		blobHex, escapeSQLite(metaJSON), escapeSQLite(time.Now().UTC().Format(time.RFC3339)),
		escapeSQLite(contentHash))

	_, err := queryDB(dbPath, query)
	if err != nil {
		return err
	}

	return nil
}

// contentHashSHA256 returns the first 16 bytes of SHA-256 as hex.
func contentHashSHA256(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:16])
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
		query += ` WHERE source = '` + escapeSQLite(source) + `'`
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
	// Build a knowledge index and search it, converting results to EmbeddingSearchResult.
	var tfidfResults []EmbeddingSearchResult
	if cfg.KnowledgeDir != "" {
		idx, idxErr := buildKnowledgeIndex(cfg.KnowledgeDir)
		if idxErr == nil && idx != nil {
			kResults := idx.search(query, topK*2)
			for _, kr := range kResults {
				tfidfResults = append(tfidfResults, EmbeddingSearchResult{
					Source:   "knowledge",
					SourceID: kr.Filename,
					Content:  kr.Snippet,
					Score:    kr.Score,
				})
			}
		}
	}

	// 2. If embedding is not enabled, return TF-IDF results only.
	if !cfg.Embedding.Enabled {
		if len(tfidfResults) > topK {
			tfidfResults = tfidfResults[:topK]
		}
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
// If embeddings are not available for candidates, the function falls back to score-based selection.
func mmrRerank(results []EmbeddingSearchResult, queryVec []float32, lambda float64, topK int) []EmbeddingSearchResult {
	if len(results) <= topK {
		return results
	}
	if topK <= 0 {
		return nil
	}

	// Build a map of content -> embedding vector from results.
	// We use content-derived pseudo-vectors: hash the content into a consistent
	// vector space for diversity comparison. If queryVec is available we can also
	// use the original score as a proxy for query-relevance.
	type candidate struct {
		result EmbeddingSearchResult
		vec    []float32
	}

	// Generate pseudo-embeddings from content for diversity computation.
	// This is a lightweight approach that avoids DB round-trips.
	candidates := make([]candidate, len(results))
	for i, r := range results {
		candidates[i] = candidate{
			result: r,
			vec:    contentToVec(r.Content, len(queryVec)),
		}
	}

	// Greedy MMR selection.
	selected := make([]int, 0, topK)
	remaining := make(map[int]bool)
	for i := range candidates {
		remaining[i] = true
	}

	// Step 1: Pick the highest-scoring candidate first.
	bestIdx := 0
	bestScore := -math.MaxFloat64
	for i := range candidates {
		if candidates[i].result.Score > bestScore {
			bestScore = candidates[i].result.Score
			bestIdx = i
		}
	}
	selected = append(selected, bestIdx)
	delete(remaining, bestIdx)

	// Step 2: For each remaining slot, pick the candidate maximizing MMR.
	for len(selected) < topK && len(remaining) > 0 {
		bestMMR := -math.MaxFloat64
		bestCand := -1

		for i := range remaining {
			// Relevance: similarity to query (use normalized score).
			relevance := candidates[i].result.Score

			// Diversity: max similarity to any already-selected result.
			maxSim := -math.MaxFloat64
			candVec := candidates[i].vec
			for _, si := range selected {
				sim := float64(cosineSimilarity(candVec, candidates[si].vec))
				if sim > maxSim {
					maxSim = sim
				}
			}
			if maxSim == -math.MaxFloat64 {
				maxSim = 0
			}

			mmrScore := lambda*relevance - (1-lambda)*maxSim
			if mmrScore > bestMMR {
				bestMMR = mmrScore
				bestCand = i
			}
		}

		if bestCand < 0 {
			break
		}
		selected = append(selected, bestCand)
		delete(remaining, bestCand)
	}

	out := make([]EmbeddingSearchResult, len(selected))
	for i, si := range selected {
		out[i] = candidates[si].result
	}
	return out
}

// contentToVec generates a deterministic pseudo-embedding vector from text content.
// This is used for MMR diversity computation when real embeddings are not available.
// It produces a consistent vector using character-level hashing into buckets.
func contentToVec(content string, dims int) []float32 {
	if dims <= 0 {
		dims = 64 // default small dimension for pseudo-vectors
	}
	vec := make([]float32, dims)
	if content == "" {
		return vec
	}

	// Hash each token into a bucket and accumulate.
	tokens := strings.Fields(strings.ToLower(content))
	for _, tok := range tokens {
		h := sha256.Sum256([]byte(tok))
		// Use first 4 bytes as bucket index, next 4 as sign/magnitude.
		bucket := int(binary.LittleEndian.Uint32(h[0:4])) % dims
		sign := float32(1.0)
		if h[4]&1 == 1 {
			sign = -1.0
		}
		vec[bucket] += sign
	}

	// L2-normalize.
	var norm float32
	for _, v := range vec {
		norm += v * v
	}
	if norm > 0 {
		norm = float32(math.Sqrt(float64(norm)))
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec
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

	// Clear existing embeddings.
	_, err := queryDB(dbPath, "DELETE FROM embeddings")
	if err != nil {
		return fmt.Errorf("clear embeddings: %w", err)
	}

	batchSize := cfg.Embedding.BatchSize
	if batchSize <= 0 {
		batchSize = 20
	}

	var totalIndexed int

	// 1. Index knowledge files.
	knowledgeDir := cfg.KnowledgeDir
	if knowledgeDir != "" {
		dirEntries, rErr := os.ReadDir(knowledgeDir)
		if rErr != nil && !os.IsNotExist(rErr) {
			logWarn("reindex: failed to read knowledge dir", "error", rErr)
		} else if rErr == nil {
			var batch []string
			var batchMeta []struct{ source, sourceID string }
			for _, de := range dirEntries {
				if de.IsDir() || strings.HasPrefix(de.Name(), ".") {
					continue
				}
				fPath := filepath.Join(knowledgeDir, de.Name())
				data, fErr := os.ReadFile(fPath)
				if fErr != nil {
					logWarn("reindex: failed to read knowledge file", "file", de.Name(), "error", fErr)
					continue
				}
				content := string(data)
				// Chunk large files (>4000 chars) into overlapping segments.
				chunks := chunkText(content, 2000, 200)
				for ci, chunk := range chunks {
					sourceID := de.Name()
					if len(chunks) > 1 {
						sourceID = fmt.Sprintf("%s#chunk%d", de.Name(), ci)
					}
					batch = append(batch, chunk)
					batchMeta = append(batchMeta, struct{ source, sourceID string }{"knowledge", sourceID})
					if len(batch) >= batchSize {
						vecs, bErr := getEmbeddings(ctx, cfg, batch)
						if bErr != nil {
							logWarn("reindex knowledge batch failed", "error", bErr)
							batch = batch[:0]
							batchMeta = batchMeta[:0]
							continue
						}
						for i, vec := range vecs {
							if sErr := storeEmbedding(dbPath, batchMeta[i].source, batchMeta[i].sourceID, batch[i], vec, nil); sErr != nil {
								logWarn("reindex knowledge store failed", "sourceID", batchMeta[i].sourceID, "error", sErr)
							} else {
								totalIndexed++
							}
						}
						batch = batch[:0]
						batchMeta = batchMeta[:0]
					}
				}
			}
			// Flush remaining.
			if len(batch) > 0 {
				vecs, bErr := getEmbeddings(ctx, cfg, batch)
				if bErr == nil {
					for i, vec := range vecs {
						if sErr := storeEmbedding(dbPath, batchMeta[i].source, batchMeta[i].sourceID, batch[i], vec, nil); sErr != nil {
							logWarn("reindex knowledge store failed", "sourceID", batchMeta[i].sourceID, "error", sErr)
						} else {
							totalIndexed++
						}
					}
				} else {
					logWarn("reindex knowledge flush batch failed", "error", bErr)
				}
			}
			logInfo("reindex: knowledge files complete", "fileCount", len(dirEntries))
		}
	}

	logInfo("embedding reindex complete", "totalIndexed", totalIndexed)
	return nil
}

// chunkText splits text into chunks of approximately maxChars, with overlap.
// If the text is shorter than maxChars, it is returned as a single chunk.
func chunkText(text string, maxChars, overlap int) []string {
	if len(text) <= maxChars {
		return []string{text}
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxChars {
		overlap = maxChars / 4
	}

	var chunks []string
	step := maxChars - overlap
	if step <= 0 {
		step = maxChars
	}
	for start := 0; start < len(text); start += step {
		end := start + maxChars
		if end > len(text) {
			end = len(text)
		}
		chunks = append(chunks, text[start:end])
		if end == len(text) {
			break
		}
	}
	return chunks
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
