package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// toolNoteDedup scans the notes vault and reports/removes duplicate notes.
func toolNoteDedup(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		AutoDelete bool   `json:"auto_delete"`
		Prefix     string `json:"prefix"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	vaultPath := svc.vaultPath

	// Walk vault and compute hashes.
	type fileHash struct {
		Path string
		Hash string
		Size int64
	}
	var files []fileHash
	hashMap := make(map[string][]string) // hash -> paths

	filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		if args.Prefix != "" {
			rel, _ := filepath.Rel(vaultPath, path)
			if !strings.HasPrefix(rel, args.Prefix) {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		h := sha256.Sum256(data)
		hash := hex.EncodeToString(h[:16])
		rel, _ := filepath.Rel(vaultPath, path)
		files = append(files, fileHash{Path: rel, Hash: hash, Size: info.Size()})
		hashMap[hash] = append(hashMap[hash], rel)
		return nil
	})

	var duplicates []map[string]any
	deleted := 0
	for hash, paths := range hashMap {
		if len(paths) <= 1 {
			continue
		}
		if args.AutoDelete {
			// Keep the first, delete the rest.
			for _, p := range paths[1:] {
				fullPath := filepath.Join(vaultPath, p)
				if err := os.Remove(fullPath); err == nil {
					deleted++
				}
			}
		}
		duplicates = append(duplicates, map[string]any{
			"hash":  hash,
			"files": paths,
			"count": len(paths),
		})
	}

	result := map[string]any{
		"total_files":      len(files),
		"duplicate_groups": len(duplicates),
		"duplicates":       duplicates,
	}
	if args.AutoDelete {
		result["deleted"] = deleted
	}

	b, _ := json.Marshal(result)
	logInfoCtx(ctx, "note dedup scan complete", "total_files", len(files), "duplicate_groups", len(duplicates))
	return string(b), nil
}

// toolSourceAudit compares expected sources against actual notes.
func toolSourceAudit(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Expected []string `json:"expected"`
		Prefix   string   `json:"prefix"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	vaultPath := svc.vaultPath
	prefix := args.Prefix
	if prefix == "" {
		prefix = "."
	}

	// Collect actual notes.
	actualSet := make(map[string]bool)
	scanDir := filepath.Join(vaultPath, prefix)
	filepath.Walk(scanDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(vaultPath, path)
		actualSet[rel] = true
		return nil
	})

	// Compare.
	expectedSet := make(map[string]bool)
	for _, e := range args.Expected {
		expectedSet[e] = true
	}

	var missing, extra []string
	for e := range expectedSet {
		if !actualSet[e] {
			missing = append(missing, e)
		}
	}
	for a := range actualSet {
		if !expectedSet[a] {
			extra = append(extra, a)
		}
	}

	result := map[string]any{
		"expected_count": len(args.Expected),
		"actual_count":   len(actualSet),
		"missing_count":  len(missing),
		"extra_count":    len(extra),
		"missing":        missing,
		"extra":          extra,
	}
	b, _ := json.Marshal(result)
	logInfoCtx(ctx, "source audit complete", "expected", len(args.Expected), "actual", len(actualSet))
	return string(b), nil
}
