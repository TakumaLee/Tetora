package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// --- STT (Speech-to-Text) Types ---

// STTProvider defines the interface for speech-to-text providers.
type STTProvider interface {
	Transcribe(ctx context.Context, audio io.Reader, opts STTOptions) (*STTResult, error)
	Name() string
}

// STTOptions configures transcription behavior.
type STTOptions struct {
	Language string // ISO 639-1 code, "" = auto-detect
	Format   string // "ogg", "wav", "mp3", "webm", etc.
}

// STTResult holds transcription output.
type STTResult struct {
	Text       string  `json:"text"`
	Language   string  `json:"language"`
	Duration   float64 `json:"durationSec"`
	Confidence float64 `json:"confidence,omitempty"`
}

// --- OpenAI STT Provider ---

// OpenAISTTProvider implements STT using OpenAI Whisper API.
type OpenAISTTProvider struct {
	endpoint string // default: https://api.openai.com/v1/audio/transcriptions
	apiKey   string
	model    string // default: "gpt-4o-mini-transcribe"
}

func (p *OpenAISTTProvider) Name() string {
	return "openai-stt"
}

func (p *OpenAISTTProvider) Transcribe(ctx context.Context, audio io.Reader, opts STTOptions) (*STTResult, error) {
	endpoint := p.endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/audio/transcriptions"
	}
	model := p.model
	if model == "" {
		model = "gpt-4o-mini-transcribe"
	}

	// Build multipart form data.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// Add file field.
	format := opts.Format
	if format == "" {
		format = "mp3"
	}
	filename := "audio." + format
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(fw, audio); err != nil {
		return nil, fmt.Errorf("copy audio: %w", err)
	}

	// Add model field.
	if err := mw.WriteField("model", model); err != nil {
		return nil, fmt.Errorf("write model field: %w", err)
	}

	// Add language field if specified.
	if opts.Language != "" {
		if err := mw.WriteField("language", opts.Language); err != nil {
			return nil, fmt.Errorf("write language field: %w", err)
		}
	}

	// Add response_format field (default: json).
	if err := mw.WriteField("response_format", "json"); err != nil {
		return nil, fmt.Errorf("write response_format field: %w", err)
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	// Create request.
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	// Execute request.
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai stt api error: status=%d body=%s", resp.StatusCode, string(body))
	}

	// Parse response: {"text": "transcribed text"}
	var result struct {
		Text     string  `json:"text"`
		Language string  `json:"language,omitempty"`
		Duration float64 `json:"duration,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &STTResult{
		Text:     result.Text,
		Language: result.Language,
		Duration: result.Duration,
	}, nil
}

// --- TTS (Text-to-Speech) Types ---

// TTSProvider defines the interface for text-to-speech providers.
type TTSProvider interface {
	Synthesize(ctx context.Context, text string, opts TTSOptions) (io.ReadCloser, error)
	Name() string
}

// TTSOptions configures synthesis behavior.
type TTSOptions struct {
	Voice  string  // provider-specific voice ID
	Speed  float64 // default 1.0
	Format string  // "mp3", "opus", "wav"
}

// --- OpenAI TTS Provider ---

// OpenAITTSProvider implements TTS using OpenAI TTS API.
type OpenAITTSProvider struct {
	endpoint string // default: https://api.openai.com/v1/audio/speech
	apiKey   string
	model    string // default: "tts-1"
	voice    string // default: "alloy"
}

func (p *OpenAITTSProvider) Name() string {
	return "openai-tts"
}

func (p *OpenAITTSProvider) Synthesize(ctx context.Context, text string, opts TTSOptions) (io.ReadCloser, error) {
	endpoint := p.endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/audio/speech"
	}
	model := p.model
	if model == "" {
		model = "tts-1"
	}
	voice := opts.Voice
	if voice == "" {
		voice = p.voice
	}
	if voice == "" {
		voice = "alloy"
	}
	format := opts.Format
	if format == "" {
		format = "mp3"
	}
	speed := opts.Speed
	if speed <= 0 {
		speed = 1.0
	}

	// Build request body.
	reqBody := map[string]any{
		"model":           model,
		"input":           text,
		"voice":           voice,
		"response_format": format,
		"speed":           speed,
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Create request.
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	// Execute request.
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("openai tts api error: status=%d body=%s", resp.StatusCode, string(body))
	}

	// Return audio stream (caller must close).
	return resp.Body, nil
}

// --- ElevenLabs TTS Provider ---

// ElevenLabsTTSProvider implements TTS using ElevenLabs API.
type ElevenLabsTTSProvider struct {
	apiKey  string
	voiceID string // default: "Rachel"
	model   string // default: "eleven_flash_v2_5"
}

func (p *ElevenLabsTTSProvider) Name() string {
	return "elevenlabs-tts"
}

func (p *ElevenLabsTTSProvider) Synthesize(ctx context.Context, text string, opts TTSOptions) (io.ReadCloser, error) {
	voiceID := opts.Voice
	if voiceID == "" {
		voiceID = p.voiceID
	}
	if voiceID == "" {
		voiceID = "Rachel"
	}
	model := p.model
	if model == "" {
		model = "eleven_flash_v2_5"
	}

	endpoint := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s", voiceID)

	// Build request body.
	reqBody := map[string]any{
		"text":     text,
		"model_id": model,
	}
	// Add voice settings if speed is specified.
	if opts.Speed > 0 && opts.Speed != 1.0 {
		reqBody["voice_settings"] = map[string]any{
			"stability":        0.5,
			"similarity_boost": 0.75,
			"speed":            opts.Speed,
		}
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Create request.
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("xi-api-key", p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")

	// Execute request.
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("elevenlabs api error: status=%d body=%s", resp.StatusCode, string(body))
	}

	// Return audio stream (caller must close).
	return resp.Body, nil
}

// --- Voice Engine (Coordinator) ---

// VoiceEngine coordinates STT and TTS providers.
type VoiceEngine struct {
	stt STTProvider
	tts TTSProvider
	cfg *Config
}

// newVoiceEngine initializes the voice engine from config.
func newVoiceEngine(cfg *Config) *VoiceEngine {
	ve := &VoiceEngine{cfg: cfg}

	// Initialize STT provider.
	if cfg.Voice.STT.Enabled {
		provider := cfg.Voice.STT.Provider
		if provider == "" {
			provider = "openai"
		}
		switch provider {
		case "openai":
			apiKey := cfg.Voice.STT.APIKey
			if apiKey == "" {
				logWarn("voice stt enabled but no apiKey configured")
			}
			ve.stt = &OpenAISTTProvider{
				endpoint: cfg.Voice.STT.Endpoint,
				apiKey:   apiKey,
				model:    cfg.Voice.STT.Model,
			}
			logInfo("voice stt initialized", "provider", provider, "model", cfg.Voice.STT.Model)
		default:
			logWarn("unknown stt provider", "provider", provider)
		}
	}

	// Initialize TTS provider.
	if cfg.Voice.TTS.Enabled {
		provider := cfg.Voice.TTS.Provider
		if provider == "" {
			provider = "openai"
		}
		switch provider {
		case "openai":
			apiKey := cfg.Voice.TTS.APIKey
			if apiKey == "" {
				logWarn("voice tts enabled but no apiKey configured")
			}
			ve.tts = &OpenAITTSProvider{
				endpoint: cfg.Voice.TTS.Endpoint,
				apiKey:   apiKey,
				model:    cfg.Voice.TTS.Model,
				voice:    cfg.Voice.TTS.Voice,
			}
			logInfo("voice tts initialized", "provider", provider, "model", cfg.Voice.TTS.Model, "voice", cfg.Voice.TTS.Voice)
		case "elevenlabs":
			apiKey := cfg.Voice.TTS.APIKey
			if apiKey == "" {
				logWarn("voice tts enabled but no apiKey configured")
			}
			ve.tts = &ElevenLabsTTSProvider{
				apiKey:  apiKey,
				voiceID: cfg.Voice.TTS.Voice,
				model:   cfg.Voice.TTS.Model,
			}
			logInfo("voice tts initialized", "provider", provider, "model", cfg.Voice.TTS.Model, "voice", cfg.Voice.TTS.Voice)
		default:
			logWarn("unknown tts provider", "provider", provider)
		}
	}

	return ve
}

// Transcribe delegates to the configured STT provider.
func (v *VoiceEngine) Transcribe(ctx context.Context, audio io.Reader, opts STTOptions) (*STTResult, error) {
	if v.stt == nil {
		return nil, fmt.Errorf("stt not enabled")
	}
	return v.stt.Transcribe(ctx, audio, opts)
}

// Synthesize delegates to the configured TTS provider.
func (v *VoiceEngine) Synthesize(ctx context.Context, text string, opts TTSOptions) (io.ReadCloser, error) {
	if v.tts == nil {
		return nil, fmt.Errorf("tts not enabled")
	}
	return v.tts.Synthesize(ctx, text, opts)
}
