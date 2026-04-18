package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"tetora/internal/log"
)

// Sentinel errors returned by AppendIntel so non-HTTP callers (e.g. the Discord
// bot) can translate them into their own response formats.
var (
	ErrInvalidFrontID = errors.New("invalid front_id")
	ErrInvalidNote    = errors.New("invalid note")
)

const warRoomNoteMaxLen = 4096

// reValidFrontID matches kebab-case front IDs: lowercase alphanumeric and hyphens only.
// A leading hyphen is rejected because the regex requires starting with [a-z0-9].
var reValidFrontID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// warRoomMu serialises concurrent writes to status.json from the HTTP layer.
// The Discord bot writes status.json without holding this mutex (it runs in its
// own goroutine context), but HTTP-layer writes are rare and this prevents races
// between concurrent HTTP requests.
var warRoomMu sync.Mutex

// WarRoomDeps holds dependencies for War Room HTTP handlers.
type WarRoomDeps struct {
	// WorkspaceDir returns the workspace root directory (e.g. ~/.tetora/workspace).
	WorkspaceDir func() string
}

// RegisterWarRoomRoutes registers the War Room API endpoints.
//
//	GET  /api/war-room/md/{front_id}  — return raw markdown living document
//	POST /api/war-room/intel           — append an intel entry to a front's md
func RegisterWarRoomRoutes(mux *http.ServeMux, d WarRoomDeps) {
	// GET /api/war-room/md/ — serve the markdown document for a front.
	mux.HandleFunc("/api/war-room/md/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		frontID := strings.TrimPrefix(r.URL.Path, "/api/war-room/md/")
		frontID = strings.Trim(frontID, "/")

		if !reValidFrontID.MatchString(frontID) {
			w.Header().Set("Content-Type", "application/json")
			b, _ := json.Marshal(map[string]string{
				"error":    "invalid_front_id",
				"detail":   "front_id must match ^[a-z0-9][a-z0-9-]*$",
				"front_id": frontID,
			})
			http.Error(w, string(b), http.StatusBadRequest)
			return
		}

		mdPath := warRoomMDPath(d.WorkspaceDir(), frontID)
		data, err := os.ReadFile(mdPath)
		if err != nil {
			if os.IsNotExist(err) {
				w.Header().Set("Content-Type", "application/json")
				b, _ := json.Marshal(map[string]string{
					"error":    "not_found",
					"front_id": frontID,
				})
				http.Error(w, string(b), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
			return
		}

		log.Info("war-room md served", "front_id", frontID, "bytes", len(data))
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write(data)
	})

	// POST /api/war-room/intel — append an intel entry.
	mux.HandleFunc("/api/war-room/intel", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			FrontID string `json:"front_id"`
			Note    string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid_json","detail":"request body must be valid JSON"}`, http.StatusBadRequest)
			return
		}

		if !reValidFrontID.MatchString(req.FrontID) {
			b, _ := json.Marshal(map[string]string{
				"error":    "invalid_front_id",
				"detail":   "front_id must match ^[a-z0-9][a-z0-9-]*$",
				"front_id": req.FrontID,
			})
			http.Error(w, string(b), http.StatusBadRequest)
			return
		}

		bullet, err := AppendIntel(d.WorkspaceDir(), req.FrontID, req.Note)
		if err != nil {
			switch {
			case errors.Is(err, ErrInvalidNote):
				http.Error(w, `{"error":"invalid_note","detail":"`+err.Error()+`"}`, http.StatusBadRequest)
			default:
				log.Error("war-room intel: append failed", "front_id", req.FrontID, "err", err)
				http.Error(w, `{"error":"internal_error","detail":"failed to append intel"}`, http.StatusInternalServerError)
			}
			return
		}

		b, _ := json.Marshal(map[string]string{
			"ok":            "true",
			"front_id":      req.FrontID,
			"line_appended": bullet,
		})
		w.Write(b)
	})
}

// AppendIntel appends an intel bullet to the front's markdown document and
// updates status.json's last_intel_at. Returns the bullet line that was
// appended. Callers must validate frontID via reValidFrontID before calling.
//
// Errors:
//   - ErrInvalidNote: note is empty or exceeds warRoomNoteMaxLen after trimming.
//   - Other errors wrap underlying I/O failures.
func AppendIntel(wsDir, frontID, note string) (string, error) {
	if !reValidFrontID.MatchString(frontID) {
		return "", ErrInvalidFrontID
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return "", fmt.Errorf("%w: note must not be empty", ErrInvalidNote)
	}
	if len(note) > warRoomNoteMaxLen {
		return "", fmt.Errorf("%w: note exceeds %d characters", ErrInvalidNote, warRoomNoteMaxLen)
	}

	loc := time.FixedZone("Asia/Taipei", 8*60*60)
	now := time.Now().In(loc)
	bullet := fmt.Sprintf("- [%s (台北)] %s", now.Format("2006-01-02 15:04"), note)

	mdPath := warRoomMDPath(wsDir, frontID)
	statusPath := warRoomStatusPath(wsDir)

	warRoomMu.Lock()
	defer warRoomMu.Unlock()

	var mdData []byte
	existing, err := os.ReadFile(mdPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("read md: %w", err)
		}
		if mkErr := os.MkdirAll(filepath.Dir(mdPath), 0o755); mkErr != nil {
			return "", fmt.Errorf("mkdir: %w", mkErr)
		}
		mdData = warRoomMinimalMD(frontID)
	} else {
		mdData = existing
	}

	mdData = appendIntelBullet(mdData, bullet)

	if err := atomicWrite(mdPath, mdData, 0o644); err != nil {
		return "", fmt.Errorf("write md: %w", err)
	}

	if err := updateStatusLastIntelAt(statusPath, frontID, time.Now().UTC()); err != nil {
		log.Warn("war-room intel: status.json update failed", "front_id", frontID, "err", err)
	}

	log.Info("war-room intel appended", "front_id", frontID, "chars", len(note))
	return bullet, nil
}

// warRoomMDPath returns the path to a front's markdown file.
func warRoomMDPath(wsDir, frontID string) string {
	return filepath.Join(wsDir, "memory", "war-room", "projects", frontID+".md")
}

// warRoomStatusPath returns the path to status.json.
func warRoomStatusPath(wsDir string) string {
	return filepath.Join(wsDir, "memory", "war-room", "status.json")
}

// warRoomMinimalMD returns a minimal markdown document for a new front.
func warRoomMinimalMD(frontID string) []byte {
	return []byte(fmt.Sprintf("# War Room — %s\n\n## 7. 用戶 Intel 累積\n\n", frontID))
}

// appendIntelBullet finds the Intel section in md and appends a bullet.
// If no matching section exists, one is appended at the end.
func appendIntelBullet(md []byte, bullet string) []byte {
	content := string(md)
	lines := strings.Split(content, "\n")

	// Find a line matching ## ... Intel ... (case-insensitive).
	intelIdx := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") && strings.Contains(strings.ToLower(line), "intel") {
			intelIdx = i
			break
		}
	}

	if intelIdx == -1 {
		// No intel section — append one at the end.
		// Ensure trailing newline before new section.
		if len(content) > 0 && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n## 7. 用戶 Intel 累積\n\n" + bullet + "\n"
		return []byte(content)
	}

	// Find the insertion point: right before the next ## heading after intelIdx,
	// or at the end of the file if none exists.
	insertAt := len(lines) // default: append at very end
	for i := intelIdx + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			insertAt = i
			break
		}
	}

	// Insert the bullet line just before insertAt, with a blank line guard.
	// Ensure the line before insertAt is blank (not already occupied by content).
	newLines := make([]string, 0, len(lines)+2)
	newLines = append(newLines, lines[:insertAt]...)

	// If the last line before insertAt is non-empty, add a blank line first.
	if insertAt > 0 && strings.TrimSpace(lines[insertAt-1]) != "" {
		newLines = append(newLines, "")
	}
	newLines = append(newLines, bullet)
	newLines = append(newLines, lines[insertAt:]...)

	return []byte(strings.Join(newLines, "\n"))
}

// atomicWrite writes data to path via a .tmp file + rename.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) // best-effort cleanup
		return fmt.Errorf("rename tmp: %w", err)
	}
	return nil
}

// warRoomStatusEntry is a minimal JSON representation of a War Room front used
// only to update the last_intel_at field without losing other fields.
type warRoomStatusEntry struct {
	ID          string  `json:"id"`
	LastIntelAt *string `json:"last_intel_at,omitempty"`
}

// updateStatusLastIntelAt reads status.json, sets last_intel_at on the matching
// front, and writes the file back atomically. Unknown fields are preserved via
// json.RawMessage round-tripping.
func updateStatusLastIntelAt(statusPath, frontID string, ts time.Time) error {
	data, err := os.ReadFile(statusPath)
	if err != nil {
		if os.IsNotExist(err) {
			// status.json doesn't exist yet — nothing to update.
			return nil
		}
		return fmt.Errorf("read status.json: %w", err)
	}

	// Parse into a shape that preserves unknown fields.
	var envelope struct {
		SchemaVersion int               `json:"schema_version"`
		GeneratedAt   string            `json:"generated_at"`
		Fronts        []json.RawMessage `json:"fronts"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("parse status.json: %w", err)
	}

	isoNow := ts.UTC().Format(time.RFC3339)
	found := false

	for i, rawFront := range envelope.Fronts {
		// Extract just the "id" field to identify the front.
		var idOnly struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rawFront, &idOnly); err != nil {
			continue
		}
		if idOnly.ID != frontID {
			continue
		}

		// Merge last_intel_at into the raw JSON object.
		var m map[string]json.RawMessage
		if err := json.Unmarshal(rawFront, &m); err != nil {
			continue
		}
		val, _ := json.Marshal(isoNow)
		m["last_intel_at"] = val
		updated, err := json.Marshal(m)
		if err != nil {
			continue
		}
		envelope.Fronts[i] = updated
		found = true
		break
	}

	if !found {
		// Front not in status.json — nothing to update.
		return nil
	}

	envelope.GeneratedAt = isoNow
	out, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal status.json: %w", err)
	}
	return atomicWrite(statusPath, append(out, '\n'), 0o644)
}
