package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tetora/internal/log"
	"tetora/internal/warroom"
)

// Sentinel errors returned by AppendIntel so non-HTTP callers (e.g. the Discord
// bot) can translate them into their own response formats.
var (
	ErrInvalidFrontID = errors.New("invalid front_id")
	ErrInvalidNote    = errors.New("invalid note")
)

const warRoomNoteMaxLen = 4096

// maxOverrideHours caps manual_override.expires_at offset at 30 days so a
// typo (e.g. 99999999) cannot produce an expiry thousands of years out.
const maxOverrideHours = 24 * 30

// reValidFrontID matches kebab-case front IDs: lowercase alphanumeric and hyphens only.
// A leading hyphen is rejected because the regex requires starting with [a-z0-9].
var reValidFrontID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// WarRoomDeps holds dependencies for War Room HTTP handlers.
type WarRoomDeps struct {
	// WorkspaceDir returns the workspace root directory (e.g. ~/.tetora/workspace).
	WorkspaceDir func() string
	// TriggerAutoUpdate runs the war_room_autoupdate cron job ad-hoc. Optional;
	// if nil the /api/war-room/autoupdate/trigger endpoint returns 503.
	TriggerAutoUpdate func(ctx context.Context) error
	// AutoUpdateMeta returns scheduling metadata for the autoupdate cron job.
	// Optional; if nil the /api/war-room/autoupdate/meta endpoint returns 503.
	AutoUpdateMeta func() AutoUpdateMeta
}

// AutoUpdateMeta is the payload shape returned by /api/war-room/autoupdate/meta.
type AutoUpdateMeta struct {
	Enabled  bool      `json:"enabled"`
	Schedule string    `json:"schedule,omitempty"`
	LastRun  time.Time `json:"last_run"`
	NextRun  time.Time `json:"next_run"`
	Running  bool      `json:"running"`
	LastErr  string    `json:"last_err,omitempty"`
}

// validStatusValues enumerates the allowed status strings for fronts.
var validStatusValues = map[string]bool{
	"green":   true,
	"yellow":  true,
	"red":     true,
	"paused":  true,
	"unknown": true,
}

// validCardTypes enumerates supported card_type values when creating fronts.
var validCardTypes = map[string]bool{
	"metrics":  true,
	"strategy": true,
	"collab":   true,
}

// validCategories enumerates supported category values when creating fronts.
// Keep in sync with the <select id="wr-cf-category"> options in dashboard.html /
// dashboard/body.html.
var validCategories = map[string]bool{
	"finance":       true,
	"dev":           true,
	"content":       true,
	"marketing":     true,
	"business":      true,
	"collaboration": true,
	"planning":      true,
	"freelance":     true,
	"company":       true,
}

// reservedFrontIDs blocks front IDs that would collide with exact sub-routes
// under /api/war-room/front/*. Go's ServeMux prefers exact matches over
// subtree patterns, so a front with id "status" or "override" could not be
// reached by the subtree handler (e.g. DELETE /api/war-room/front/status would
// be routed to the status POST handler and reject with 405).
var reservedFrontIDs = map[string]bool{
	"status":   true,
	"override": true,
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

	// POST /api/war-room/front/status — quick status update.
	// Body: {front_id, status, summary?, blocking?, next_action?, override_hours?, reason?}
	mux.HandleFunc("/api/war-room/front/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			FrontID       string  `json:"front_id"`
			Status        string  `json:"status"`
			Summary       *string `json:"summary"`
			Blocking      *string `json:"blocking"`
			NextAction    *string `json:"next_action"`
			OverrideHours *int    `json:"override_hours"`
			Reason        string  `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid_json"}`, http.StatusBadRequest)
			return
		}
		if !reValidFrontID.MatchString(req.FrontID) {
			http.Error(w, `{"error":"invalid_front_id"}`, http.StatusBadRequest)
			return
		}
		if !validStatusValues[req.Status] {
			http.Error(w, `{"error":"invalid_status","detail":"must be green/yellow/red/paused/unknown"}`, http.StatusBadRequest)
			return
		}
		updates := map[string]any{
			"status":       req.Status,
			"last_updated": time.Now().UTC().Format(time.RFC3339),
		}
		if req.Summary != nil {
			updates["summary"] = *req.Summary
		}
		if req.Blocking != nil {
			updates["blocking"] = *req.Blocking
		}
		if req.NextAction != nil {
			updates["next_action"] = *req.NextAction
		}
		if req.OverrideHours != nil && *req.OverrideHours > 0 {
			if *req.OverrideHours > maxOverrideHours {
				http.Error(w, `{"error":"override_hours_too_large","detail":"max 720 (30 days)"}`, http.StatusBadRequest)
				return
			}
			expires := time.Now().UTC().Add(time.Duration(*req.OverrideHours) * time.Hour).Format(time.RFC3339)
			updates["manual_override"] = map[string]any{
				"active":     true,
				"expires_at": expires,
				"reason":     req.Reason,
			}
		}
		if err := mutateFront(d.WorkspaceDir(), req.FrontID, updates); err != nil {
			writeMutateError(w, err)
			return
		}
		log.Info("war-room front status set", "front_id", req.FrontID, "status", req.Status)
		w.Write([]byte(`{"ok":true}`))
	})

	// POST /api/war-room/front/override — set or clear manual_override.
	// Body: {front_id, active, hours?, reason?}
	mux.HandleFunc("/api/war-room/front/override", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			FrontID string `json:"front_id"`
			Active  bool   `json:"active"`
			Hours   int    `json:"hours"`
			Reason  string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid_json"}`, http.StatusBadRequest)
			return
		}
		if !reValidFrontID.MatchString(req.FrontID) {
			http.Error(w, `{"error":"invalid_front_id"}`, http.StatusBadRequest)
			return
		}
		if req.Hours > maxOverrideHours {
			http.Error(w, `{"error":"hours_too_large","detail":"max 720 (30 days)"}`, http.StatusBadRequest)
			return
		}
		var override map[string]any
		if req.Active {
			override = map[string]any{"active": true, "reason": req.Reason}
			if req.Hours > 0 {
				override["expires_at"] = time.Now().UTC().Add(time.Duration(req.Hours) * time.Hour).Format(time.RFC3339)
			} else {
				override["expires_at"] = nil
			}
		} else {
			override = map[string]any{"active": false, "expires_at": nil}
		}
		if err := mutateFront(d.WorkspaceDir(), req.FrontID, map[string]any{"manual_override": override}); err != nil {
			writeMutateError(w, err)
			return
		}
		log.Info("war-room override set", "front_id", req.FrontID, "active", req.Active, "hours", req.Hours)
		w.Write([]byte(`{"ok":true}`))
	})

	// POST /api/war-room/autoupdate/trigger — run autoupdate ad-hoc.
	mux.HandleFunc("/api/war-room/autoupdate/trigger", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if d.TriggerAutoUpdate == nil {
			http.Error(w, `{"error":"unavailable","detail":"autoupdate trigger not wired"}`, http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		if err := d.TriggerAutoUpdate(ctx); err != nil {
			log.Error("war-room autoupdate trigger failed", "err", err)
			b, _ := json.Marshal(map[string]string{"error": "trigger_failed", "detail": err.Error()})
			http.Error(w, string(b), http.StatusInternalServerError)
			return
		}
		log.Info("war-room autoupdate triggered via HTTP")
		w.Write([]byte(`{"ok":true}`))
	})

	// GET /api/war-room/autoupdate/meta — return last/next run metadata.
	mux.HandleFunc("/api/war-room/autoupdate/meta", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if d.AutoUpdateMeta == nil {
			http.Error(w, `{"error":"unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		meta := d.AutoUpdateMeta()
		b, _ := json.Marshal(meta)
		w.Write(b)
	})

	// POST /api/war-room/front — create a new front.
	// Body: {id, name, category, card_type, auto?, depends_on?}
	mux.HandleFunc("/api/war-room/front", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ID        string   `json:"id"`
			Name      string   `json:"name"`
			Category  string   `json:"category"`
			CardType  string   `json:"card_type"`
			Auto      bool     `json:"auto"`
			DependsOn []string `json:"depends_on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid_json"}`, http.StatusBadRequest)
			return
		}
		if !reValidFrontID.MatchString(req.ID) {
			http.Error(w, `{"error":"invalid_id"}`, http.StatusBadRequest)
			return
		}
		if reservedFrontIDs[req.ID] {
			http.Error(w, `{"error":"reserved_id","detail":"id collides with a sub-route"}`, http.StatusConflict)
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			http.Error(w, `{"error":"name_required"}`, http.StatusBadRequest)
			return
		}
		if !validCategories[req.Category] {
			http.Error(w, `{"error":"invalid_category"}`, http.StatusBadRequest)
			return
		}
		if !validCardTypes[req.CardType] {
			http.Error(w, `{"error":"invalid_card_type","detail":"must be metrics/strategy/collab"}`, http.StatusBadRequest)
			return
		}
		if req.DependsOn == nil {
			req.DependsOn = []string{}
		}
		// Each depends_on item must match the same front-id format. Otherwise
		// arbitrary strings (e.g. XSS payloads, SQL-ish fragments) could land
		// in status.json's raw depends_on array.
		for _, dep := range req.DependsOn {
			if !reValidFrontID.MatchString(dep) {
				b, _ := json.Marshal(map[string]string{
					"error":  "invalid_depends_on",
					"detail": "each depends_on item must match ^[a-z0-9][a-z0-9-]*$",
					"got":    dep,
				})
				http.Error(w, string(b), http.StatusBadRequest)
				return
			}
		}
		if err := createFront(d.WorkspaceDir(), req.ID, req.Name, req.Category, req.CardType, req.Auto, req.DependsOn); err != nil {
			writeMutateError(w, err)
			return
		}
		log.Info("war-room front created", "id", req.ID, "card_type", req.CardType, "auto", req.Auto)
		w.Write([]byte(`{"ok":true}`))
	})

	// DELETE /api/war-room/front/{id} — archive (default) or hard-delete (?hard=true).
	mux.HandleFunc("/api/war-room/front/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodDelete {
			http.Error(w, `{"error":"DELETE only"}`, http.StatusMethodNotAllowed)
			return
		}
		frontID := strings.TrimPrefix(r.URL.Path, "/api/war-room/front/")
		frontID = strings.Trim(frontID, "/")
		if !reValidFrontID.MatchString(frontID) {
			http.Error(w, `{"error":"invalid_front_id"}`, http.StatusBadRequest)
			return
		}
		hard := r.URL.Query().Get("hard") == "true"
		if err := deleteFront(d.WorkspaceDir(), frontID, hard); err != nil {
			writeMutateError(w, err)
			return
		}
		log.Info("war-room front removed", "id", frontID, "hard", hard)
		w.Write([]byte(`{"ok":true}`))
	})
}

// errFrontNotFound is returned by mutateFront/deleteFront when the given id does
// not exist in status.json.
var errFrontNotFound = errors.New("front not found")

// errFrontExists is returned by createFront when the id already exists.
var errFrontExists = errors.New("front already exists")

// mutateFront loads status.json, merges updates into the matching front, and
// saves atomically. Acquires warroom.Mu for the full critical section.
func mutateFront(wsDir, frontID string, updates map[string]any) error {
	statusPath := warRoomStatusPath(wsDir)
	warroom.Mu.Lock()
	defer warroom.Mu.Unlock()
	s, err := warroom.LoadStatus(statusPath)
	if err != nil {
		return err
	}
	idx := -1
	for i, raw := range s.Fronts {
		id, err := warroom.FrontID(raw)
		if err == nil && id == frontID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errFrontNotFound
	}
	newRaw, err := warroom.UpdateFrontFields(s.Fronts[idx], updates)
	if err != nil {
		return err
	}
	s.Fronts[idx] = newRaw
	return warroom.SaveStatus(statusPath, s)
}

// createFront adds a new front to status.json with sensible defaults.
func createFront(wsDir, id, name, category, cardType string, auto bool, dependsOn []string) error {
	statusPath := warRoomStatusPath(wsDir)
	warroom.Mu.Lock()
	defer warroom.Mu.Unlock()
	s, err := warroom.LoadStatus(statusPath)
	if err != nil {
		if os.IsNotExist(err) {
			s = &warroom.Status{SchemaVersion: 1}
		} else {
			return err
		}
	}
	for _, raw := range s.Fronts {
		existing, err := warroom.FrontID(raw)
		if err == nil && existing == id {
			return errFrontExists
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	front := map[string]any{
		"id":              id,
		"name":            name,
		"category":        category,
		"card_type":       cardType,
		"status":          "unknown",
		"auto":            auto,
		"depends_on":      dependsOn,
		"summary":         "",
		"blocking":        "",
		"next_action":     "",
		"last_updated":    now,
		"manual_override": map[string]any{"active": false, "expires_at": nil},
	}
	data, err := json.Marshal(front)
	if err != nil {
		return err
	}
	s.Fronts = append(s.Fronts, json.RawMessage(data))
	return warroom.SaveStatus(statusPath, s)
}

// deleteFront either archives (sets archived=true) or hard-deletes the front.
func deleteFront(wsDir, frontID string, hard bool) error {
	statusPath := warRoomStatusPath(wsDir)
	warroom.Mu.Lock()
	defer warroom.Mu.Unlock()
	s, err := warroom.LoadStatus(statusPath)
	if err != nil {
		return err
	}
	idx := -1
	for i, raw := range s.Fronts {
		id, err := warroom.FrontID(raw)
		if err == nil && id == frontID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errFrontNotFound
	}
	if hard {
		s.Fronts = append(s.Fronts[:idx], s.Fronts[idx+1:]...)
	} else {
		updated, err := warroom.UpdateFrontFields(s.Fronts[idx], map[string]any{"archived": true})
		if err != nil {
			return err
		}
		s.Fronts[idx] = updated
	}
	return warroom.SaveStatus(statusPath, s)
}

// writeMutateError maps internal errors to HTTP responses with a JSON body.
func writeMutateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errFrontNotFound):
		http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
	case errors.Is(err, errFrontExists):
		http.Error(w, `{"error":"exists"}`, http.StatusConflict)
	default:
		log.Error("war-room mutate failed", "err", err)
		http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
	}
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

	warroom.Mu.Lock()
	defer warroom.Mu.Unlock()

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
