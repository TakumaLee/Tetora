package main

// wire_knowledge.go bridges root callers to internal/knowledge.

import (
	"path/filepath"

	"tetora/internal/knowledge"
)

// --- Type aliases ---

type KnowledgeFile = knowledge.File

// --- Forwarding functions ---

func initKnowledgeDir(baseDir string) string                { return knowledge.InitDir(baseDir) }
func listKnowledgeFiles(dir string) ([]KnowledgeFile, error) { return knowledge.ListFiles(dir) }
func addKnowledgeFile(dir, sourcePath string) error          { return knowledge.AddFile(dir, sourcePath) }
func removeKnowledgeFile(dir, name string) error             { return knowledge.RemoveFile(dir, name) }
func knowledgeDirHasFiles(dir string) bool                   { return knowledge.HasFiles(dir) }
func validateKnowledgeFilename(name string) error            { return knowledge.ValidateFilename(name) }

// knowledgeDir returns the knowledge directory path for a config.
func knowledgeDir(cfg *Config) string {
	if cfg.KnowledgeDir != "" {
		return cfg.KnowledgeDir
	}
	return filepath.Join(cfg.baseDir, "knowledge")
}
