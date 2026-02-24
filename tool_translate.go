package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// Base URLs for translation APIs (overridable in tests).
var (
	lingvaBaseURL = "https://lingva.ml"
	deeplBaseURL  = "https://api-free.deepl.com"
)

func toolTranslate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Text string `json:"text"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}
	if args.To == "" {
		return "", fmt.Errorf("target language (to) is required")
	}
	if args.From == "" {
		args.From = "auto"
	}

	provider := strings.ToLower(cfg.Translate.Provider)
	if provider == "" {
		provider = "lingva"
	}

	switch provider {
	case "deepl":
		return translateDeepL(args.Text, args.From, args.To, cfg.Translate.APIKey)
	default:
		return translateLingva(args.Text, args.From, args.To)
	}
}

func translateLingva(text, from, to string) (string, error) {
	apiURL := fmt.Sprintf("%s/api/v1/%s/%s/%s",
		lingvaBaseURL,
		url.PathEscape(strings.ToLower(from)),
		url.PathEscape(strings.ToLower(to)),
		url.PathEscape(text))

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("lingva API error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("lingva API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Translation string `json:"translation"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode error: %w", err)
	}

	return fmt.Sprintf("[%s -> %s] %s", from, to, result.Translation), nil
}

func translateDeepL(text, from, to, apiKey string) (string, error) {
	if apiKey == "" {
		return "", fmt.Errorf("DeepL API key required (set translate.apiKey in config)")
	}

	form := url.Values{}
	form.Set("text", text)
	form.Set("target_lang", strings.ToUpper(to))
	if from != "" && from != "auto" {
		form.Set("source_lang", strings.ToUpper(from))
	}

	apiURL := deeplBaseURL + "/v2/translate"
	req, err := http.NewRequest("POST", apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create request error: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "DeepL-Auth-Key "+apiKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("deepl API error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("deepl API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Translations []struct {
			DetectedSourceLang string `json:"detected_source_language"`
			Text               string `json:"text"`
		} `json:"translations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode error: %w", err)
	}
	if len(result.Translations) == 0 {
		return "", fmt.Errorf("no translation returned")
	}

	t := result.Translations[0]
	srcLang := from
	if t.DetectedSourceLang != "" {
		srcLang = strings.ToLower(t.DetectedSourceLang)
	}
	return fmt.Sprintf("[%s -> %s] %s", srcLang, to, t.Text), nil
}

func toolDetectLanguage(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	provider := strings.ToLower(cfg.Translate.Provider)
	if provider == "deepl" && cfg.Translate.APIKey != "" {
		return detectLanguageDeepL(args.Text, cfg.Translate.APIKey)
	}

	// Heuristic-based detection.
	return detectLanguageHeuristic(args.Text), nil
}

func detectLanguageDeepL(text, apiKey string) (string, error) {
	// DeepL detects language as part of translation; translate to EN to detect source.
	form := url.Values{}
	form.Set("text", text)
	form.Set("target_lang", "EN")

	apiURL := deeplBaseURL + "/v2/translate"
	req, err := http.NewRequest("POST", apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create request error: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "DeepL-Auth-Key "+apiKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("deepl API error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("deepl API returned %d", resp.StatusCode)
	}

	var result struct {
		Translations []struct {
			DetectedSourceLang string `json:"detected_source_language"`
		} `json:"translations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode error: %w", err)
	}
	if len(result.Translations) == 0 {
		return "", fmt.Errorf("no result from DeepL")
	}

	lang := strings.ToLower(result.Translations[0].DetectedSourceLang)
	return fmt.Sprintf("Detected language: %s (via DeepL)", lang), nil
}

func detectLanguageHeuristic(text string) string {
	var (
		cjk      int
		hiragana int
		katakana int
		hangul   int
		latin    int
		cyrillic int
		arabic   int
		thai     int
		total    int
	)

	for _, r := range text {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsDigit(r) {
			continue
		}
		total++
		switch {
		case r >= '\u3040' && r <= '\u309F':
			hiragana++
			cjk++
		case r >= '\u30A0' && r <= '\u30FF':
			katakana++
			cjk++
		case r >= '\u4E00' && r <= '\u9FFF':
			cjk++
		case r >= '\uAC00' && r <= '\uD7AF':
			hangul++
		case r >= '\u0400' && r <= '\u04FF':
			cyrillic++
		case r >= '\u0600' && r <= '\u06FF':
			arabic++
		case r >= '\u0E00' && r <= '\u0E7F':
			thai++
		case unicode.Is(unicode.Latin, r):
			latin++
		}
	}

	if total == 0 {
		return "Detected language: unknown (no text content)"
	}

	// Japanese: has hiragana/katakana.
	if hiragana+katakana > 0 {
		return "Detected language: ja (Japanese) — heuristic"
	}
	// Korean: has hangul.
	if hangul > total/3 {
		return "Detected language: ko (Korean) — heuristic"
	}
	// Chinese: CJK without kana.
	if cjk > total/3 {
		return "Detected language: zh (Chinese) — heuristic"
	}
	if cyrillic > total/3 {
		return "Detected language: ru (Russian/Cyrillic) — heuristic"
	}
	if arabic > total/3 {
		return "Detected language: ar (Arabic) — heuristic"
	}
	if thai > total/3 {
		return "Detected language: th (Thai) — heuristic"
	}
	if latin > total/2 {
		return "Detected language: en (English/Latin script) — heuristic"
	}

	return "Detected language: unknown — heuristic"
}
