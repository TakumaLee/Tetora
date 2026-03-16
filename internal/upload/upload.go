// Package upload manages file uploads with sanitization and retention.
package upload

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File represents a file uploaded by a user.
type File struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	MimeType   string `json:"mimeType"`
	Source     string `json:"source"`
	UploadedAt string `json:"uploadedAt"`
}

// InitDir ensures the upload directory exists and returns its path.
func InitDir(baseDir string) string {
	dir := filepath.Join(baseDir, "uploads")
	os.MkdirAll(dir, 0o755)
	return dir
}

// Save saves uploaded content to the uploads directory.
func Save(uploadDir, originalName string, reader io.Reader, size int64, source string) (*File, error) {
	safeName := SanitizeFilename(originalName)
	if safeName == "" {
		safeName = "upload"
	}

	ts := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s_%s", ts, safeName)
	fullPath := filepath.Join(uploadDir, filename)

	f, err := os.Create(fullPath)
	if err != nil {
		return nil, fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	written, err := io.Copy(f, reader)
	if err != nil {
		os.Remove(fullPath)
		return nil, fmt.Errorf("write file: %w", err)
	}

	return &File{
		Name:       safeName,
		Path:       fullPath,
		Size:       written,
		MimeType:   DetectMimeType(safeName),
		Source:     source,
		UploadedAt: time.Now().Format(time.RFC3339),
	}, nil
}

// SanitizeFilename removes path separators and unsafe characters.
func SanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.TrimLeft(name, ".")
	var safe []byte
	for _, c := range []byte(name) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' {
			safe = append(safe, c)
		}
	}
	return string(safe)
}

// DetectMimeType guesses MIME type from filename extension.
func DetectMimeType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain"
	case ".md":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".csv":
		return "text/csv"
	case ".xml":
		return "application/xml"
	case ".yaml", ".yml":
		return "text/yaml"
	case ".go":
		return "text/x-go"
	case ".py":
		return "text/x-python"
	case ".js":
		return "text/javascript"
	case ".ts":
		return "text/typescript"
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".zip":
		return "application/zip"
	case ".tar":
		return "application/x-tar"
	case ".gz":
		return "application/gzip"
	default:
		return "application/octet-stream"
	}
}

// BuildPromptPrefix creates the prompt prefix describing attached files.
func BuildPromptPrefix(files []*File) string {
	if len(files) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, "The user has attached the following files:")
	for _, f := range files {
		lines = append(lines, fmt.Sprintf("- %s (%s, %d bytes): %s", f.Name, f.MimeType, f.Size, f.Path))
	}
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

// Cleanup removes upload files older than the given number of days.
func Cleanup(uploadDir string, days int) {
	entries, err := os.ReadDir(uploadDir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(uploadDir, e.Name()))
		}
	}
}

// Coalesce returns the first non-empty string from the arguments.
func Coalesce(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
