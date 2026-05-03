package recap

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tetora/internal/config"
	"tetora/internal/log"
)

// Watcher polls Claude Code transcript JSONL files and delivers new
// `away_summary` entries to Discord. Safe to run as a single daemon goroutine.
type Watcher struct {
	Cfg    config.DiscordRecapConfig
	Router *Router

	mu    sync.Mutex
	state map[string]*fileState // key: absolute jsonl path
}

type fileState struct {
	offset int64
	mtime  time.Time
}

// New builds a watcher with the given config and router. Call Start on the
// result to begin the polling loop.
func New(cfg config.DiscordRecapConfig, router *Router) *Watcher {
	return &Watcher{
		Cfg:    cfg,
		Router: router,
		state:  make(map[string]*fileState),
	}
}

// Start runs the polling loop until ctx is cancelled. On the first tick it
// records current EOF for every existing transcript file so historical
// `away_summary` entries are NOT re-delivered on daemon restart.
func (w *Watcher) Start(ctx context.Context) {
	if !w.Cfg.Enabled {
		log.Info("recap watcher disabled")
		return
	}
	root := w.transcriptRoot()
	interval := w.pollInterval()
	log.Info("recap watcher started", "root", root, "intervalMs", interval.Milliseconds())

	w.primeExistingFiles(root)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("recap watcher stopped")
			return
		case <-ticker.C:
			w.scan(root)
		}
	}
}

// primeExistingFiles marks all currently-present transcripts as "already seen
// up to EOF" so we do not re-forward historical recaps after a daemon restart.
// New sessions (files that did not exist at start) will still be picked up.
func (w *Watcher) primeExistingFiles(root string) {
	paths, err := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	if err != nil {
		log.Warn("recap: glob on startup failed", "root", root, "error", err)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		w.state[p] = &fileState{offset: info.Size(), mtime: info.ModTime()}
	}
	log.Debug("recap: primed transcript offsets", "files", len(paths))
}

// scan walks every transcript once, reads the delta from each modified file,
// and delivers any newly-discovered away_summary records.
func (w *Watcher) scan(root string) {
	paths, err := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	if err != nil {
		log.Warn("recap: glob failed", "root", root, "error", err)
		return
	}
	for _, p := range paths {
		w.scanOne(p)
	}
}

func (w *Watcher) scanOne(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}

	w.mu.Lock()
	st, seen := w.state[path]
	w.mu.Unlock()

	if !seen {
		// New file that appeared after daemon start. Read from the beginning
		// so we capture recaps generated during this session's lifetime.
		st = &fileState{offset: 0}
	} else if !info.ModTime().After(st.mtime) && info.Size() == st.offset {
		// No change since last check.
		return
	}

	records, newOffset, err := ReadAwaySummariesFrom(path, st.offset)
	if err != nil {
		log.Warn("recap: read transcript failed", "path", path, "error", err)
	}

	w.mu.Lock()
	w.state[path] = &fileState{offset: newOffset, mtime: info.ModTime()}
	w.mu.Unlock()

	for _, rec := range records {
		if err := w.Router.Deliver(rec); err != nil {
			log.Warn("recap: deliver failed", "error", err, "uuid", rec.UUID)
		}
	}
}

func (w *Watcher) transcriptRoot() string {
	if w.Cfg.TranscriptRoot != "" {
		return expandHome(w.Cfg.TranscriptRoot)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

func (w *Watcher) pollInterval() time.Duration {
	if w.Cfg.PollIntervalMs > 0 {
		return time.Duration(w.Cfg.PollIntervalMs) * time.Millisecond
	}
	return 2 * time.Second
}

func expandHome(p string) string {
	if len(p) == 0 || p[0] != '~' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}
