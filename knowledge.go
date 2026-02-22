package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// KnowledgeFile represents a file in the knowledge base directory.
type KnowledgeFile struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

// initKnowledgeDir creates the knowledge base directory if it does not exist
// and returns the absolute path.
func initKnowledgeDir(baseDir string) string {
	dir := filepath.Join(baseDir, "knowledge")
	os.MkdirAll(dir, 0o755)
	return dir
}

// knowledgeDir returns the knowledge directory path for a config.
// Uses config.KnowledgeDir if set, otherwise defaults to baseDir/knowledge/.
func knowledgeDir(cfg *Config) string {
	if cfg.KnowledgeDir != "" {
		return cfg.KnowledgeDir
	}
	return filepath.Join(cfg.baseDir, "knowledge")
}

// listKnowledgeFiles lists all files in the knowledge directory.
// Returns name, size, and modification time for each file.
func listKnowledgeFiles(knowledgeDir string) ([]KnowledgeFile, error) {
	entries, err := os.ReadDir(knowledgeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read knowledge dir: %w", err)
	}

	var files []KnowledgeFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Skip hidden files.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, KnowledgeFile{
			Name:    e.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
		})
	}
	return files, nil
}

// addKnowledgeFile copies a file from sourcePath into the knowledge directory.
// The filename is preserved from the source. Returns an error if the source
// does not exist or the filename is unsafe.
func addKnowledgeFile(knowledgeDir, sourcePath string) error {
	// Validate source exists.
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("source file: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("source is a directory, not a file")
	}

	name := filepath.Base(sourcePath)
	if err := validateKnowledgeFilename(name); err != nil {
		return err
	}

	// Ensure knowledge dir exists.
	os.MkdirAll(knowledgeDir, 0o755)

	// Copy file.
	src, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	dstPath := filepath.Join(knowledgeDir, name)
	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(dstPath)
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

// removeKnowledgeFile removes a file from the knowledge directory.
// Validates that the filename is safe (no path traversal).
func removeKnowledgeFile(knowledgeDir, name string) error {
	if err := validateKnowledgeFilename(name); err != nil {
		return err
	}

	path := filepath.Join(knowledgeDir, name)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file %q not found in knowledge base", name)
		}
		return err
	}
	return os.Remove(path)
}

// knowledgeDirHasFiles returns true if the knowledge directory exists and
// contains at least one non-hidden file.
func knowledgeDirHasFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			return true
		}
	}
	return false
}

// validateKnowledgeFilename checks that a filename is safe:
// no path separators, no path traversal, no hidden files.
func validateKnowledgeFilename(name string) error {
	if name == "" {
		return fmt.Errorf("filename is empty")
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("hidden files not allowed: %q", name)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("path separators not allowed in filename: %q", name)
	}
	if name == ".." || name == "." {
		return fmt.Errorf("invalid filename: %q", name)
	}
	// Check that cleaned name equals original (no traversal).
	if filepath.Clean(name) != name {
		return fmt.Errorf("unsafe filename: %q", name)
	}
	return nil
}
