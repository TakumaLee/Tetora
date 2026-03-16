package main

// wire_integration.go wires the integration service internal packages to the root
// package by providing constructors, type aliases, and OAuth adapters that keep the
// root API surface stable.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"tetora/internal/integration/drive"
	"tetora/internal/integration/dropbox"
	"tetora/internal/integration/gmail"
	"tetora/internal/integration/oauthif"
)

// --- Config type aliases ---

type GmailConfig = gmail.Config

// --- Service type aliases ---

type GmailService = gmail.Service
type DriveService = drive.Service
type DropboxService = dropbox.Service

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

// --- Global singletons (backwards compat) ---

var (
	globalGmailService   *GmailService
	globalDriveService   *DriveService
	globalDropboxService *DropboxService
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
		args.MimeType = mimeFromExt(args.Name)
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
