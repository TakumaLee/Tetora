package main

// wire_integration.go wires the integration service internal packages to the root
// package by providing constructors, type aliases, and OAuth adapters that keep the
// root API surface stable.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

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
	"tetora/internal/storage"
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
	tok, err := a.mgr.refreshTokenIfNeeded(service)
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
		LogInfo: logInfo,
		LogWarn: logWarn,
	})
}

func newHAService(cfg HomeAssistantConfig) *HAService {
	return homeassistant.New(cfg, logInfo, logWarn, logDebug)
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
	return notes.New(cfg.Notes, cfg.BaseDir, cfg.Embedding.Enabled, embedFn, logInfo, logWarn, logDebug)
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
)

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
			logInfo("spotify play search result", "query", args.Query, "track", results[0].Name, "artist", results[0].Artist)
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
