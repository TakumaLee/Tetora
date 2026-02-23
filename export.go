package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// --- P23.7: GDPR Data Export ---

// ExportResult describes the result of a data export operation.
type ExportResult struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"sizeBytes"`
	Tables    int    `json:"tables"`
	CreatedAt string `json:"createdAt"`
}

// exportUserData creates a ZIP archive containing all user data from the database.
// If userID is non-empty, tables with a user_id column are filtered.
func exportUserData(cfg *Config, userID string) (*ExportResult, error) {
	if cfg.HistoryDB == "" {
		return nil, fmt.Errorf("historyDB not configured")
	}

	// Tables to export with optional user_id filter column.
	tables := []struct {
		Name         string
		FilterColumn string // empty = export all rows
	}{
		{Name: "agent_memory", FilterColumn: ""},
		{Name: "unified_memory", FilterColumn: ""},
		{Name: "embeddings", FilterColumn: ""},
		{Name: "reflections", FilterColumn: "role"},
		{Name: "sessions", FilterColumn: ""},
		{Name: "history", FilterColumn: ""},
		{Name: "reminders", FilterColumn: "user_id"},
		{Name: "message_queue", FilterColumn: ""},
		{Name: "channel_status", FilterColumn: ""},
		{Name: "backup_log", FilterColumn: ""},
		{Name: "skill_usage", FilterColumn: ""},
		{Name: "trust_events", FilterColumn: ""},
	}

	// Create temp directory.
	tmpDir, err := os.MkdirTemp("", "tetora-export-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Export each table.
	manifest := map[string]any{
		"exportTimestamp": time.Now().UTC().Format(time.RFC3339),
		"userID":          userID,
		"tables":          []any{},
	}
	tableInfos := []any{}
	exportedTables := 0

	for _, tbl := range tables {
		// Check if table exists.
		existsRows, err := queryDB(cfg.HistoryDB,
			fmt.Sprintf("SELECT name FROM sqlite_master WHERE type='table' AND name='%s'", escapeSQLite(tbl.Name)))
		if err != nil || len(existsRows) == 0 {
			continue // Skip non-existent tables.
		}

		// Build query.
		query := fmt.Sprintf("SELECT * FROM %s", tbl.Name)
		if userID != "" && tbl.FilterColumn != "" {
			query += fmt.Sprintf(" WHERE %s = '%s'", tbl.FilterColumn, escapeSQLite(userID))
		}

		rows, err := queryDB(cfg.HistoryDB, query)
		if err != nil {
			logWarn("export: query failed for table", "table", tbl.Name, "error", err)
			continue
		}

		// Write table data as JSON.
		data, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			logWarn("export: marshal failed for table", "table", tbl.Name, "error", err)
			continue
		}

		outPath := filepath.Join(tmpDir, tbl.Name+".json")
		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			logWarn("export: write failed for table", "table", tbl.Name, "error", err)
			continue
		}

		exportedTables++
		tableInfos = append(tableInfos, map[string]any{
			"name":     tbl.Name,
			"rowCount": len(rows),
			"bytes":    len(data),
		})
	}

	manifest["tables"] = tableInfos
	manifest["tableCount"] = exportedTables

	// Write manifest.
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestData, 0o644); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	// Create exports directory.
	exportsDir := filepath.Join(cfg.baseDir, "exports")
	if err := os.MkdirAll(exportsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create exports dir: %w", err)
	}

	// Create ZIP archive.
	ts := time.Now().UTC().Format("20060102-150405")
	zipName := fmt.Sprintf("%s_export.zip", ts)
	zipPath := filepath.Join(exportsDir, zipName)

	if err := createZipFromDir(tmpDir, zipPath); err != nil {
		return nil, fmt.Errorf("create zip: %w", err)
	}

	// Get zip size.
	info, err := os.Stat(zipPath)
	if err != nil {
		return nil, fmt.Errorf("stat zip: %w", err)
	}

	result := &ExportResult{
		Filename:  zipPath,
		SizeBytes: info.Size(),
		Tables:    exportedTables,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	logInfo("data export complete", "filename", zipName, "tables", exportedTables, "sizeBytes", info.Size())
	return result, nil
}

// createZipFromDir creates a ZIP archive from all files in srcDir.
func createZipFromDir(srcDir, destPath string) error {
	outFile, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	w := zip.NewWriter(outFile)
	defer w.Close()

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filePath := filepath.Join(srcDir, entry.Name())
		srcFile, err := os.Open(filePath)
		if err != nil {
			return err
		}

		info, err := srcFile.Stat()
		if err != nil {
			srcFile.Close()
			return err
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			srcFile.Close()
			return err
		}
		header.Name = entry.Name()
		header.Method = zip.Deflate

		writer, err := w.CreateHeader(header)
		if err != nil {
			srcFile.Close()
			return err
		}

		if _, err := io.Copy(writer, srcFile); err != nil {
			srcFile.Close()
			return err
		}
		srcFile.Close()
	}

	return nil
}
