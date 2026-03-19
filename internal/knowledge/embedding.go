package knowledge

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

	"tetora/internal/db"
)

// --- Embedding Config ---

// EmbeddingConfig holds configuration for the embedding provider.
type EmbeddingConfig struct {
	Enabled       bool           `json:"enabled,omitempty"`
	Provider      string         `json:"provider,omitempty"`
	Model         string         `json:"model,omitempty"`
	Endpoint      string         `json:"endpoint,omitempty"`
	APIKey        string         `json:"apiKey,omitempty"`
	Dimensions    int            `json:"dimensions,omitempty"`
	BatchSize     int            `json:"batchSize,omitempty"`
	MMR           MMRConfig      `json:"mmr,omitempty"`
	TemporalDecay TemporalConfig `json:"temporalDecay,omitempty"`
}

// MMRConfig configures Maximal Marginal Relevance reranking.
type MMRConfig struct {
	Enabled bool    `json:"enabled,omitempty"`
	Lambda  float64 `json:"lambda,omitempty"`
}

// TemporalConfig configures temporal decay scoring.
type TemporalConfig struct {
	Enabled      bool    `json:"enabled,omitempty"`
	HalfLifeDays float64 `json:"halfLifeDays,omitempty"`
	MinScore     float64 `json:"minScore,omitempty"` // 0 = disabled, e.g. 0.01
}

func (cfg EmbeddingConfig) MmrLambdaOrDefault() float64 {
	if cfg.MMR.Lambda > 0 {
		return cfg.MMR.Lambda
	}
	return 0.7
}

func (cfg EmbeddingConfig) DecayHalfLifeOrDefault() float64 {
	if cfg.TemporalDecay.HalfLifeDays > 0 {
		return cfg.TemporalDecay.HalfLifeDays
	}
	return 30.0
}

// --- Embedding Database ---

// InitEmbeddingDB creates the embeddings table if it doesn't exist.
func InitEmbeddingDB(dbPath string) error {
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
	if err := db.Exec(dbPath, schema); err != nil {
		return err
	}
	if err := db.Exec(dbPath, `ALTER TABLE embeddings ADD COLUMN content_hash TEXT DEFAULT ''`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			// log but don't fail
			_ = err
		}
	}
	if err := db.Exec(dbPath, `CREATE UNIQUE INDEX IF NOT EXISTS idx_embeddings_dedup ON embeddings(source, source_id, content_hash)`); err != nil {
		_ = err // tolerate "already exists" errors
	}
	return nil
}

// --- Embedding Provider ---

type embeddingRequest struct {
	Input          []string `json:"input"`
	Model          string   `json:"model"`
	EncodingFormat string   `json:"encoding_format,omitempty"`
	Dimensions     int      `json:"dimensions,omitempty"`
}

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

// GetEmbeddings calls the embedding API to get vectors for the given texts.
func GetEmbeddings(ctx context.Context, cfg EmbeddingConfig, texts []string) ([][]float32, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("embedding not enabled")
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/embeddings"
	}

	model := cfg.Model
	if model == "" {
		model = "text-embedding-3-small"
	}

	apiKey := cfg.APIKey
	if apiKey == "" {
		return nil, fmt.Errorf("embedding API key not configured")
	}

	reqBody := embeddingRequest{
		Input: texts,
		Model: model,
	}
	if cfg.Dimensions > 0 {
		reqBody.Dimensions = cfg.Dimensions
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

// GetEmbedding gets a single embedding vector.
func GetEmbedding(ctx context.Context, cfg EmbeddingConfig, text string) ([]float32, error) {
	vecs, err := GetEmbeddings(ctx, cfg, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("empty embedding result")
	}
	return vecs[0], nil
}

// --- Vector Math ---

func CosineSimilarity(a, b []float32) float32 {
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

func SerializeVec(vec []float32) []byte {
	buf := new(bytes.Buffer)
	for _, v := range vec {
		binary.Write(buf, binary.LittleEndian, v)
	}
	return buf.Bytes()
}

func DeserializeVec(data []byte) []float32 {
	count := len(data) / 4
	vec := make([]float32, count)
	reader := bytes.NewReader(data)
	for i := range vec {
		binary.Read(reader, binary.LittleEndian, &vec[i])
	}
	return vec
}

func DeserializeVecFromHex(hexStr string) []float32 {
	if len(hexStr) == 0 {
		return nil
	}
	if len(hexStr) > 2 && (hexStr[0] == 'X' || hexStr[0] == 'x') && hexStr[1] == '\'' {
		hexData := hexStr[2:]
		if len(hexData) > 0 && hexData[len(hexData)-1] == '\'' {
			hexData = hexData[:len(hexData)-1]
		}
		data, err := hex.DecodeString(hexData)
		if err != nil {
			return nil
		}
		return DeserializeVec(data)
	}
	return DeserializeVec([]byte(hexStr))
}

// StoreEmbedding saves an embedding to the database.
func StoreEmbedding(dbPath string, source, sourceID, content string, vec []float32, metadata map[string]interface{}) error {
	metaJSON := "{}"
	if metadata != nil {
		b, _ := json.Marshal(metadata)
		metaJSON = string(b)
	}

	contentHash := ContentHashSHA256(content)

	blob := SerializeVec(vec)
	blobHex := fmt.Sprintf("X'%x'", blob)

	query := fmt.Sprintf(`INSERT OR REPLACE INTO embeddings (source, source_id, content, embedding, metadata, created_at, content_hash)
VALUES ('%s', '%s', '%s', %s, '%s', '%s', '%s')`,
		db.Escape(source), db.Escape(sourceID), db.Escape(content),
		blobHex, db.Escape(metaJSON), db.Escape(time.Now().UTC().Format(time.RFC3339)),
		db.Escape(contentHash))

	return db.Exec(dbPath, query)
}

func ContentHashSHA256(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:16])
}

type EmbeddingRecord struct {
	ID        int
	Source    string
	SourceID  string
	Content   string
	Embedding []float32
	Metadata  map[string]interface{}
	CreatedAt time.Time
}

func LoadEmbeddings(dbPath, source string) ([]EmbeddingRecord, error) {
	query := `SELECT id, source, source_id, content, embedding, metadata, created_at FROM embeddings`
	if source != "" {
		query += ` WHERE source = '` + db.Escape(source) + `'`
	}

	rows, err := db.Query(dbPath, query)
	if err != nil {
		return nil, fmt.Errorf("query embeddings: %w", err)
	}

	var records []EmbeddingRecord
	for _, row := range rows {
		id := db.Int(row["id"])

		embStr := db.Str(row["embedding"])
		vec := DeserializeVecFromHex(embStr)
		if len(vec) == 0 {
			continue
		}

		var meta map[string]interface{}
		if metaStr := db.Str(row["metadata"]); metaStr != "" {
			json.Unmarshal([]byte(metaStr), &meta)
		}

		createdAt, _ := time.Parse(time.RFC3339, db.Str(row["created_at"]))

		records = append(records, EmbeddingRecord{
			ID:        id,
			Source:    db.Str(row["source"]),
			SourceID:  db.Str(row["source_id"]),
			Content:   db.Str(row["content"]),
			Embedding: vec,
			Metadata:  meta,
			CreatedAt: createdAt,
		})
	}

	return records, nil
}

// --- Search ---

// EmbeddingSearchResult represents a search result from hybrid search.
type EmbeddingSearchResult struct {
	Source    string                 `json:"source"`
	SourceID  string                 `json:"sourceId"`
	Content   string                 `json:"content"`
	Score     float64                `json:"score"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt string                 `json:"createdAt,omitempty"`
}

// VectorSearch finds the top-K most similar embeddings to the query vector.
func VectorSearch(dbPath string, queryVec []float32, source string, topK int) ([]EmbeddingSearchResult, error) {
	records, err := LoadEmbeddings(dbPath, source)
	if err != nil {
		return nil, err
	}

	type scored struct {
		result     EmbeddingSearchResult
		similarity float32
	}

	var candidates []scored
	for _, rec := range records {
		sim := CosineSimilarity(queryVec, rec.Embedding)
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

// HybridSearch combines TF-IDF and vector search using Reciprocal Rank Fusion (RRF).
func HybridSearch(ctx context.Context, cfg EmbeddingConfig, dbPath, knowledgeDir, query, source string, topK int) ([]EmbeddingSearchResult, error) {
	var tfidfResults []EmbeddingSearchResult
	if knowledgeDir != "" {
		idx, idxErr := BuildIndex(knowledgeDir)
		if idxErr == nil && idx != nil {
			kResults := idx.Search(query, topK*2)
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

	if !cfg.Enabled {
		if len(tfidfResults) > topK {
			tfidfResults = tfidfResults[:topK]
		}
		return tfidfResults, nil
	}

	queryVec, err := GetEmbedding(ctx, cfg, query)
	if err != nil {
		return tfidfResults, nil
	}

	vecResults, err := VectorSearch(dbPath, queryVec, source, topK*2)
	if err != nil {
		return tfidfResults, nil
	}

	if len(tfidfResults) == 0 {
		merged := vecResults

		if cfg.TemporalDecay.Enabled {
			halfLife := cfg.DecayHalfLifeOrDefault()
			for i := range merged {
				if merged[i].CreatedAt != "" {
					if createdAt, err := time.Parse(time.RFC3339, merged[i].CreatedAt); err == nil {
						merged[i].Score = TemporalDecay(merged[i].Score, createdAt, halfLife)
					}
				}
			}
			for i := 0; i < len(merged)-1; i++ {
				for j := i + 1; j < len(merged); j++ {
					if merged[j].Score > merged[i].Score {
						merged[i], merged[j] = merged[j], merged[i]
					}
				}
			}
		}

		if cfg.MMR.Enabled && len(merged) > topK {
			merged = MMRRerank(merged, queryVec, cfg.MmrLambdaOrDefault(), topK)
		}

		if topK > len(merged) {
			topK = len(merged)
		}
		if topK <= 0 {
			return []EmbeddingSearchResult{}, nil
		}
		return merged[:topK], nil
	}

	merged := RRFMerge(tfidfResults, vecResults, 60)

	if cfg.TemporalDecay.Enabled {
		halfLife := cfg.DecayHalfLifeOrDefault()
		for i := range merged {
			if merged[i].CreatedAt != "" {
				if createdAt, err := time.Parse(time.RFC3339, merged[i].CreatedAt); err == nil {
					merged[i].Score = TemporalDecay(merged[i].Score, createdAt, halfLife)
				}
			}
		}
		for i := 0; i < len(merged)-1; i++ {
			for j := i + 1; j < len(merged); j++ {
				if merged[j].Score > merged[i].Score {
					merged[i], merged[j] = merged[j], merged[i]
				}
			}
		}
	}

	if cfg.MMR.Enabled && len(merged) > topK {
		merged = MMRRerank(merged, queryVec, cfg.MmrLambdaOrDefault(), topK)
	}

	if topK > len(merged) {
		topK = len(merged)
	}
	if topK <= 0 {
		return []EmbeddingSearchResult{}, nil
	}
	return merged[:topK], nil
}

func RRFMerge(listA, listB []EmbeddingSearchResult, k int) []EmbeddingSearchResult {
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

	var merged []EmbeddingSearchResult
	for key, r := range results {
		r.Score = scores[key]
		merged = append(merged, r)
	}

	for i := 0; i < len(merged)-1; i++ {
		for j := i + 1; j < len(merged); j++ {
			if merged[j].Score > merged[i].Score {
				merged[i], merged[j] = merged[j], merged[i]
			}
		}
	}

	return merged
}

func MMRRerank(results []EmbeddingSearchResult, queryVec []float32, lambda float64, topK int) []EmbeddingSearchResult {
	if len(results) <= topK {
		return results
	}
	if topK <= 0 {
		return nil
	}

	type candidate struct {
		result EmbeddingSearchResult
		vec    []float32
	}

	candidates := make([]candidate, len(results))
	for i, r := range results {
		candidates[i] = candidate{
			result: r,
			vec:    ContentToVec(r.Content, len(queryVec)),
		}
	}

	selected := make([]int, 0, topK)
	remaining := make(map[int]bool)
	for i := range candidates {
		remaining[i] = true
	}

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

	for len(selected) < topK && len(remaining) > 0 {
		bestMMR := -math.MaxFloat64
		bestCand := -1

		for i := range remaining {
			relevance := candidates[i].result.Score

			maxSim := -math.MaxFloat64
			candVec := candidates[i].vec
			for _, si := range selected {
				sim := float64(CosineSimilarity(candVec, candidates[si].vec))
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

func ContentToVec(content string, dims int) []float32 {
	if dims <= 0 {
		dims = 64
	}
	vec := make([]float32, dims)
	if content == "" {
		return vec
	}

	tokens := strings.Fields(strings.ToLower(content))
	for _, tok := range tokens {
		h := sha256.Sum256([]byte(tok))
		bucket := int(binary.LittleEndian.Uint32(h[0:4])) % dims
		sign := float32(1.0)
		if h[4]&1 == 1 {
			sign = -1.0
		}
		vec[bucket] += sign
	}

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

func TemporalDecay(score float64, createdAt time.Time, halfLifeDays float64) float64 {
	age := time.Since(createdAt)
	ageDays := age.Hours() / 24.0
	decay := math.Pow(0.5, ageDays/halfLifeDays)
	return score * decay
}

// ReindexAll re-indexes all knowledge into embeddings.
func ReindexAll(ctx context.Context, cfg EmbeddingConfig, dbPath, knowledgeDir string) error {
	if !cfg.Enabled {
		return fmt.Errorf("embedding not enabled")
	}

	if err := db.Exec(dbPath, "DELETE FROM embeddings"); err != nil {
		return fmt.Errorf("clear embeddings: %w", err)
	}

	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 20
	}

	var totalIndexed int

	if knowledgeDir != "" {
		dirEntries, rErr := os.ReadDir(knowledgeDir)
		if rErr == nil {
			var batch []string
			var batchMeta []struct{ source, sourceID string }
			for _, de := range dirEntries {
				if de.IsDir() || strings.HasPrefix(de.Name(), ".") {
					continue
				}
				fPath := filepath.Join(knowledgeDir, de.Name())
				data, fErr := os.ReadFile(fPath)
				if fErr != nil {
					continue
				}
				content := string(data)
				chunks := ChunkText(content, 2000, 200)
				for ci, chunk := range chunks {
					sourceID := de.Name()
					if len(chunks) > 1 {
						sourceID = fmt.Sprintf("%s#chunk%d", de.Name(), ci)
					}
					batch = append(batch, chunk)
					batchMeta = append(batchMeta, struct{ source, sourceID string }{"knowledge", sourceID})
					if len(batch) >= batchSize {
						vecs, bErr := GetEmbeddings(ctx, cfg, batch)
						if bErr == nil {
							for i, vec := range vecs {
								if sErr := StoreEmbedding(dbPath, batchMeta[i].source, batchMeta[i].sourceID, batch[i], vec, nil); sErr == nil {
									totalIndexed++
								}
							}
						}
						batch = batch[:0]
						batchMeta = batchMeta[:0]
					}
				}
			}
			if len(batch) > 0 {
				vecs, bErr := GetEmbeddings(ctx, cfg, batch)
				if bErr == nil {
					for i, vec := range vecs {
						if sErr := StoreEmbedding(dbPath, batchMeta[i].source, batchMeta[i].sourceID, batch[i], vec, nil); sErr == nil {
							totalIndexed++
						}
					}
				}
			}
		}
	}

	_ = totalIndexed
	return nil
}

func ChunkText(text string, maxChars, overlap int) []string {
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

// EmbeddingStatus returns statistics about the embedding index.
func EmbeddingStatus(dbPath string) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	rows, err := db.Query(dbPath, "SELECT COUNT(*) as cnt FROM embeddings")
	if err != nil {
		return nil, err
	}
	total := 0
	if len(rows) > 0 {
		total = db.Int(rows[0]["cnt"])
	}
	stats["total"] = total

	rows, err = db.Query(dbPath, "SELECT source, COUNT(*) as cnt FROM embeddings GROUP BY source")
	if err != nil {
		return nil, err
	}
	bySource := make(map[string]int)
	for _, row := range rows {
		src := db.Str(row["source"])
		bySource[src] = db.Int(row["cnt"])
	}
	stats["by_source"] = bySource

	return stats, nil
}
