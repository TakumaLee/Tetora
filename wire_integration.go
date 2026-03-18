package main

// wire_integration.go wires the integration service internal packages to the root
// package by providing constructors, type aliases, and OAuth adapters that keep the
// root API surface stable.

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tcrypto "tetora/internal/crypto"
	"tetora/internal/db"
	"tetora/internal/integration/drive"
	"tetora/internal/integration/dropbox"
	"tetora/internal/integration/gmail"
	"tetora/internal/integration/homeassistant"
	"tetora/internal/integration/notes"
	"tetora/internal/integration/oauthif"
	"tetora/internal/integration/podcast"
	"tetora/internal/integration/spotify"
	"tetora/internal/integration/twitter"
	"tetora/internal/knowledge"
	"tetora/internal/log"
	"tetora/internal/mcp"
	iOAuth "tetora/internal/oauth"
	"tetora/internal/storage"
	"tetora/internal/tools"
	"tetora/internal/voice"
)

// --- Service type aliases ---

type GmailService = gmail.Service
type DriveService = drive.Service
type DropboxService = dropbox.Service
type SpotifyService = spotify.Service
type TwitterService = twitter.Service
type PodcastService = podcast.Service
type HAService = homeassistant.Service
type NotesService = notes.Service

// --- Data type aliases ---

// Gmail types
type GmailMessage = gmail.Message
type GmailMessageSummary = gmail.MessageSummary

// Drive types
type DriveFile = drive.File
type DriveFileList = drive.FileList

// Dropbox types
type DropboxFile = dropbox.File
type DropboxListResult = dropbox.ListResult
type DropboxSearchResult = dropbox.SearchResult

// Spotify types
type SpotifyItem = spotify.Item
type SpotifyDevice = spotify.Device

// Twitter types
type Tweet = twitter.Tweet
type TwitterUser = twitter.User

// Podcast types
type PodcastFeed = podcast.Feed
type PodcastEpisode = podcast.Episode

// HomeAssistant types
type HAEntity = homeassistant.Entity

// Notes types
type NoteInfo = notes.NoteInfo
type NotesSearchResult = notes.SearchResult

// --- Gmail helper forwarding ---

func base64URLEncode(data []byte) string         { return gmail.Base64URLEncode(data) }
func decodeBase64URL(s string) (string, error)    { return gmail.DecodeBase64URL(s) }
func buildRFC2822(from, to, subject, body string, cc, bcc []string) string {
	return gmail.BuildRFC2822(from, to, subject, body, cc, bcc)
}
func parseGmailPayload(payload map[string]any) (subject, from, to, date, body string) {
	return gmail.ParsePayload(payload)
}
func extractBody(payload map[string]any, mimeType string) string {
	return gmail.ExtractBody(payload, mimeType)
}

// Drive helper forwarding
func isTextMime(mime string) bool { return drive.IsTextMime(mime) }

// Spotify helper forwarding
func parseSearchResults(data []byte, searchType string) ([]SpotifyItem, error) {
	return spotify.ParseSearchResults(data, searchType)
}
func parseSpotifyItem(data json.RawMessage, itemType string) (SpotifyItem, error) {
	return spotify.ParseItem(data, itemType)
}
func jsonStrField(m map[string]any, key string) string { return spotify.JSONStrField(m, key) }

// Twitter helper forwarding
func parseTweetsResponse(body io.Reader) ([]Tweet, error) { return twitter.ParseTweetsResponse(body) }

// Podcast helper forwarding
func parsePodcastRSS(data []byte) (*PodcastFeed, []PodcastEpisode, error) {
	return podcast.ParseRSS(data)
}
func truncatePodcastText(s string, maxLen int) string { return podcast.TruncateText(s, maxLen) }
func formatEpisodes(episodes []PodcastEpisode) string  { return podcast.FormatEpisodes(episodes) }

// HomeAssistant WebSocket helper forwarding
func wsGenerateKey() string                            { return homeassistant.WsGenerateKey() }
func wsReadFrame(r *bufio.Reader) ([]byte, error)      { return homeassistant.WsReadFrame(r) }
func wsWriteFrame(conn net.Conn, payload []byte) error { return homeassistant.WsWriteFrame(conn, payload) }

// Notes helper forwarding
func validateNoteName(name string) error           { return notes.ValidateNoteName(name) }
func extractWikilinks(content string) []string     { return notes.ExtractWikilinks(content) }
func extractTags(content string) []string          { return notes.ExtractTags(content) }
func lnNotes(x float64) float64                   { return notes.Ln(x) }

// --- OAuth adapters ---

// oauthRequesterAdapter wraps *OAuthManager to satisfy oauthif.Requester.
type oauthRequesterAdapter struct {
	mgr *OAuthManager
}

func (a *oauthRequesterAdapter) Request(ctx context.Context, service, method, url string, body io.Reader) (*http.Response, error) {
	return a.mgr.Request(ctx, service, method, url, body)
}

// Ensure oauthRequesterAdapter satisfies the interface at compile time.
var _ oauthif.Requester = (*oauthRequesterAdapter)(nil)

// oauthTokenProviderAdapter wraps *OAuthManager to satisfy oauthif.TokenProvider.
type oauthTokenProviderAdapter struct {
	oauthRequesterAdapter
}

func (a *oauthTokenProviderAdapter) RefreshTokenIfNeeded(service string) (string, error) {
	tok, err := a.mgr.RefreshTokenIfNeeded(service)
	if err != nil {
		return "", err
	}
	if tok == nil || tok.AccessToken == "" {
		return "", fmt.Errorf("%s not connected — authorize via /api/oauth/%s/authorize", service, service)
	}
	return tok.AccessToken, nil
}

var _ oauthif.TokenProvider = (*oauthTokenProviderAdapter)(nil)

// --- Constructors ---

func newGmailService(cfg *Config) *GmailService {
	var oauth oauthif.Requester
	if globalOAuthManager != nil {
		oauth = &oauthRequesterAdapter{mgr: globalOAuthManager}
	}
	return gmail.New(cfg.Gmail, oauth)
}

func newDriveService() *DriveService {
	var oauth oauthif.Requester
	if globalOAuthManager != nil {
		oauth = &oauthRequesterAdapter{mgr: globalOAuthManager}
	}
	return drive.New(oauth)
}

func newDropboxService() *DropboxService {
	var oauth oauthif.Requester
	if globalOAuthManager != nil {
		oauth = &oauthRequesterAdapter{mgr: globalOAuthManager}
	}
	return dropbox.New(oauth)
}

func newSpotifyService(cfg *Config) *SpotifyService {
	var oauth oauthif.TokenProvider
	if globalOAuthManager != nil {
		oauth = &oauthTokenProviderAdapter{oauthRequesterAdapter{mgr: globalOAuthManager}}
	}
	return spotify.New(cfg.Spotify, oauth)
}

func newTwitterService(cfg *Config) *TwitterService {
	var oauth oauthif.TokenProvider
	if globalOAuthManager != nil {
		oauth = &oauthTokenProviderAdapter{oauthRequesterAdapter{mgr: globalOAuthManager}}
	}
	return twitter.New(cfg.Twitter, oauth)
}

func initPodcastDB(dbPath string) error {
	return podcast.InitDB(dbPath, db.Exec)
}

func newPodcastService(dbPath string) *PodcastService {
	return podcast.New(dbPath, podcast.DB{
		Query:   db.Query,
		Exec:    db.Exec,
		Escape:  db.Escape,
		LogInfo: log.Info,
		LogWarn: log.Warn,
	})
}

func newHAService(cfg HomeAssistantConfig) *HAService {
	return homeassistant.New(cfg, log.Info, log.Warn, log.Debug)
}

func newNotesService(cfg *Config) *NotesService {
	var embedFn notes.EmbedFn
	if cfg.Notes.AutoEmbed && cfg.Embedding.Enabled {
		embedFn = func(ctx context.Context, name, content string, tags []string) error {
			vec, err := getEmbedding(ctx, cfg, content)
			if err != nil {
				return err
			}
			meta := map[string]interface{}{
				"name": name,
				"tags": tags,
			}
			return storeEmbedding(cfg.HistoryDB, "notes", name, content, vec, meta)
		}
	}
	return notes.New(cfg.Notes, cfg.BaseDir, cfg.Embedding.Enabled, embedFn, log.Info, log.Warn, log.Debug)
}

// Global notes service with thread-safe access (matches original pattern).
var (
	globalNotesMu      sync.RWMutex
	globalNotesService *NotesService
)

func setGlobalNotesService(svc *NotesService) {
	globalNotesMu.Lock()
	defer globalNotesMu.Unlock()
	globalNotesService = svc
}

func getGlobalNotesService() *NotesService {
	globalNotesMu.RLock()
	defer globalNotesMu.RUnlock()
	return globalNotesService
}

// haEventPublisherAdapter wraps *sseBroker to satisfy homeassistant.EventPublisher.
type haEventPublisherAdapter struct {
	broker *sseBroker
}

func (a *haEventPublisherAdapter) PublishEvent(key, eventType string, data any) {
	a.broker.Publish(key, SSEEvent{Type: eventType, Data: data})
}

var _ homeassistant.EventPublisher = (*haEventPublisherAdapter)(nil)

// --- Global singletons (backwards compat) ---

var (
	globalGmailService   *GmailService
	globalDriveService   *DriveService
	globalDropboxService *DropboxService
	globalSpotifyService *SpotifyService
	globalTwitterService *TwitterService
	globalPodcastService *PodcastService
	globalHAService      *HAService
	globalFileManager    *storage.Service
)

func newFileManagerService(cfg *Config) *storage.Service {
	dir := cfg.FileManager.StorageDirOrDefault(cfg.BaseDir)
	return storage.New(cfg.HistoryDB, dir, cfg.FileManager.MaxSizeOrDefault(), makeLifeDB(), newUUID)
}

// --- Base URL forwarding for tests ---

var driveBaseURL = drive.BaseURL

// --- Tool handler stubs ---

func toolEmailList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"maxResults"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if app == nil || app.Gmail == nil {
		return "", fmt.Errorf("gmail not configured; enable gmail in config and connect via OAuth")
	}
	messages, err := app.Gmail.ListMessages(ctx, args.Query, args.MaxResults)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(map[string]any{"count": len(messages), "messages": messages})
	return string(b), nil
}

func toolEmailRead(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.MessageID == "" {
		return "", fmt.Errorf("message_id is required")
	}
	if app == nil || app.Gmail == nil {
		return "", fmt.Errorf("gmail not configured; enable gmail in config and connect via OAuth")
	}
	msg, err := app.Gmail.GetMessage(ctx, args.MessageID)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(msg)
	return string(b), nil
}

func toolEmailSend(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		To      string   `json:"to"`
		Subject string   `json:"subject"`
		Body    string   `json:"body"`
		Cc      []string `json:"cc"`
		Bcc     []string `json:"bcc"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.To == "" {
		return "", fmt.Errorf("to is required")
	}
	if args.Subject == "" {
		return "", fmt.Errorf("subject is required")
	}
	if args.Body == "" {
		return "", fmt.Errorf("body is required")
	}
	if app == nil || app.Gmail == nil {
		return "", fmt.Errorf("gmail not configured; enable gmail in config and connect via OAuth")
	}
	messageID, err := app.Gmail.SendMessage(ctx, args.To, args.Subject, args.Body, args.Cc, args.Bcc)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"status":"sent","messageId":"%s"}`, messageID), nil
}

func toolEmailDraft(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.To == "" {
		return "", fmt.Errorf("to is required")
	}
	if args.Subject == "" {
		return "", fmt.Errorf("subject is required")
	}
	if app == nil || app.Gmail == nil {
		return "", fmt.Errorf("gmail not configured; enable gmail in config and connect via OAuth")
	}
	draftID, err := app.Gmail.CreateDraft(ctx, args.To, args.Subject, args.Body)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"status":"draft_created","draftId":"%s"}`, draftID), nil
}

func toolEmailSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"maxResults"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if app == nil || app.Gmail == nil {
		return "", fmt.Errorf("gmail not configured; enable gmail in config and connect via OAuth")
	}
	messages, err := app.Gmail.SearchMessages(ctx, args.Query, args.MaxResults)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(map[string]any{"count": len(messages), "messages": messages})
	return string(b), nil
}

func toolEmailLabel(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		MessageID    string   `json:"message_id"`
		AddLabels    []string `json:"add_labels"`
		RemoveLabels []string `json:"remove_labels"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.MessageID == "" {
		return "", fmt.Errorf("message_id is required")
	}
	if len(args.AddLabels) == 0 && len(args.RemoveLabels) == 0 {
		return "", fmt.Errorf("at least one of add_labels or remove_labels is required")
	}
	if app == nil || app.Gmail == nil {
		return "", fmt.Errorf("gmail not configured; enable gmail in config and connect via OAuth")
	}
	if err := app.Gmail.ModifyLabels(ctx, args.MessageID, args.AddLabels, args.RemoveLabels); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"status":"labels_modified","messageId":"%s"}`, args.MessageID), nil
}

func toolDriveSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	app := appFromCtx(ctx)
	if app == nil || app.Drive == nil {
		return "", fmt.Errorf("Google Drive integration not enabled")
	}
	files, err := app.Drive.Search(ctx, args.Query, args.MaxResults)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "No files found matching query.", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Drive search results (%d files):\n\n", len(files)))
	for _, f := range files {
		size := f.Size
		if size == "" {
			size = "-"
		}
		sb.WriteString(fmt.Sprintf("- %s | %s | %s | %s bytes | %s\n",
			f.ID, f.Name, f.MimeType, size, f.ModifiedTime))
	}
	return sb.String(), nil
}

func toolDriveUpload(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name     string `json:"name"`
		Content  string `json:"content"`
		MimeType string `json:"mime_type"`
		ParentID string `json:"parent_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	if args.Content == "" {
		return "", fmt.Errorf("content is required")
	}
	app := appFromCtx(ctx)
	if app == nil || app.Drive == nil {
		return "", fmt.Errorf("Google Drive integration not enabled")
	}
	if args.MimeType == "" {
		args.MimeType = storage.MimeFromExt(args.Name)
	}
	result, err := app.Drive.Upload(ctx, args.Name, args.MimeType, args.ParentID, []byte(args.Content))
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	return fmt.Sprintf("File uploaded to Drive:\n%s", string(out)), nil
}

func toolDriveDownload(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		FileID string `json:"file_id"`
		SaveAs string `json:"save_as"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.FileID == "" {
		return "", fmt.Errorf("file_id is required")
	}
	app := appFromCtx(ctx)
	if app == nil || app.Drive == nil {
		return "", fmt.Errorf("Google Drive integration not enabled")
	}
	data, fileMeta, err := app.Drive.Download(ctx, args.FileID)
	if err != nil {
		return "", err
	}
	if args.SaveAs != "" && app.FileManager != nil {
		name := args.SaveAs
		if name == "auto" {
			name = fileMeta.Name
		}
		mf, isDup, err := app.FileManager.StoreFile("", name, "drive", "google_drive", fileMeta.ID, data)
		if err != nil {
			return "", fmt.Errorf("save to file manager: %w", err)
		}
		status := "saved"
		if isDup {
			status = "duplicate (existing)"
		}
		return fmt.Sprintf("Downloaded '%s' (%d bytes) from Drive and %s locally (ID: %s)",
			fileMeta.Name, len(data), status, mf.ID), nil
	}
	if isTextMime(fileMeta.MimeType) && len(data) < 50000 {
		return fmt.Sprintf("Downloaded '%s' (%d bytes):\n\n%s", fileMeta.Name, len(data), string(data)), nil
	}
	return fmt.Sprintf("Downloaded '%s' (%d bytes, %s). Use save_as to store locally.",
		fileMeta.Name, len(data), fileMeta.MimeType), nil
}

func toolDropboxOp(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Action     string `json:"action"`
		Query      string `json:"query"`
		Path       string `json:"path"`
		Content    string `json:"content"`
		Overwrite  bool   `json:"overwrite"`
		Recursive  bool   `json:"recursive"`
		MaxResults int    `json:"max_results"`
		SaveAs     string `json:"save_as"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Action == "" {
		return "", fmt.Errorf("action is required (search, upload, download, list)")
	}
	app := appFromCtx(ctx)
	if app == nil || app.Dropbox == nil {
		return "", fmt.Errorf("Dropbox integration not enabled")
	}
	svc := app.Dropbox

	switch args.Action {
	case "search":
		if args.Query == "" {
			return "", fmt.Errorf("query is required for search")
		}
		files, err := svc.Search(ctx, args.Query, args.MaxResults)
		if err != nil {
			return "", err
		}
		if len(files) == 0 {
			return "No files found.", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Dropbox search results (%d files):\n\n", len(files)))
		for _, f := range files {
			sb.WriteString(fmt.Sprintf("- %s | %s | %d bytes | %s\n",
				f.PathDisplay, f.Name, f.Size, f.ServerModified))
		}
		return sb.String(), nil

	case "upload":
		if args.Path == "" {
			return "", fmt.Errorf("path is required for upload")
		}
		if args.Content == "" {
			return "", fmt.Errorf("content is required for upload")
		}
		result, err := svc.Upload(ctx, args.Path, []byte(args.Content), args.Overwrite)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return fmt.Sprintf("File uploaded to Dropbox:\n%s", string(out)), nil

	case "download":
		if args.Path == "" {
			return "", fmt.Errorf("path is required for download")
		}
		data, meta, err := svc.Download(ctx, args.Path)
		if err != nil {
			return "", err
		}
		if args.SaveAs != "" && app.FileManager != nil {
			name := args.SaveAs
			if name == "auto" && meta != nil {
				name = meta.Name
			}
			if name == "" || name == "auto" {
				parts := strings.Split(args.Path, "/")
				name = parts[len(parts)-1]
			}
			mf, isDup, err := app.FileManager.StoreFile("", name, "dropbox", "dropbox", args.Path, data)
			if err != nil {
				return "", fmt.Errorf("save to file manager: %w", err)
			}
			status := "saved"
			if isDup {
				status = "duplicate (existing)"
			}
			return fmt.Sprintf("Downloaded from Dropbox and %s locally (ID: %s, %d bytes)", status, mf.ID, len(data)), nil
		}
		if len(data) < 50000 {
			return fmt.Sprintf("Downloaded '%s' (%d bytes):\n\n%s", args.Path, len(data), string(data)), nil
		}
		return fmt.Sprintf("Downloaded '%s' (%d bytes). Use save_as to store locally.", args.Path, len(data)), nil

	case "list":
		files, err := svc.ListFolder(ctx, args.Path, args.Recursive)
		if err != nil {
			return "", err
		}
		if len(files) == 0 {
			return "Folder is empty.", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Dropbox folder (%d entries):\n\n", len(files)))
		for _, f := range files {
			tag := f.Tag
			if tag == "" {
				tag = "file"
			}
			sb.WriteString(fmt.Sprintf("- [%s] %s | %s | %d bytes\n",
				tag, f.PathDisplay, f.Name, f.Size))
		}
		return sb.String(), nil

	default:
		return "", fmt.Errorf("unknown action: %s (use search, upload, download, list)", args.Action)
	}
}

// --- Spotify tool handler stubs ---

func toolSpotifyPlay(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Spotify == nil {
		return "", fmt.Errorf("spotify not initialized")
	}

	var args struct {
		Action   string `json:"action"`
		Query    string `json:"query"`
		URI      string `json:"uri"`
		DeviceID string `json:"deviceId"`
		Volume   int    `json:"volume"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := app.Spotify

	switch args.Action {
	case "play":
		uri := args.URI
		if uri == "" && args.Query != "" {
			results, err := svc.Search(args.Query, "track", 1)
			if err != nil {
				return "", fmt.Errorf("search failed: %w", err)
			}
			if len(results) == 0 {
				return "No tracks found for query: " + args.Query, nil
			}
			uri = results[0].URI
			log.Info("spotify play search result", "query", args.Query, "track", results[0].Name, "artist", results[0].Artist)
		}
		if err := svc.Play(uri, args.DeviceID); err != nil {
			return "", fmt.Errorf("play failed: %w", err)
		}
		if uri != "" {
			return fmt.Sprintf("Now playing: %s", uri), nil
		}
		return "Playback resumed.", nil

	case "pause":
		if err := svc.Pause(); err != nil {
			return "", fmt.Errorf("pause failed: %w", err)
		}
		return "Playback paused.", nil

	case "next":
		if err := svc.Next(); err != nil {
			return "", fmt.Errorf("next failed: %w", err)
		}
		return "Skipped to next track.", nil

	case "prev", "previous":
		if err := svc.Previous(); err != nil {
			return "", fmt.Errorf("previous failed: %w", err)
		}
		return "Returned to previous track.", nil

	case "volume":
		if err := svc.SetVolume(args.Volume); err != nil {
			return "", fmt.Errorf("volume failed: %w", err)
		}
		return fmt.Sprintf("Volume set to %d%%.", args.Volume), nil

	default:
		return "", fmt.Errorf("unknown action %q — use play, pause, next, prev, or volume", args.Action)
	}
}

func toolSpotifySearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Spotify == nil {
		return "", fmt.Errorf("spotify not initialized")
	}

	var args struct {
		Query string `json:"query"`
		Type  string `json:"type"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query required")
	}
	if args.Type == "" {
		args.Type = "track"
	}
	if args.Limit <= 0 {
		args.Limit = 5
	}

	results, err := app.Spotify.Search(args.Query, args.Type, args.Limit)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "No results found for: " + args.Query, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Spotify search results for %q (%s):\n\n", args.Query, args.Type)
	for i, item := range results {
		fmt.Fprintf(&sb, "%d. %s", i+1, item.Name)
		if item.Artist != "" {
			fmt.Fprintf(&sb, " — %s", item.Artist)
		}
		if item.Album != "" {
			fmt.Fprintf(&sb, " [%s]", item.Album)
		}
		sb.WriteString("\n")
		fmt.Fprintf(&sb, "   URI: %s\n", item.URI)
		if item.DurMS > 0 {
			dur := time.Duration(item.DurMS) * time.Millisecond
			min := int(dur.Minutes())
			sec := int(dur.Seconds()) % 60
			fmt.Fprintf(&sb, "   Duration: %d:%02d\n", min, sec)
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

func toolSpotifyNowPlaying(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Spotify == nil {
		return "", fmt.Errorf("spotify not initialized")
	}

	item, err := app.Spotify.CurrentlyPlaying()
	if err != nil {
		return "", err
	}
	if item == nil {
		return "Nothing is currently playing.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Now playing: %s", item.Name)
	if item.Artist != "" {
		fmt.Fprintf(&sb, " — %s", item.Artist)
	}
	if item.Album != "" {
		fmt.Fprintf(&sb, " [%s]", item.Album)
	}
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "URI: %s\n", item.URI)
	if item.DurMS > 0 {
		dur := time.Duration(item.DurMS) * time.Millisecond
		min := int(dur.Minutes())
		sec := int(dur.Seconds()) % 60
		fmt.Fprintf(&sb, "Duration: %d:%02d\n", min, sec)
	}
	return sb.String(), nil
}

func toolSpotifyDevices(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Spotify == nil {
		return "", fmt.Errorf("spotify not initialized")
	}

	devices, err := app.Spotify.GetDevices()
	if err != nil {
		return "", err
	}
	if len(devices) == 0 {
		return "No active Spotify devices found.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Spotify devices (%d):\n\n", len(devices))
	for i, d := range devices {
		active := ""
		if d.IsActive {
			active = " [ACTIVE]"
		}
		fmt.Fprintf(&sb, "%d. %s (%s)%s — Volume: %d%%\n", i+1, d.Name, d.Type, active, d.Volume)
		fmt.Fprintf(&sb, "   ID: %s\n", d.ID)
	}
	return sb.String(), nil
}

func toolSpotifyRecommend(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Spotify == nil {
		return "", fmt.Errorf("spotify not initialized")
	}

	var args struct {
		TrackIDs []string `json:"trackIds"`
		Limit    int      `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if len(args.TrackIDs) == 0 {
		current, err := app.Spotify.CurrentlyPlaying()
		if err != nil {
			return "", fmt.Errorf("no seed tracks provided and cannot get current track: %w", err)
		}
		if current == nil {
			return "", fmt.Errorf("no seed tracks provided and nothing is playing")
		}
		args.TrackIDs = []string{current.ID}
	}
	if args.Limit <= 0 {
		args.Limit = 5
	}

	results, err := app.Spotify.GetRecommendations(args.TrackIDs, args.Limit)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "No recommendations found.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Spotify recommendations (%d tracks):\n\n", len(results))
	for i, item := range results {
		fmt.Fprintf(&sb, "%d. %s", i+1, item.Name)
		if item.Artist != "" {
			fmt.Fprintf(&sb, " — %s", item.Artist)
		}
		if item.Album != "" {
			fmt.Fprintf(&sb, " [%s]", item.Album)
		}
		sb.WriteString("\n")
		fmt.Fprintf(&sb, "   URI: %s\n\n", item.URI)
	}
	return sb.String(), nil
}

// --- Twitter tool handler stubs ---

func toolTweetPost(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Text    string `json:"text"`
		ReplyTo string `json:"reply_to"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	app := appFromCtx(ctx)
	if app == nil || app.Twitter == nil {
		return "", fmt.Errorf("twitter not configured; enable twitter in config and connect via OAuth")
	}

	tweet, err := app.Twitter.PostTweet(ctx, args.Text, args.ReplyTo)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{
		"status": "posted",
		"tweet":  tweet,
	})
	return string(b), nil
}

func toolTweetTimeline(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		MaxResults int `json:"max_results"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	app := appFromCtx(ctx)
	if app == nil || app.Twitter == nil {
		return "", fmt.Errorf("twitter not configured; enable twitter in config and connect via OAuth")
	}

	tweets, err := app.Twitter.ReadTimeline(ctx, args.MaxResults)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{
		"count":  len(tweets),
		"tweets": tweets,
	})
	return string(b), nil
}

func toolTweetSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	app := appFromCtx(ctx)
	if app == nil || app.Twitter == nil {
		return "", fmt.Errorf("twitter not configured; enable twitter in config and connect via OAuth")
	}

	tweets, err := app.Twitter.SearchTweets(ctx, args.Query, args.MaxResults)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{
		"count":  len(tweets),
		"tweets": tweets,
	})
	return string(b), nil
}

func toolTweetReply(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		TweetID string `json:"tweet_id"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.TweetID == "" {
		return "", fmt.Errorf("tweet_id is required")
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	app := appFromCtx(ctx)
	if app == nil || app.Twitter == nil {
		return "", fmt.Errorf("twitter not configured; enable twitter in config and connect via OAuth")
	}

	tweet, err := app.Twitter.ReplyToTweet(ctx, args.TweetID, args.Text)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{
		"status": "replied",
		"tweet":  tweet,
	})
	return string(b), nil
}

func toolTweetDM(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		RecipientID string `json:"recipient_id"`
		Text        string `json:"text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.RecipientID == "" {
		return "", fmt.Errorf("recipient_id is required")
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	app := appFromCtx(ctx)
	if app == nil || app.Twitter == nil {
		return "", fmt.Errorf("twitter not configured; enable twitter in config and connect via OAuth")
	}

	if err := app.Twitter.SendDM(ctx, args.RecipientID, args.Text); err != nil {
		return "", err
	}

	return `{"status":"dm_sent"}`, nil
}

// --- Podcast tool handler stubs ---

func toolPodcastList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Podcast == nil {
		return "", fmt.Errorf("podcast service not initialized")
	}

	var args struct {
		Action  string `json:"action"`
		FeedURL string `json:"feedUrl"`
		GUID    string `json:"guid"`
		UserID  string `json:"userId"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}
	if args.Limit <= 0 {
		args.Limit = 10
	}

	svc := app.Podcast

	switch args.Action {
	case "subscribe":
		if err := svc.Subscribe(args.UserID, args.FeedURL); err != nil {
			return "", err
		}
		return fmt.Sprintf("Subscribed to %s", args.FeedURL), nil

	case "unsubscribe":
		if err := svc.Unsubscribe(args.UserID, args.FeedURL); err != nil {
			return "", err
		}
		return fmt.Sprintf("Unsubscribed from %s", args.FeedURL), nil

	case "list":
		feeds, err := svc.ListFeeds(args.UserID)
		if err != nil {
			return "", err
		}
		if len(feeds) == 0 {
			return "No podcast subscriptions.", nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Podcast subscriptions (%d):\n\n", len(feeds))
		for i, f := range feeds {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, f.Title)
			fmt.Fprintf(&sb, "   %s\n", f.FeedURL)
			if f.Description != "" {
				desc := f.Description
				if len(desc) > 100 {
					desc = desc[:100] + "..."
				}
				fmt.Fprintf(&sb, "   %s\n", desc)
			}
			sb.WriteString("\n")
		}
		return sb.String(), nil

	case "episodes":
		if args.FeedURL == "" {
			return "", fmt.Errorf("feedUrl required for episodes action")
		}
		episodes, err := svc.ListEpisodes(args.FeedURL, args.Limit)
		if err != nil {
			return "", err
		}
		if len(episodes) == 0 {
			return "No episodes found.", nil
		}
		return formatEpisodes(episodes), nil

	case "latest":
		episodes, err := svc.LatestEpisodes(args.UserID, args.Limit)
		if err != nil {
			return "", err
		}
		if len(episodes) == 0 {
			return "No new episodes.", nil
		}
		return formatEpisodes(episodes), nil

	case "played":
		if args.FeedURL == "" || args.GUID == "" {
			return "", fmt.Errorf("feedUrl and guid required for played action")
		}
		if err := svc.MarkPlayed(args.FeedURL, args.GUID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Marked episode %s as played.", args.GUID), nil

	default:
		return "", fmt.Errorf("unknown action %q — use subscribe, unsubscribe, list, episodes, latest, or played", args.Action)
	}
}

// --- HomeAssistant tool handler stubs ---

func toolHAListEntities(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.HA == nil {
		return "", fmt.Errorf("home assistant not configured")
	}

	var args struct {
		Domain string `json:"domain"`
	}
	json.Unmarshal(input, &args)

	entities, err := app.HA.ListEntities(args.Domain)
	if err != nil {
		return "", fmt.Errorf("list entities: %w", err)
	}

	type entitySummary struct {
		EntityID     string `json:"entity_id"`
		State        string `json:"state"`
		FriendlyName string `json:"friendly_name,omitempty"`
	}
	summaries := make([]entitySummary, 0, len(entities))
	for _, e := range entities {
		name, _ := e.Attributes["friendly_name"].(string)
		summaries = append(summaries, entitySummary{
			EntityID:     e.EntityID,
			State:        e.State,
			FriendlyName: name,
		})
	}

	b, _ := json.Marshal(summaries)
	return string(b), nil
}

func toolHAGetState(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.HA == nil {
		return "", fmt.Errorf("home assistant not configured")
	}

	var args struct {
		EntityID string `json:"entity_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.EntityID == "" {
		return "", fmt.Errorf("entity_id is required")
	}

	entity, err := app.HA.GetState(args.EntityID)
	if err != nil {
		return "", fmt.Errorf("get state: %w", err)
	}

	b, _ := json.Marshal(entity)
	return string(b), nil
}

func toolHACallService(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.HA == nil {
		return "", fmt.Errorf("home assistant not configured")
	}

	var args struct {
		Domain  string         `json:"domain"`
		Service string         `json:"service"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Domain == "" || args.Service == "" {
		return "", fmt.Errorf("domain and service are required")
	}

	if err := app.HA.CallService(args.Domain, args.Service, args.Data); err != nil {
		return "", fmt.Errorf("call service: %w", err)
	}

	return fmt.Sprintf("called %s/%s successfully", args.Domain, args.Service), nil
}

func toolHASetState(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.HA == nil {
		return "", fmt.Errorf("home assistant not configured")
	}

	var args struct {
		EntityID   string         `json:"entity_id"`
		State      string         `json:"state"`
		Attributes map[string]any `json:"attributes"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.EntityID == "" || args.State == "" {
		return "", fmt.Errorf("entity_id and state are required")
	}

	if err := app.HA.SetState(args.EntityID, args.State, args.Attributes); err != nil {
		return "", fmt.Errorf("set state: %w", err)
	}

	return fmt.Sprintf("set %s to %s", args.EntityID, args.State), nil
}

// --- Notes tool handler stubs ---

func toolNoteCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	if err := svc.CreateNote(args.Name, args.Content); err != nil {
		return "", err
	}

	result := map[string]any{
		"status": "created",
		"name":   args.Name,
		"path":   svc.FullPath(args.Name),
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

func toolNoteRead(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	content, err := svc.ReadNote(args.Name)
	if err != nil {
		return "", err
	}

	result := map[string]any{
		"name":    args.Name,
		"content": content,
		"tags":    extractTags(content),
		"links":   extractWikilinks(content),
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

func toolNoteAppend(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	if err := svc.AppendNote(args.Name, args.Content); err != nil {
		return "", err
	}

	result := map[string]any{
		"status": "appended",
		"name":   args.Name,
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

func toolNoteList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Prefix string `json:"prefix"`
	}
	json.Unmarshal(input, &args)

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	notesList, err := svc.ListNotes(args.Prefix)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(notesList)
	return string(b), nil
}

func toolNoteSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 5
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	results := svc.SearchNotes(args.Query, args.MaxResults)
	b, _ := json.Marshal(results)
	return string(b), nil
}

// --- File manager tool handler stubs ---

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

// ============================================================
// Merged shims: voice, mcp_host
// ============================================================

// --- Voice (from voice.go) ---

type STTProvider = voice.STTProvider
type STTOptions = voice.STTOptions
type STTResult = voice.STTResult
type TTSProvider = voice.TTSProvider
type TTSOptions = voice.TTSOptions
type OpenAISTTProvider = voice.OpenAISTTProvider
type OpenAITTSProvider = voice.OpenAITTSProvider
type ElevenLabsTTSProvider = voice.ElevenLabsTTSProvider
type VoiceEngine = voice.VoiceEngine

func newVoiceEngine(cfg *Config) *VoiceEngine {
	return voice.NewVoiceEngine(voice.VoiceConfig{
		STT: voice.STTConfig{
			Enabled:  cfg.Voice.STT.Enabled,
			Provider: cfg.Voice.STT.Provider,
			Model:    cfg.Voice.STT.Model,
			Endpoint: cfg.Voice.STT.Endpoint,
			APIKey:   cfg.Voice.STT.APIKey,
			Language: cfg.Voice.STT.Language,
		},
		TTS: voice.TTSConfig{
			Enabled:  cfg.Voice.TTS.Enabled,
			Provider: cfg.Voice.TTS.Provider,
			Model:    cfg.Voice.TTS.Model,
			Endpoint: cfg.Voice.TTS.Endpoint,
			APIKey:   cfg.Voice.TTS.APIKey,
			Voice:    cfg.Voice.TTS.Voice,
			Format:   cfg.Voice.TTS.Format,
		},
		Wake: voice.VoiceWakeConfig{
			Enabled:   cfg.Voice.Wake.Enabled,
			WakeWords: cfg.Voice.Wake.WakeWords,
			Threshold: cfg.Voice.Wake.Threshold,
		},
		Realtime: voice.VoiceRealtimeConfig{
			Enabled:  cfg.Voice.Realtime.Enabled,
			Provider: cfg.Voice.Realtime.Provider,
			Model:    cfg.Voice.Realtime.Model,
			APIKey:   cfg.Voice.Realtime.APIKey,
			Voice:    cfg.Voice.Realtime.Voice,
		},
	})
}

var _ interface {
	Transcribe(ctx context.Context, audio io.Reader, opts STTOptions) (*STTResult, error)
	Synthesize(ctx context.Context, text string, opts TTSOptions) (io.ReadCloser, error)
} = (*VoiceEngine)(nil)

// --- MCP Host (from mcp_host.go) ---

type MCPHost = mcp.Host
type MCPServer = mcp.Server
type MCPServerStatus = mcp.ServerStatus

type jsonRPCRequest = mcp.JSONRPCRequest
type jsonRPCResponse = mcp.JSONRPCResponse
type jsonRPCError = mcp.JSONRPCError

const mcpProtocolVersion = mcp.ProtocolVersion

type initializeResult struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type toolsListResult struct {
	Tools []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		InputSchema []byte `json:"inputSchema"`
	} `json:"tools"`
}

type toolsCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content"`
}

type initializeParams struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

type toolsCallParams struct {
	Name      string `json:"name"`
	Arguments []byte `json:"arguments"`
}

func newMCPHost(cfg *Config, toolReg *tools.Registry) *MCPHost {
	return mcp.NewHost(cfg, toolReg)
}

// ============================================================
// Merged shims: voice_realtime, embedding
// ============================================================

// --- Voice Realtime (from voice_realtime.go) ---

const (
	wsText   = voice.WSText
	wsBinary = voice.WSBinary
	wsClose  = voice.WSClose
	wsPing   = voice.WSPing
	wsPong   = voice.WSPong
)

type VoiceRealtimeEngine = voice.VoiceRealtimeEngine

func newVoiceRealtimeEngine(cfg *Config, ve *VoiceEngine) *VoiceRealtimeEngine {
	vcfg := voice.VoiceConfig{
		STT: voice.STTConfig{
			Enabled:  cfg.Voice.STT.Enabled,
			Provider: cfg.Voice.STT.Provider,
			Model:    cfg.Voice.STT.Model,
			Endpoint: cfg.Voice.STT.Endpoint,
			APIKey:   cfg.Voice.STT.APIKey,
			Language: cfg.Voice.STT.Language,
		},
		TTS: voice.TTSConfig{
			Enabled:  cfg.Voice.TTS.Enabled,
			Provider: cfg.Voice.TTS.Provider,
			Model:    cfg.Voice.TTS.Model,
			Endpoint: cfg.Voice.TTS.Endpoint,
			APIKey:   cfg.Voice.TTS.APIKey,
			Voice:    cfg.Voice.TTS.Voice,
			Format:   cfg.Voice.TTS.Format,
		},
		Wake: voice.VoiceWakeConfig{
			Enabled:   cfg.Voice.Wake.Enabled,
			WakeWords: cfg.Voice.Wake.WakeWords,
			Threshold: cfg.Voice.Wake.Threshold,
		},
		Realtime: voice.VoiceRealtimeConfig{
			Enabled:  cfg.Voice.Realtime.Enabled,
			Provider: cfg.Voice.Realtime.Provider,
			Model:    cfg.Voice.Realtime.Model,
			APIKey:   cfg.Voice.Realtime.APIKey,
			Voice:    cfg.Voice.Realtime.Voice,
		},
	}
	return voice.NewVoiceRealtimeEngine(vcfg, ve)
}

type toolRegistryAdapter struct {
	cfg *Config
	reg *ToolRegistry
}

func (a *toolRegistryAdapter) GetTool(name string) *voice.ToolEntry {
	tool, ok := a.reg.Get(name)
	if !ok {
		return nil
	}
	cfg := a.cfg
	return &voice.ToolEntry{
		Name:        tool.Name,
		Description: tool.Description,
		InputSchema: tool.InputSchema,
		Handler: func(ctx context.Context, argsJSON json.RawMessage) (string, error) {
			return tool.Handler(ctx, cfg, argsJSON)
		},
	}
}

func (a *toolRegistryAdapter) ListTools() []*voice.ToolEntry {
	defs := a.reg.List()
	entries := make([]*voice.ToolEntry, 0, len(defs))
	cfg := a.cfg
	for _, tool := range defs {
		t := tool
		entries = append(entries, &voice.ToolEntry{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Handler: func(ctx context.Context, argsJSON json.RawMessage) (string, error) {
				return t.Handler(ctx, cfg, argsJSON)
			},
		})
	}
	return entries
}

func wsUpgrade(w http.ResponseWriter, r *http.Request) (voice.WSConn, error) {
	return voice.WSUpgrade(w, r)
}

func generateSessionID() string {
	return voice.GenerateSessionID()
}

// --- Embedding (from embedding.go) ---

type EmbeddingSearchResult = knowledge.EmbeddingSearchResult
type embeddingRecord = knowledge.EmbeddingRecord

func embeddingCfg(cfg EmbeddingConfig) knowledge.EmbeddingConfig {
	return knowledge.EmbeddingConfig{
		Enabled:    cfg.Enabled,
		Provider:   cfg.Provider,
		Model:      cfg.Model,
		Endpoint:   cfg.Endpoint,
		APIKey:     cfg.APIKey,
		Dimensions: cfg.Dimensions,
		BatchSize:  cfg.BatchSize,
		MMR: knowledge.MMRConfig{
			Enabled: cfg.MMR.Enabled,
			Lambda:  cfg.MMR.Lambda,
		},
		TemporalDecay: knowledge.TemporalConfig{
			Enabled:      cfg.TemporalDecay.Enabled,
			HalfLifeDays: cfg.TemporalDecay.HalfLifeDays,
		},
	}
}

func initEmbeddingDB(dbPath string) error                              { return knowledge.InitEmbeddingDB(dbPath) }
func getEmbeddings(ctx context.Context, cfg *Config, texts []string) ([][]float32, error) {
	return knowledge.GetEmbeddings(ctx, embeddingCfg(cfg.Embedding), texts)
}
func getEmbedding(ctx context.Context, cfg *Config, text string) ([]float32, error) {
	return knowledge.GetEmbedding(ctx, embeddingCfg(cfg.Embedding), text)
}
func storeEmbedding(dbPath string, source, sourceID, content string, vec []float32, metadata map[string]interface{}) error {
	return knowledge.StoreEmbedding(dbPath, source, sourceID, content, vec, metadata)
}
func loadEmbeddings(dbPath, source string) ([]embeddingRecord, error) {
	return knowledge.LoadEmbeddings(dbPath, source)
}
func vectorSearch(dbPath string, queryVec []float32, source string, topK int) ([]EmbeddingSearchResult, error) {
	return knowledge.VectorSearch(dbPath, queryVec, source, topK)
}
func hybridSearch(ctx context.Context, cfg *Config, query string, source string, topK int) ([]EmbeddingSearchResult, error) {
	return knowledge.HybridSearch(ctx, embeddingCfg(cfg.Embedding), cfg.HistoryDB, cfg.KnowledgeDir, query, source, topK)
}
func reindexAll(ctx context.Context, cfg *Config) error {
	return knowledge.ReindexAll(ctx, embeddingCfg(cfg.Embedding), cfg.HistoryDB, cfg.KnowledgeDir)
}
func embeddingStatus(dbPath string) (map[string]interface{}, error) { return knowledge.EmbeddingStatus(dbPath) }
func cosineSimilarity(a, b []float32) float32                       { return knowledge.CosineSimilarity(a, b) }
func serializeVec(vec []float32) []byte                             { return knowledge.SerializeVec(vec) }
func deserializeVec(data []byte) []float32                          { return knowledge.DeserializeVec(data) }
func deserializeVecFromHex(hexStr string) []float32                 { return knowledge.DeserializeVecFromHex(hexStr) }
func contentHashSHA256(content string) string                       { return knowledge.ContentHashSHA256(content) }
func rrfMerge(a, b []EmbeddingSearchResult, k int) []EmbeddingSearchResult {
	return knowledge.RRFMerge(a, b, k)
}
func mmrRerank(results []EmbeddingSearchResult, queryVec []float32, lambda float64, topK int) []EmbeddingSearchResult {
	return knowledge.MMRRerank(results, queryVec, lambda, topK)
}
func contentToVec(content string, dims int) []float32 { return knowledge.ContentToVec(content, dims) }
func temporalDecay(score float64, createdAt time.Time, halfLifeDays float64) float64 {
	return knowledge.TemporalDecay(score, createdAt, halfLifeDays)
}
func chunkText(text string, maxChars, overlap int) []string { return knowledge.ChunkText(text, maxChars, overlap) }
func embeddingMMRLambdaOrDefault(cfg EmbeddingConfig) float64 {
	return knowledge.EmbeddingConfig(embeddingCfg(cfg)).MmrLambdaOrDefault()
}
func embeddingDecayHalfLifeOrDefault(cfg EmbeddingConfig) float64 {
	return knowledge.EmbeddingConfig(embeddingCfg(cfg)).DecayHalfLifeOrDefault()
}

// ============================================================
// Merged from oauth.go
// ============================================================

// --- OAuth Type aliases ---

type OAuthManager = iOAuth.OAuthManager
type OAuthToken = iOAuth.OAuthToken
type OAuthTokenStatus = iOAuth.OAuthTokenStatus

var globalOAuthManager *OAuthManager

var oauthTemplates = iOAuth.OAuthTemplates

func newOAuthManager(cfg *Config) *OAuthManager {
	iOAuth.EncryptFn = tcrypto.Encrypt
	iOAuth.DecryptFn = tcrypto.Decrypt
	return iOAuth.NewOAuthManager(cfg.OAuth, cfg.HistoryDB, cfg.ListenAddr)
}

func initOAuthTable(dbPath string) error {
	return iOAuth.InitOAuthTable(dbPath)
}

func encryptOAuthToken(plaintext, key string) (string, error) {
	return tcrypto.Encrypt(plaintext, key)
}

func decryptOAuthToken(ciphertextHex, key string) (string, error) {
	return tcrypto.Decrypt(ciphertextHex, key)
}

func storeOAuthToken(dbPath string, token OAuthToken, encKey string) error {
	return iOAuth.StoreOAuthToken(dbPath, token, encKey)
}

func loadOAuthToken(dbPath, serviceName, encKey string) (*OAuthToken, error) {
	return iOAuth.LoadOAuthToken(dbPath, serviceName, encKey)
}

func deleteOAuthToken(dbPath, serviceName string) error {
	return iOAuth.DeleteOAuthToken(dbPath, serviceName)
}

func listOAuthTokenStatuses(dbPath, encKey string) ([]OAuthTokenStatus, error) {
	return iOAuth.ListOAuthTokenStatuses(dbPath, encKey)
}

func generateState() (string, error) {
	return iOAuth.GenerateState()
}

func toolOAuthStatus(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	statuses, err := listOAuthTokenStatuses(cfg.HistoryDB, cfg.OAuth.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("list oauth statuses: %w", err)
	}

	if len(statuses) == 0 {
		return "No OAuth services connected. Configure services in config.json under \"oauth.services\" and use the authorize endpoint to connect.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Connected OAuth services (%d):\n", len(statuses)))
	for _, s := range statuses {
		status := "connected"
		if s.ExpiresSoon {
			status = "expires soon"
		}
		sb.WriteString(fmt.Sprintf("  - %s: %s", s.ServiceName, status))
		if s.Scopes != "" {
			sb.WriteString(fmt.Sprintf(" (scopes: %s)", s.Scopes))
		}
		if s.ExpiresAt != "" {
			sb.WriteString(fmt.Sprintf(" (expires: %s)", s.ExpiresAt))
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

func toolOAuthRequest(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Service string `json:"service"`
		Method  string `json:"method"`
		URL     string `json:"url"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.Service == "" || args.URL == "" {
		return "", fmt.Errorf("service and url are required")
	}
	if args.Method == "" {
		args.Method = "GET"
	}

	app := appFromCtx(ctx)
	var mgr *OAuthManager
	if app != nil && app.OAuth != nil {
		mgr = app.OAuth
	} else {
		mgr = newOAuthManager(cfg)
	}
	var body io.Reader
	if args.Body != "" {
		body = strings.NewReader(args.Body)
	}

	resp, err := mgr.Request(ctx, args.Service, args.Method, args.URL, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	return fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(respBody)), nil
}

func toolOAuthAuthorize(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Service string `json:"service"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.Service == "" {
		return "", fmt.Errorf("service is required")
	}

	app := appFromCtx(ctx)
	var mgr *OAuthManager
	if app != nil && app.OAuth != nil {
		mgr = app.OAuth
	} else {
		mgr = newOAuthManager(cfg)
	}
	svcCfg, err := mgr.ResolveServiceConfig(args.Service)
	if err != nil {
		return "", err
	}

	redirectURL := svcCfg.RedirectURL
	if redirectURL == "" {
		base := cfg.OAuth.RedirectBase
		if base == "" {
			base = "http://localhost" + cfg.ListenAddr
		}
		redirectURL = base + "/api/oauth/" + args.Service + "/callback"
	}

	params := url.Values{
		"client_id":     {svcCfg.ClientID},
		"redirect_uri":  {redirectURL},
		"response_type": {"code"},
	}
	if len(svcCfg.Scopes) > 0 {
		params.Set("scope", strings.Join(svcCfg.Scopes, " "))
	}
	for k, v := range svcCfg.ExtraParams {
		params.Set(k, v)
	}

	authorizeURL := fmt.Sprintf("%s/api/oauth/%s/authorize", strings.TrimRight(cfg.OAuth.RedirectBase, "/"), args.Service)
	if cfg.OAuth.RedirectBase == "" {
		authorizeURL = fmt.Sprintf("http://localhost%s/api/oauth/%s/authorize", cfg.ListenAddr, args.Service)
	}

	return fmt.Sprintf("To connect %s, visit this URL:\n%s\n\nThe authorization flow will handle CSRF protection and token exchange automatically.", args.Service, authorizeURL), nil
}

// Ensure *http.Response is used so the import is not flagged unused.
var _ *http.Response

// ============================================================
// Merged from gcalendar.go
// ============================================================

var globalCalendarService *CalendarService

func toolCalendarList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if !cfg.Calendar.Enabled {
		return "", fmt.Errorf("calendar integration is not enabled")
	}
	app := appFromCtx(ctx)
	if app == nil || app.Calendar == nil {
		return "", fmt.Errorf("calendar service not initialized")
	}

	var args struct {
		TimeMin    string `json:"timeMin"`
		TimeMax    string `json:"timeMax"`
		MaxResults int    `json:"maxResults"`
		Days       int    `json:"days"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if args.TimeMin == "" && args.TimeMax == "" {
		now := time.Now()
		args.TimeMin = now.Format(time.RFC3339)
		days := 7
		if args.Days > 0 {
			days = args.Days
		}
		args.TimeMax = now.AddDate(0, 0, days).Format(time.RFC3339)
	}

	events, err := app.Calendar.ListEvents(ctx, args.TimeMin, args.TimeMax, args.MaxResults)
	if err != nil {
		return "", err
	}

	if len(events) == 0 {
		return "No upcoming events found.", nil
	}

	out, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Found %d events:\n%s", len(events), string(out)), nil
}

func toolCalendarCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if !cfg.Calendar.Enabled {
		return "", fmt.Errorf("calendar integration is not enabled")
	}
	app := appFromCtx(ctx)
	if app == nil || app.Calendar == nil {
		return "", fmt.Errorf("calendar service not initialized")
	}

	var args struct {
		Summary     string   `json:"summary"`
		Description string   `json:"description"`
		Location    string   `json:"location"`
		Start       string   `json:"start"`
		End         string   `json:"end"`
		TimeZone    string   `json:"timeZone"`
		Attendees   []string `json:"attendees"`
		AllDay      bool     `json:"allDay"`
		Text        string   `json:"text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	var eventInput CalendarEventInput

	if args.Text != "" {
		parsed, err := parseNaturalSchedule(args.Text)
		if err != nil {
			return "", fmt.Errorf("cannot parse schedule: %w", err)
		}
		eventInput = *parsed
	} else {
		if args.Summary == "" {
			return "", fmt.Errorf("summary is required")
		}
		if args.Start == "" {
			return "", fmt.Errorf("start time is required")
		}
		eventInput = CalendarEventInput{
			Summary:     args.Summary,
			Description: args.Description,
			Location:    args.Location,
			Start:       args.Start,
			End:         args.End,
			TimeZone:    args.TimeZone,
			Attendees:   args.Attendees,
			AllDay:      args.AllDay,
		}
	}

	if eventInput.TimeZone == "" {
		eventInput.TimeZone = app.Calendar.TimeZone()
	}

	ev, err := app.Calendar.CreateEvent(ctx, eventInput)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(ev, "", "  ")
	return fmt.Sprintf("Event created:\n%s", string(out)), nil
}

func toolCalendarUpdate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if !cfg.Calendar.Enabled {
		return "", fmt.Errorf("calendar integration is not enabled")
	}
	app := appFromCtx(ctx)
	if app == nil || app.Calendar == nil {
		return "", fmt.Errorf("calendar service not initialized")
	}

	var args struct {
		EventID     string   `json:"eventId"`
		Summary     string   `json:"summary"`
		Description string   `json:"description"`
		Location    string   `json:"location"`
		Start       string   `json:"start"`
		End         string   `json:"end"`
		TimeZone    string   `json:"timeZone"`
		Attendees   []string `json:"attendees"`
		AllDay      bool     `json:"allDay"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if args.EventID == "" {
		return "", fmt.Errorf("eventId is required")
	}

	eventInput := CalendarEventInput{
		Summary:     args.Summary,
		Description: args.Description,
		Location:    args.Location,
		Start:       args.Start,
		End:         args.End,
		TimeZone:    args.TimeZone,
		Attendees:   args.Attendees,
		AllDay:      args.AllDay,
	}

	if eventInput.TimeZone == "" {
		eventInput.TimeZone = app.Calendar.TimeZone()
	}

	ev, err := app.Calendar.UpdateEvent(ctx, args.EventID, eventInput)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(ev, "", "  ")
	return fmt.Sprintf("Event updated:\n%s", string(out)), nil
}

func toolCalendarDelete(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if !cfg.Calendar.Enabled {
		return "", fmt.Errorf("calendar integration is not enabled")
	}
	app := appFromCtx(ctx)
	if app == nil || app.Calendar == nil {
		return "", fmt.Errorf("calendar service not initialized")
	}

	var args struct {
		EventID string `json:"eventId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if args.EventID == "" {
		return "", fmt.Errorf("eventId is required")
	}

	if err := app.Calendar.DeleteEvent(ctx, args.EventID); err != nil {
		return "", err
	}

	return fmt.Sprintf("Event %s deleted successfully.", args.EventID), nil
}

func toolCalendarSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if !cfg.Calendar.Enabled {
		return "", fmt.Errorf("calendar integration is not enabled")
	}
	app := appFromCtx(ctx)
	if app == nil || app.Calendar == nil {
		return "", fmt.Errorf("calendar service not initialized")
	}

	var args struct {
		Query   string `json:"query"`
		TimeMin string `json:"timeMin"`
		TimeMax string `json:"timeMax"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	if args.TimeMin == "" {
		args.TimeMin = time.Now().AddDate(0, 0, -30).Format(time.RFC3339)
	}
	if args.TimeMax == "" {
		args.TimeMax = time.Now().AddDate(0, 0, 90).Format(time.RFC3339)
	}

	events, err := app.Calendar.SearchEvents(ctx, args.Query, args.TimeMin, args.TimeMax)
	if err != nil {
		return "", err
	}

	if len(events) == 0 {
		return fmt.Sprintf("No events found matching %q.", args.Query), nil
	}

	out, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Found %d events matching %q:\n%s", len(events), args.Query, string(out)), nil
}

// ============================================================
// Merged from crypto.go
// ============================================================

var (
	globalEncKeyMu  sync.RWMutex
	globalEncKeyVal string
)

func setGlobalEncryptionKey(key string) {
	globalEncKeyMu.Lock()
	globalEncKeyVal = key
	globalEncKeyMu.Unlock()
}

func globalEncryptionKey() string {
	globalEncKeyMu.RLock()
	defer globalEncKeyMu.RUnlock()
	return globalEncKeyVal
}

func encryptField(cfg *Config, value string) string {
	key := resolveEncryptionKey(cfg)
	if key == "" || value == "" {
		return value
	}
	enc, err := tcrypto.Encrypt(value, key)
	if err != nil {
		return value
	}
	return enc
}

func decryptField(cfg *Config, value string) string {
	key := resolveEncryptionKey(cfg)
	if key == "" || value == "" {
		return value
	}
	dec, err := tcrypto.Decrypt(value, key)
	if err != nil {
		return value
	}
	return dec
}

func resolveEncryptionKey(cfg *Config) string {
	if cfg.EncryptionKey != "" {
		return cfg.EncryptionKey
	}
	return cfg.OAuth.EncryptionKey
}

func cmdMigrateEncrypt() {
	cfg := loadConfig(findConfigPath())
	key := resolveEncryptionKey(cfg)
	if key == "" {
		fmt.Fprintln(os.Stderr, "Error: no encryptionKey configured. Set it in config.json first.")
		os.Exit(1)
	}

	dbPath := cfg.HistoryDB
	if dbPath == "" {
		fmt.Fprintln(os.Stderr, "Error: no historyDB configured.")
		os.Exit(1)
	}

	total := 0

	rows, err := db.Query(dbPath, `SELECT id, content FROM session_messages WHERE content != ''`)
	if err == nil {
		for _, row := range rows {
			content := jsonStr(row["content"])
			if content == "" {
				continue
			}
			if _, decErr := hex.DecodeString(content); decErr == nil {
				continue
			}
			enc, err := tcrypto.Encrypt(content, key)
			if err != nil {
				continue
			}
			id := int(jsonFloat(row["id"]))
			updateSQL := fmt.Sprintf(`UPDATE session_messages SET content = '%s' WHERE id = %d`,
				db.Escape(enc), id)
			db.Query(dbPath, updateSQL)
			total++
		}
	}
	fmt.Printf("Encrypted %d session messages\n", total)

	contactCount := 0
	rows, err = db.Query(dbPath, `SELECT id, email, phone, notes FROM contacts`)
	if err == nil {
		for _, row := range rows {
			id := jsonStr(row["id"])
			email := jsonStr(row["email"])
			phone := jsonStr(row["phone"])
			notesVal := jsonStr(row["notes"])

			updates := []string{}
			if email != "" {
				if _, decErr := hex.DecodeString(email); decErr != nil {
					if enc, err := tcrypto.Encrypt(email, key); err == nil {
						updates = append(updates, fmt.Sprintf("email = '%s'", db.Escape(enc)))
					}
				}
			}
			if phone != "" {
				if _, decErr := hex.DecodeString(phone); decErr != nil {
					if enc, err := tcrypto.Encrypt(phone, key); err == nil {
						updates = append(updates, fmt.Sprintf("phone = '%s'", db.Escape(enc)))
					}
				}
			}
			if notesVal != "" {
				if _, decErr := hex.DecodeString(notesVal); decErr != nil {
					if enc, err := tcrypto.Encrypt(notesVal, key); err == nil {
						updates = append(updates, fmt.Sprintf("notes = '%s'", db.Escape(enc)))
					}
				}
			}
			if len(updates) > 0 {
				sqlStr := fmt.Sprintf("UPDATE contacts SET %s WHERE id = '%s'",
					strings.Join(updates, ", "), db.Escape(id))
				db.Query(dbPath, sqlStr)
				contactCount++
			}
		}
	}
	fmt.Printf("Encrypted %d contacts\n", contactCount)

	expenseCount := 0
	rows, err = db.Query(dbPath, `SELECT rowid, description FROM expenses WHERE description != ''`)
	if err == nil {
		for _, row := range rows {
			desc := jsonStr(row["description"])
			if desc == "" {
				continue
			}
			if _, decErr := hex.DecodeString(desc); decErr == nil {
				continue
			}
			enc, err := tcrypto.Encrypt(desc, key)
			if err != nil {
				continue
			}
			id := int(jsonFloat(row["rowid"]))
			updateSQL := fmt.Sprintf(`UPDATE expenses SET description = '%s' WHERE rowid = %d`,
				db.Escape(enc), id)
			db.Query(dbPath, updateSQL)
			expenseCount++
		}
	}
	fmt.Printf("Encrypted %d expenses\n", expenseCount)

	habitCount := 0
	rows, err = db.Query(dbPath, `SELECT id, note FROM habit_logs WHERE note != ''`)
	if err == nil {
		for _, row := range rows {
			note := jsonStr(row["note"])
			if note == "" {
				continue
			}
			if _, decErr := hex.DecodeString(note); decErr == nil {
				continue
			}
			enc, err := tcrypto.Encrypt(note, key)
			if err != nil {
				continue
			}
			id := jsonStr(row["id"])
			updateSQL := fmt.Sprintf(`UPDATE habit_logs SET note = '%s' WHERE id = '%s'`,
				db.Escape(enc), db.Escape(id))
			db.Query(dbPath, updateSQL)
			habitCount++
		}
	}
	fmt.Printf("Encrypted %d habit logs\n", habitCount)

	fmt.Printf("\nTotal: %d rows encrypted\n", total+contactCount+expenseCount+habitCount)
}

// ============================================================
// Merged from mcp.go
// ============================================================

// MCPConfigInfo represents summary info about an MCP server config.
type MCPConfigInfo struct {
	Name    string          `json:"name"`
	Command string          `json:"command,omitempty"`
	Args    string          `json:"args,omitempty"`
	Config  json.RawMessage `json:"config"`
}

func listMCPConfigs(cfg *Config) []MCPConfigInfo {
	cfg.MCPMu.RLock()
	defer cfg.MCPMu.RUnlock()

	if len(cfg.MCPConfigs) == 0 {
		return nil
	}

	var configs []MCPConfigInfo
	for name, raw := range cfg.MCPConfigs {
		cmd, mcpArgs := extractMCPSummary(raw)
		configs = append(configs, MCPConfigInfo{
			Name:    name,
			Command: cmd,
			Args:    mcpArgs,
			Config:  raw,
		})
	}

	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Name < configs[j].Name
	})
	return configs
}

func getMCPConfig(cfg *Config, name string) (json.RawMessage, error) {
	cfg.MCPMu.RLock()
	defer cfg.MCPMu.RUnlock()

	raw, ok := cfg.MCPConfigs[name]
	if !ok {
		return nil, fmt.Errorf("MCP config %q not found", name)
	}
	return raw, nil
}

func setMCPConfig(cfg *Config, configPath, name string, config json.RawMessage) error {
	if name == "" {
		return fmt.Errorf("MCP name is required")
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("invalid character %q in MCP name (use a-z, 0-9, -, _)", string(r))
		}
	}
	if !json.Valid(config) {
		return fmt.Errorf("invalid JSON config")
	}

	if err := updateConfigMCPs(configPath, name, config); err != nil {
		return err
	}

	mcpDir := filepath.Join(cfg.BaseDir, "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		return fmt.Errorf("create mcp dir: %w", err)
	}
	path := filepath.Join(mcpDir, name+".json")
	if err := os.WriteFile(path, config, 0o644); err != nil {
		return fmt.Errorf("write mcp file %q: %w", path, err)
	}

	cfg.MCPMu.Lock()
	if cfg.MCPConfigs == nil {
		cfg.MCPConfigs = make(map[string]json.RawMessage)
	}
	cfg.MCPConfigs[name] = config
	if cfg.MCPPaths == nil {
		cfg.MCPPaths = make(map[string]string)
	}
	cfg.MCPPaths[name] = path
	cfg.MCPMu.Unlock()

	return nil
}

func deleteMCPConfig(cfg *Config, configPath, name string) error {
	cfg.MCPMu.RLock()
	_, ok := cfg.MCPConfigs[name]
	cfg.MCPMu.RUnlock()
	if !ok {
		return fmt.Errorf("MCP config %q not found", name)
	}

	if err := updateConfigMCPs(configPath, name, nil); err != nil {
		return err
	}

	cfg.MCPMu.Lock()
	var filePath string
	if p, ok := cfg.MCPPaths[name]; ok {
		filePath = p
		delete(cfg.MCPPaths, name)
	} else {
		filePath = filepath.Join(cfg.BaseDir, "mcp", name+".json")
	}
	delete(cfg.MCPConfigs, name)
	cfg.MCPMu.Unlock()

	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove mcp file %q: %w", filePath, err)
	}

	return nil
}

func testMCPConfig(raw json.RawMessage) (bool, string) {
	cmd, mcpArgs := extractMCPSummary(raw)
	if cmd == "" {
		return false, "could not extract command from config"
	}

	cmdPath, err := exec.LookPath(cmd)
	if err != nil {
		return false, fmt.Sprintf("command %q not found in PATH", cmd)
	}

	var cmdArgsList []string
	if mcpArgs != "" {
		cmdArgsList = strings.Fields(mcpArgs)
	}
	proc := exec.Command(cmdPath, cmdArgsList...)
	proc.Env = os.Environ()

	if err := proc.Start(); err != nil {
		return false, fmt.Sprintf("failed to start: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			return false, fmt.Sprintf("process exited: %v", err)
		}
		return true, fmt.Sprintf("OK: %s (%s)", cmd, cmdPath)
	case <-time.After(2 * time.Second):
		proc.Process.Kill()
		return true, fmt.Sprintf("OK: %s started successfully (%s)", cmd, cmdPath)
	}
}

func extractMCPSummary(raw json.RawMessage) (command, args string) {
	var wrapper struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &wrapper) == nil && len(wrapper.MCPServers) > 0 {
		for _, srv := range wrapper.MCPServers {
			return srv.Command, strings.Join(srv.Args, " ")
		}
	}

	var flat struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if json.Unmarshal(raw, &flat) == nil && flat.Command != "" {
		return flat.Command, strings.Join(flat.Args, " ")
	}

	return "", ""
}
