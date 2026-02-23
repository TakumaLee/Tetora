package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// --- P23.5: YouTube Subtitle Extraction & Video Summary ---

// YouTubeConfig holds YouTube tools settings.
type YouTubeConfig struct {
	Enabled   bool   `json:"enabled"`
	YtDlpPath string `json:"ytDlpPath,omitempty"` // default "yt-dlp"
}

func (c YouTubeConfig) ytDlpOrDefault() string {
	if c.YtDlpPath != "" {
		return c.YtDlpPath
	}
	return "yt-dlp"
}

// YouTubeVideoInfo holds metadata about a YouTube video.
type YouTubeVideoInfo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Channel     string `json:"channel"`
	Duration    int    `json:"duration"` // seconds
	Description string `json:"description"`
	UploadDate  string `json:"upload_date"`
	ViewCount   int    `json:"view_count"`
}

// extractYouTubeSubtitles downloads and parses subtitles for a YouTube video.
func extractYouTubeSubtitles(videoURL string, lang string, ytDlpPath string) (string, error) {
	if videoURL == "" {
		return "", fmt.Errorf("video URL required")
	}
	if lang == "" {
		lang = "en"
	}
	if ytDlpPath == "" {
		ytDlpPath = "yt-dlp"
	}

	// Create temp directory for subtitle files.
	tmpDir, err := os.MkdirTemp("", "tetora-yt-sub-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	outTemplate := filepath.Join(tmpDir, "sub")

	// Run yt-dlp to download subtitles.
	cmd := exec.Command(ytDlpPath,
		"--write-auto-sub",
		"--sub-lang", lang,
		"--skip-download",
		"--sub-format", "vtt",
		"-o", outTemplate,
		videoURL,
	)
	cmd.Stderr = nil // suppress stderr
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("yt-dlp subtitle extraction failed: %s: %w", string(out), err)
	}

	// Find the VTT file (yt-dlp adds language suffix).
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return "", fmt.Errorf("read temp dir: %w", err)
	}

	var vttPath string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".vtt") {
			vttPath = filepath.Join(tmpDir, e.Name())
			break
		}
	}
	if vttPath == "" {
		return "", fmt.Errorf("no subtitle file found (language %q may not be available)", lang)
	}

	data, err := os.ReadFile(vttPath)
	if err != nil {
		return "", fmt.Errorf("read VTT file: %w", err)
	}

	return parseVTT(string(data)), nil
}

// vttTimestampRe matches VTT timestamp lines (e.g., "00:00:01.000 --> 00:00:05.000").
var vttTimestampRe = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}\.\d{3}\s*-->`)

// vttTagRe matches VTT tags like <c>, </c>, <00:00:01.000>, etc.
var vttTagRe = regexp.MustCompile(`<[^>]+>`)

// parseVTT parses a WebVTT file and returns clean text without timestamps or duplicates.
func parseVTT(content string) string {
	lines := strings.Split(content, "\n")
	seen := make(map[string]bool)
	var result []string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines, WEBVTT header, NOTE blocks, and timestamp lines.
		if line == "" || line == "WEBVTT" || strings.HasPrefix(line, "Kind:") ||
			strings.HasPrefix(line, "Language:") || strings.HasPrefix(line, "NOTE") {
			continue
		}

		// Skip timestamp lines.
		if vttTimestampRe.MatchString(line) {
			continue
		}

		// Skip numeric cue identifiers.
		if isNumericLine(line) {
			continue
		}

		// Remove VTT formatting tags.
		cleaned := vttTagRe.ReplaceAllString(line, "")
		cleaned = strings.TrimSpace(cleaned)

		if cleaned == "" {
			continue
		}

		// Deduplicate lines (auto-subs repeat a lot).
		if !seen[cleaned] {
			seen[cleaned] = true
			result = append(result, cleaned)
		}
	}

	return strings.Join(result, "\n")
}

// isNumericLine checks if a line is purely numeric (VTT cue identifier).
func isNumericLine(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// getYouTubeVideoInfo fetches video metadata using yt-dlp --dump-json.
func getYouTubeVideoInfo(videoURL string, ytDlpPath string) (*YouTubeVideoInfo, error) {
	if videoURL == "" {
		return nil, fmt.Errorf("video URL required")
	}
	if ytDlpPath == "" {
		ytDlpPath = "yt-dlp"
	}

	cmd := exec.Command(ytDlpPath, "--dump-json", "--no-download", videoURL)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("yt-dlp metadata failed: %s: %w", string(exitErr.Stderr), err)
		}
		return nil, fmt.Errorf("yt-dlp metadata failed: %w", err)
	}

	return parseYouTubeVideoJSON(out)
}

// parseYouTubeVideoJSON parses yt-dlp --dump-json output into YouTubeVideoInfo.
func parseYouTubeVideoJSON(data []byte) (*YouTubeVideoInfo, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse video JSON: %w", err)
	}

	info := &YouTubeVideoInfo{}

	if v, ok := raw["id"].(string); ok {
		info.ID = v
	}
	if v, ok := raw["title"].(string); ok {
		info.Title = v
	}
	if v, ok := raw["channel"].(string); ok {
		info.Channel = v
	} else if v, ok := raw["uploader"].(string); ok {
		info.Channel = v
	}
	if v, ok := raw["duration"].(float64); ok {
		info.Duration = int(v)
	}
	if v, ok := raw["description"].(string); ok {
		info.Description = v
	}
	if v, ok := raw["upload_date"].(string); ok {
		info.UploadDate = v
	}
	if v, ok := raw["view_count"].(float64); ok {
		info.ViewCount = int(v)
	}

	return info, nil
}

// summarizeYouTubeVideo truncates subtitles to a given word limit.
func summarizeYouTubeVideo(subtitles string, maxWords int) string {
	if maxWords <= 0 {
		maxWords = 500
	}

	words := strings.Fields(subtitles)
	if len(words) <= maxWords {
		return subtitles
	}

	return strings.Join(words[:maxWords], " ") + "..."
}

// formatDuration formats seconds into "HH:MM:SS" or "MM:SS".
func formatYTDuration(seconds int) string {
	if seconds <= 0 {
		return "0:00"
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// formatViewCount formats a view count with commas.
func formatViewCount(count int) string {
	if count <= 0 {
		return "0"
	}
	s := fmt.Sprintf("%d", count)
	if len(s) <= 3 {
		return s
	}

	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// --- Tool Handler ---

// toolYouTubeSummary extracts subtitles and video info, returns a formatted summary.
func toolYouTubeSummary(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		URL      string `json:"url"`
		Lang     string `json:"lang"`
		MaxWords int    `json:"maxWords"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.URL == "" {
		return "", fmt.Errorf("url required")
	}
	if args.Lang == "" {
		args.Lang = "en"
	}
	if args.MaxWords <= 0 {
		args.MaxWords = 500
	}

	// Default yt-dlp path
	ytDlpPath := "yt-dlp"

	// Try to get video info first (non-blocking if yt-dlp unavailable).
	var info *YouTubeVideoInfo
	infoData, infoErr := func() (*YouTubeVideoInfo, error) {
		return getYouTubeVideoInfo(args.URL, ytDlpPath)
	}()
	if infoErr == nil {
		info = infoData
	}

	// Extract subtitles.
	subtitles, subErr := extractYouTubeSubtitles(args.URL, args.Lang, ytDlpPath)
	if subErr != nil {
		// If we have video info but no subtitles, still return info.
		if info != nil {
			var sb strings.Builder
			writeVideoHeader(&sb, info)
			fmt.Fprintf(&sb, "\nSubtitles not available in %q.\n", args.Lang)
			if info.Description != "" {
				sb.WriteString("\nDescription:\n")
				sb.WriteString(summarizeYouTubeVideo(info.Description, args.MaxWords))
				sb.WriteString("\n")
			}
			return sb.String(), nil
		}
		return "", fmt.Errorf("subtitle extraction failed: %w", subErr)
	}

	summary := summarizeYouTubeVideo(subtitles, args.MaxWords)

	var sb strings.Builder
	if info != nil {
		writeVideoHeader(&sb, info)
		sb.WriteString("\n")
	}

	sb.WriteString("Transcript summary:\n")
	sb.WriteString(summary)
	sb.WriteString("\n")

	wordCount := len(strings.Fields(subtitles))
	if wordCount > args.MaxWords {
		fmt.Fprintf(&sb, "\n[Showing %d of %d words]\n", args.MaxWords, wordCount)
	}

	return sb.String(), nil
}

// writeVideoHeader writes formatted video metadata to a string builder.
func writeVideoHeader(sb *strings.Builder, info *YouTubeVideoInfo) {
	fmt.Fprintf(sb, "Title: %s\n", info.Title)
	if info.Channel != "" {
		fmt.Fprintf(sb, "Channel: %s\n", info.Channel)
	}
	if info.Duration > 0 {
		fmt.Fprintf(sb, "Duration: %s\n", formatYTDuration(info.Duration))
	}
	if info.ViewCount > 0 {
		fmt.Fprintf(sb, "Views: %s\n", formatViewCount(info.ViewCount))
	}
	if info.UploadDate != "" {
		fmt.Fprintf(sb, "Uploaded: %s\n", info.UploadDate)
	}
}
