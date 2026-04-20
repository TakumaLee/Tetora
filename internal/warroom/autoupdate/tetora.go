package autoupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/config"
	"tetora/internal/log"
)

// resolveTetoraProjectPath returns the Tetora source directory. Resolution order:
//  1. TETORA_PROJECT_PATH env var
//  2. $HOME/Workspace/Projects/01-Personal/tetora (user's default layout)
func resolveTetoraProjectPath() string {
	if p := os.Getenv("TETORA_PROJECT_PATH"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Workspace", "Projects", "01-Personal", "tetora")
}

func updateTetora(ctx context.Context, cfg *config.Config, front json.RawMessage) (map[string]any, error) {
	return updateTetoraAt(ctx, resolveTetoraProjectPath())
}

// updateTetoraAt is the testable inner implementation that accepts a project path.
func updateTetoraAt(ctx context.Context, projectPath string) (map[string]any, error) {
	if _, err := os.Stat(projectPath); err != nil {
		log.Warn("tetora autoupdate: project dir missing", "path", projectPath)
		return nil, nil
	}

	branch, err := gitOutput(ctx, projectPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		log.Warn("tetora autoupdate: git rev-parse failed", "err", err)
		return nil, nil
	}

	subject, err := gitOutput(ctx, projectPath, "log", "-1", "--pretty=format:%s")
	if err != nil {
		log.Warn("tetora autoupdate: git log failed", "err", err)
		subject = "(unknown)"
	}

	// Count tasks/*.md files.
	taskFiles, _ := filepath.Glob(filepath.Join(projectPath, "tasks/*.md"))
	taskCount := len(taskFiles)

	summary := fmt.Sprintf("[auto] 分支 %s；最新 commit: %s；tasks/ 有 %d 個檔案", branch, subject, taskCount)
	if len(summary) > 300 {
		summary = summary[:300]
	}

	updates := map[string]any{
		"summary":      summary,
		"last_updated": time.Now().UTC().Format(time.RFC3339),
	}
	return updates, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", dir}, args...)
	out, err := exec.CommandContext(ctx, "git", cmdArgs...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
