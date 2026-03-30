// Package voice implements STT/TTS provider types and the VoiceEngine coordinator.
package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	tlog "tetora/internal/log"
)

// --- Config Types ---

// VoiceConfig aggregates all voice-related configuration.
type VoiceConfig struct {
	STT      STTConfig           `json:"stt,omitempty"`
	TTS      TTSConfig           `json:"tts,omitempty"`
	Wake     VoiceWakeConfig     `json:"wake,omitempty"`
	Realtime VoiceRealtimeConfig `json:"realtime,omitempty"`
}

// STTConfig configures speech-to-text.
type STTConfig struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Provider string `json:"provider,omitempty"` // "openai"
	Model    string `json:"model,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	APIKey   string `json:"apiKey,omitempty"` // supports $ENV_VAR
	Language string `json:"language,omitempty"`
}

// TTSConfig configures text-to-speech.
type TTSConfig struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Provider string `json:"provider,omitempty"` // "openai", "elevenlabs"
	Model    string `json:"model,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	APIKey   string `json:"apiKey,omitempty"` // supports $ENV_VAR
	Voice    string `json:"voice,omitempty"`
	Format   string `json:"format,omitempty"` // "mp3", "opus"
}

// VoiceWakeConfig configures wake word detection.
type VoiceWakeConfig struct {
	Enabled   bool     `json:"enabled,omitempty"`
	WakeWords []string `json:"wakeWords,omitempty"` // ["テトラ", "tetora", "hey tetora"]
	Threshold float64  `json:"threshold,omitempty"` // VAD sensitivity (0.0-1.0), default 0.6
}

// VoiceRealtimeConfig configures the OpenAI Realtime API relay.
type VoiceRealtimeConfig struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Provider string `json:"provider,omitempty"` // "openai"
	Model    string `json:"model,omitempty"`    // "gpt-4o-realtime-preview"
	APIKey   string `json:"apiKey,omitempty"`   // $ENV_VAR supported
	Voice    string `json:"voice,omitempty"`    // "alloy", "shimmer", etc.
}

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
	Endpoint string // default: https://api.openai.com/v1/audio/transcriptions
	APIKey   string
	Model    string // default: "gpt-4o-mini-transcribe"
}

func (p *OpenAISTTProvider) Name() string {
	return "openai-stt"
}

func (p *OpenAISTTProvider) Transcribe(ctx context.Context, audio io.Reader, opts STTOptions) (*STTResult, error) {
	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/audio/transcriptions"
	}
	model := p.Model
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
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
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

// --- Groq STT Provider ---

// GroqSTTProvider implements STT using Groq's Whisper-compatible API.
// Groq offers OpenAI-compatible transcription endpoints at significantly lower cost
// (~9x cheaper than OpenAI Whisper for the Large v3 Turbo model).
type GroqSTTProvider struct {
	apiKey string
	model  string // default: "whisper-large-v3"
}

func (p *GroqSTTProvider) Name() string {
	return "groq-stt"
}

func (p *GroqSTTProvider) Transcribe(ctx context.Context, audio io.Reader, opts STTOptions) (*STTResult, error) {
	model := p.model
	if model == "" {
		model = "whisper-large-v3"
	}

	// Build multipart form data (Groq uses same format as OpenAI Whisper).
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	format := opts.Format
	if format == "" {
		format = "ogg"
	}
	fw, err := mw.CreateFormFile("file", "audio."+format)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(fw, audio); err != nil {
		return nil, fmt.Errorf("copy audio: %w", err)
	}
	if err := mw.WriteField("model", model); err != nil {
		return nil, fmt.Errorf("write model field: %w", err)
	}
	if opts.Language != "" {
		if err := mw.WriteField("language", opts.Language); err != nil {
			return nil, fmt.Errorf("write language field: %w", err)
		}
	}
	if err := mw.WriteField("response_format", "json"); err != nil {
		return nil, fmt.Errorf("write response_format field: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.groq.com/openai/v1/audio/transcriptions", &buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("groq stt api error: status=%d body=%s", resp.StatusCode, string(body))
	}

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
	Endpoint string // default: https://api.openai.com/v1/audio/speech
	APIKey   string
	Model    string // default: "tts-1"
	Voice    string // default: "alloy"
}

func (p *OpenAITTSProvider) Name() string {
	return "openai-tts"
}

func (p *OpenAITTSProvider) Synthesize(ctx context.Context, text string, opts TTSOptions) (io.ReadCloser, error) {
	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/audio/speech"
	}
	model := p.Model
	if model == "" {
		model = "tts-1"
	}
	voice := opts.Voice
	if voice == "" {
		voice = p.Voice
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
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
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
	APIKey  string
	VoiceID string // default: "Rachel"
	Model   string // default: "eleven_flash_v2_5"
}

func (p *ElevenLabsTTSProvider) Name() string {
	return "elevenlabs-tts"
}

func (p *ElevenLabsTTSProvider) Synthesize(ctx context.Context, text string, opts TTSOptions) (io.ReadCloser, error) {
	voiceID := opts.Voice
	if voiceID == "" {
		voiceID = p.VoiceID
	}
	if voiceID == "" {
		voiceID = "Rachel"
	}
	model := p.Model
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
	req.Header.Set("xi-api-key", p.APIKey)
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
	STT STTProvider
	TTS TTSProvider
	Cfg VoiceConfig
}

// NewVoiceEngine initializes the voice engine from VoiceConfig.
func NewVoiceEngine(cfg VoiceConfig) *VoiceEngine {
	ve := &VoiceEngine{Cfg: cfg}

	// Initialize STT provider.
	if cfg.STT.Enabled {
		provider := cfg.STT.Provider
		if provider == "" {
			provider = "openai"
		}
		switch provider {
		case "openai":
			apiKey := cfg.STT.APIKey
			if apiKey == "" {
				tlog.Warn("voice stt enabled but no apiKey configured")
			}
			ve.STT = &OpenAISTTProvider{
				Endpoint: cfg.STT.Endpoint,
				APIKey:   apiKey,
				Model:    cfg.STT.Model,
			}
			tlog.Info("voice stt initialized", "provider", provider, "model", cfg.STT.Model)
		case "groq":
			apiKey := cfg.STT.APIKey
			if apiKey == "" {
				tlog.Warn("voice stt enabled but no apiKey configured for groq")
			}
			ve.STT = &GroqSTTProvider{
				apiKey: apiKey,
				model:  cfg.STT.Model,
			}
			tlog.Info("voice stt initialized", "provider", provider, "model", cfg.STT.Model)
		default:
			tlog.Warn("unknown stt provider", "provider", provider)
		}
	}

	// Initialize TTS provider.
	if cfg.TTS.Enabled {
		provider := cfg.TTS.Provider
		if provider == "" {
			provider = "openai"
		}
		switch provider {
		case "openai":
			apiKey := cfg.TTS.APIKey
			if apiKey == "" {
				tlog.Warn("voice tts enabled but no apiKey configured")
			}
			ve.TTS = &OpenAITTSProvider{
				Endpoint: cfg.TTS.Endpoint,
				APIKey:   apiKey,
				Model:    cfg.TTS.Model,
				Voice:    cfg.TTS.Voice,
			}
			tlog.Info("voice tts initialized", "provider", provider, "model", cfg.TTS.Model, "voice", cfg.TTS.Voice)
		case "elevenlabs":
			apiKey := cfg.TTS.APIKey
			if apiKey == "" {
				tlog.Warn("voice tts enabled but no apiKey configured")
			}
			ve.TTS = &ElevenLabsTTSProvider{
				APIKey:  apiKey,
				VoiceID: cfg.TTS.Voice,
				Model:   cfg.TTS.Model,
			}
			tlog.Info("voice tts initialized", "provider", provider, "model", cfg.TTS.Model, "voice", cfg.TTS.Voice)
		default:
			tlog.Warn("unknown tts provider", "provider", provider)
		}
	}

	return ve
}

// Transcribe delegates to the configured STT provider.
func (v *VoiceEngine) Transcribe(ctx context.Context, audio io.Reader, opts STTOptions) (*STTResult, error) {
	if v.STT == nil {
		return nil, fmt.Errorf("stt not enabled")
	}
	return v.STT.Transcribe(ctx, audio, opts)
}

// Synthesize delegates to the configured TTS provider.
func (v *VoiceEngine) Synthesize(ctx context.Context, text string, opts TTSOptions) (io.ReadCloser, error) {
	if v.TTS == nil {
		return nil, fmt.Errorf("tts not enabled")
	}
	return v.TTS.Synthesize(ctx, text, opts)
}
