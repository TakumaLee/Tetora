package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// backupIncludes lists the files and directory prefixes to include in backups.
// Exact files are matched by name; directories are matched by prefix.
var backupIncludeFiles = []string{
	"config.json",
	"jobs.json",
	"history.db",
}

var backupIncludeDirs = []string{
	"prompts",
	"knowledge",
	"souls",
}

// backupExcludeDirs lists directories that should never be included.
var backupExcludeDirs = []string{
	"bin",
	"outputs",
	"mcp",
	"logs",
	"backups",
}

// shouldInclude determines whether a file path (relative to baseDir)
// should be included in the backup.
func shouldInclude(relPath string) bool {
	// Skip hidden files and directories (except config files).
	base := filepath.Base(relPath)
	if strings.HasPrefix(base, ".") && relPath != base {
		return false
	}

	// Skip tar.gz files.
	if strings.HasSuffix(relPath, ".tar.gz") {
		return false
	}

	// Skip config backups.
	if strings.Contains(relPath, ".backup.") {
		return false
	}

	// Check excluded directories.
	parts := strings.SplitN(relPath, string(filepath.Separator), 2)
	topDir := parts[0]
	for _, excl := range backupExcludeDirs {
		if topDir == excl {
			return false
		}
	}

	// Include exact files.
	for _, f := range backupIncludeFiles {
		if relPath == f {
			return true
		}
	}

	// Include files under included directories.
	for _, d := range backupIncludeDirs {
		if topDir == d {
			return true
		}
	}

	// Include soul files at top level (SOUL*.md).
	if filepath.Dir(relPath) == "." && strings.HasPrefix(base, "SOUL") && strings.HasSuffix(base, ".md") {
		return true
	}

	return false
}

// createBackup creates a tar.gz archive of the essential tetora files
// from baseDir and writes it to outputPath.
func createBackup(baseDir, outputPath string) error {
	// Ensure output directory exists.
	os.MkdirAll(filepath.Dir(outputPath), 0o755)

	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	gw := gzip.NewWriter(outFile)
	defer gw.Close()

	// Set gzip header with timestamp.
	gw.Comment = fmt.Sprintf("tetora backup %s", time.Now().Format(time.RFC3339))

	tw := tar.NewWriter(gw)
	defer tw.Close()

	fileCount := 0

	err = filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip files we can't read
		}

		relPath, err := filepath.Rel(baseDir, path)
		if err != nil {
			return nil
		}

		// Skip the base dir itself.
		if relPath == "." {
			return nil
		}

		// Skip excluded directories entirely.
		if info.IsDir() {
			parts := strings.SplitN(relPath, string(filepath.Separator), 2)
			topDir := parts[0]
			for _, excl := range backupExcludeDirs {
				if topDir == excl {
					return filepath.SkipDir
				}
			}
			return nil
		}

		if !shouldInclude(relPath) {
			return nil
		}

		// Read file.
		data, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}

		header := &tar.Header{
			Name:    relPath,
			Size:    int64(len(data)),
			Mode:    int64(info.Mode()),
			ModTime: info.ModTime(),
		}

		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("write tar header %s: %w", relPath, err)
		}
		if _, err := tw.Write(data); err != nil {
			return fmt.Errorf("write tar data %s: %w", relPath, err)
		}

		fileCount++
		return nil
	})

	if err != nil {
		// Clean up partial file on error.
		outFile.Close()
		os.Remove(outputPath)
		return err
	}

	if fileCount == 0 {
		tw.Close()
		gw.Close()
		outFile.Close()
		os.Remove(outputPath)
		return fmt.Errorf("no files to backup in %s", baseDir)
	}

	return nil
}

// restoreBackup extracts a tar.gz backup archive to the target directory.
// It validates all tar entries against path traversal before extracting.
// A backup of the current state is created before restoration.
func restoreBackup(backupPath, targetDir string) error {
	// Validate the backup first.
	entries, err := listBackupContents(backupPath)
	if err != nil {
		return fmt.Errorf("validate backup: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("backup is empty")
	}

	// Check for path traversal in all entries before extracting.
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("resolve target dir: %w", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry, "..") {
			return fmt.Errorf("path traversal detected in backup entry: %s", entry)
		}
		resolved := filepath.Join(absTarget, entry)
		if !strings.HasPrefix(resolved, absTarget+string(filepath.Separator)) && resolved != absTarget {
			return fmt.Errorf("path traversal detected: %s resolves outside target", entry)
		}
	}

	// Create backup of current state before restoring.
	backupDir := filepath.Join(targetDir, "backups")
	os.MkdirAll(backupDir, 0o755)
	preRestoreBackup := filepath.Join(backupDir,
		fmt.Sprintf("pre-restore-%s.tar.gz", time.Now().Format("20060102-150405")))
	if err := createBackup(targetDir, preRestoreBackup); err != nil {
		// Non-fatal — warn but continue.
		fmt.Fprintf(os.Stderr, "Warning: could not create pre-restore backup: %v\n", err)
	}

	// Open and extract the backup.
	f, err := os.Open(backupPath)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	extracted := 0
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		// Only handle regular files.
		if header.Typeflag != tar.TypeReg {
			continue
		}

		// Final path traversal check on each entry.
		target := filepath.Join(absTarget, header.Name)
		if !strings.HasPrefix(target, absTarget+string(filepath.Separator)) {
			return fmt.Errorf("path traversal in entry: %s", header.Name)
		}

		// Create parent directories.
		os.MkdirAll(filepath.Dir(target), 0o755)

		// Write file.
		outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
		if err != nil {
			return fmt.Errorf("create file %s: %w", header.Name, err)
		}

		// Limit read size to prevent decompression bombs (100MB per file).
		limited := io.LimitReader(tr, 100*1024*1024)
		if _, err := io.Copy(outFile, limited); err != nil {
			outFile.Close()
			return fmt.Errorf("write file %s: %w", header.Name, err)
		}
		outFile.Close()

		extracted++
	}

	if extracted == 0 {
		return fmt.Errorf("no files extracted from backup")
	}

	return nil
}

// listBackupContents lists the file entries in a backup without extracting.
func listBackupContents(backupPath string) ([]string, error) {
	f, err := os.Open(backupPath)
	if err != nil {
		return nil, fmt.Errorf("open backup: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	var entries []string
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return entries, fmt.Errorf("read tar entry: %w", err)
		}
		entries = append(entries, header.Name)
	}

	return entries, nil
}
