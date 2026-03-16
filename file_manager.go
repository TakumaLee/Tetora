package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tetora/internal/storage"
)

// --- P23.3: File & Document Processing ---

// FileManagerConfig configures the file management system.
type FileManagerConfig struct {
	Enabled    bool   `json:"enabled"`
	StorageDir string `json:"storageDir,omitempty"` // default: {baseDir}/files
	MaxSizeMB  int    `json:"maxSizeMB,omitempty"`  // default: 50
}

func (c FileManagerConfig) storageDirOrDefault(baseDir string) string {
	if c.StorageDir != "" {
		return c.StorageDir
	}
	return filepath.Join(baseDir, "files")
}

func (c FileManagerConfig) maxSizeOrDefault() int {
	if c.MaxSizeMB > 0 {
		return c.MaxSizeMB
	}
	return 50
}

// globalFileManager is exposed for tool handlers.
var globalFileManager *storage.Service

// newFileManagerService constructs a storage.Service from Config.
func newFileManagerService(cfg *Config) *storage.Service {
	dir := cfg.FileManager.storageDirOrDefault(cfg.baseDir)
	return storage.New(cfg.HistoryDB, dir, cfg.FileManager.maxSizeOrDefault(), makeLifeDB(), newUUID)
}

// --- Tool Handlers ---

// toolPdfRead extracts text from a PDF file (by ID or path).
func toolPdfRead(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		FileID   string `json:"file_id"`
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	app := appFromCtx(ctx)
	if app == nil || app.FileManager == nil {
		return "", fmt.Errorf("file manager not enabled")
	}
	svc := app.FileManager

	var pdfPath string
	if args.FileID != "" {
		mf, err := svc.GetFile(args.FileID)
		if err != nil {
			return "", err
		}
		pdfPath = mf.StoragePath
	} else if args.FilePath != "" {
		pdfPath = args.FilePath
	} else {
		return "", fmt.Errorf("file_id or file_path is required")
	}

	text, err := svc.ExtractPDF(pdfPath)
	if err != nil {
		return "", err
	}
	if len(text) > 50000 {
		text = text[:50000] + "\n... (truncated)"
	}
	return fmt.Sprintf("PDF text extracted (%d chars):\n\n%s", len(text), text), nil
}

// toolDocSummarize reads a document and returns a structured summary.
func toolDocSummarize(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		FileID   string `json:"file_id"`
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	app := appFromCtx(ctx)
	if app == nil || app.FileManager == nil {
		return "", fmt.Errorf("file manager not enabled")
	}
	svc := app.FileManager

	var content string
	var filename string
	var mimeType string

	if args.FileID != "" {
		mf, err := svc.GetFile(args.FileID)
		if err != nil {
			return "", err
		}
		filename = mf.OriginalName
		mimeType = mf.MimeType
		if mf.MimeType == "application/pdf" {
			text, err := svc.ExtractPDF(mf.StoragePath)
			if err != nil {
				return "", fmt.Errorf("extract pdf: %w", err)
			}
			content = text
		} else {
			data, err := os.ReadFile(mf.StoragePath)
			if err != nil {
				return "", fmt.Errorf("read file: %w", err)
			}
			content = string(data)
		}
	} else if args.FilePath != "" {
		filename = filepath.Base(args.FilePath)
		mimeType = storage.MimeFromExt(filename)
		if mimeType == "application/pdf" {
			text, err := svc.ExtractPDF(args.FilePath)
			if err != nil {
				return "", fmt.Errorf("extract pdf: %w", err)
			}
			content = text
		} else {
			data, err := os.ReadFile(args.FilePath)
			if err != nil {
				return "", fmt.Errorf("read file: %w", err)
			}
			content = string(data)
		}
	} else {
		return "", fmt.Errorf("file_id or file_path is required")
	}

	if len(content) > 100000 {
		content = content[:100000]
	}

	lines := strings.Split(content, "\n")
	wordCount := 0
	for _, line := range lines {
		wordCount += len(strings.Fields(line))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Document: %s\n", filename))
	sb.WriteString(fmt.Sprintf("Type: %s\n", mimeType))
	sb.WriteString(fmt.Sprintf("Lines: %d\n", len(lines)))
	sb.WriteString(fmt.Sprintf("Words: ~%d\n", wordCount))
	sb.WriteString(fmt.Sprintf("Characters: %d\n\n", len(content)))

	previewLines := 20
	if len(lines) < previewLines {
		previewLines = len(lines)
	}
	sb.WriteString("Preview (first lines):\n")
	for i := 0; i < previewLines; i++ {
		sb.WriteString(lines[i])
		sb.WriteString("\n")
	}
	if len(lines) > previewLines {
		sb.WriteString(fmt.Sprintf("... (%d more lines)\n", len(lines)-previewLines))
	}

	return sb.String(), nil
}

// toolFileOrganize moves a file to a new category.
func toolFileOrganize(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		FileID   string `json:"file_id"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.FileID == "" {
		return "", fmt.Errorf("file_id is required")
	}
	if args.Category == "" {
		return "", fmt.Errorf("category is required")
	}

	app := appFromCtx(ctx)
	if app == nil || app.FileManager == nil {
		return "", fmt.Errorf("file manager not enabled")
	}
	svc := app.FileManager

	mf, err := svc.OrganizeFile(args.FileID, args.Category)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(mf, "", "  ")
	return fmt.Sprintf("File organized to category '%s':\n%s", args.Category, string(out)), nil
}

// toolFileList lists managed files.
func toolFileList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Category string `json:"category"`
		UserID   string `json:"user_id"`
		Limit    int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	app := appFromCtx(ctx)
	if app == nil || app.FileManager == nil {
		return "", fmt.Errorf("file manager not enabled")
	}
	svc := app.FileManager

	files, err := svc.ListFiles(args.Category, args.UserID, args.Limit)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "No files found.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Files (%d):\n\n", len(files)))
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("- %s | %s | %s | %s | %d bytes | %s\n",
			f.ID[:8], f.OriginalName, f.Category, f.MimeType, f.FileSize, f.CreatedAt))
	}
	return sb.String(), nil
}

// toolFileDuplicates finds duplicate files by content hash.
func toolFileDuplicates(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.FileManager == nil {
		return "", fmt.Errorf("file manager not enabled")
	}
	svc := app.FileManager

	groups, err := svc.FindDuplicates()
	if err != nil {
		return "", err
	}
	if len(groups) == 0 {
		return "No duplicate files found.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d duplicate groups:\n\n", len(groups)))
	for i, group := range groups {
		sb.WriteString(fmt.Sprintf("Group %d (hash: %s, %d files):\n", i+1, group[0].ContentHash[:16], len(group)))
		for _, f := range group {
			sb.WriteString(fmt.Sprintf("  - %s | %s | %s | %d bytes\n", f.ID[:8], f.OriginalName, f.Category, f.FileSize))
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// toolFileStore stores file content (base64 or text) into the file manager.
func toolFileStore(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Filename string `json:"filename"`
		Content  string `json:"content"`
		Base64   string `json:"base64"`
		Category string `json:"category"`
		UserID   string `json:"user_id"`
		Source   string `json:"source"`
		SourceID string `json:"source_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Filename == "" {
		return "", fmt.Errorf("filename is required")
	}

	app := appFromCtx(ctx)
	if app == nil || app.FileManager == nil {
		return "", fmt.Errorf("file manager not enabled")
	}
	svc := app.FileManager

	var data []byte
	if args.Base64 != "" {
		var err error
		data, err = base64.StdEncoding.DecodeString(args.Base64)
		if err != nil {
			return "", fmt.Errorf("invalid base64: %w", err)
		}
	} else if args.Content != "" {
		data = []byte(args.Content)
	} else {
		return "", fmt.Errorf("content or base64 is required")
	}

	mf, isDup, err := svc.StoreFile(args.UserID, args.Filename, args.Category, args.Source, args.SourceID, data)
	if err != nil {
		return "", err
	}

	status := "stored"
	if isDup {
		status = "duplicate (existing file returned)"
	}
	out, _ := json.MarshalIndent(mf, "", "  ")
	return fmt.Sprintf("File %s (%s):\n%s", status, args.Filename, string(out)), nil
}
