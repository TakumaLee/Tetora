package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (s *Server) registerVoiceRoutes(mux *http.ServeMux) {
	cfg := s.cfg

	// --- Voice Engine ---
	mux.HandleFunc("/api/voice/transcribe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if s.voiceEngine == nil || s.voiceEngine.stt == nil {
			http.Error(w, `{"error":"voice stt not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		// Parse multipart form.
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"parse form: %v"}`, err), http.StatusBadRequest)
			return
		}

		// Get audio file.
		file, header, err := r.FormFile("audio")
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"missing audio field: %v"}`, err), http.StatusBadRequest)
			return
		}
		defer file.Close()

		// Get options from form.
		language := r.FormValue("language")
		format := r.FormValue("format")
		if format == "" {
			// Try to infer from filename.
			if strings.HasSuffix(header.Filename, ".ogg") {
				format = "ogg"
			} else if strings.HasSuffix(header.Filename, ".wav") {
				format = "wav"
			} else if strings.HasSuffix(header.Filename, ".webm") {
				format = "webm"
			} else {
				format = "mp3"
			}
		}

		opts := STTOptions{
			Language: language,
			Format:   format,
		}

		// Transcribe.
		result, err := s.voiceEngine.Transcribe(r.Context(), file, opts)
		if err != nil {
			logErrorCtx(r.Context(), "voice transcribe failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	mux.HandleFunc("/api/voice/synthesize", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if s.voiceEngine == nil || s.voiceEngine.tts == nil {
			http.Error(w, `{"error":"voice tts not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		// Parse request body.
		var req struct {
			Text   string  `json:"text"`
			Voice  string  `json:"voice,omitempty"`
			Speed  float64 `json:"speed,omitempty"`
			Format string  `json:"format,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Text == "" {
			http.Error(w, `{"error":"text field required"}`, http.StatusBadRequest)
			return
		}

		opts := TTSOptions{
			Voice:  req.Voice,
			Speed:  req.Speed,
			Format: req.Format,
		}

		// Synthesize.
		stream, err := s.voiceEngine.Synthesize(r.Context(), req.Text, opts)
		if err != nil {
			logErrorCtx(r.Context(), "voice synthesize failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		defer stream.Close()

		// Determine content type.
		format := req.Format
		if format == "" {
			format = cfg.Voice.TTS.Format
		}
		if format == "" {
			format = "mp3"
		}
		contentType := "audio/mpeg"
		if format == "opus" {
			contentType = "audio/opus"
		} else if format == "wav" {
			contentType = "audio/wav"
		}

		// Stream audio to response.
		w.Header().Set("Content-Type", contentType)
		io.Copy(w, stream)
	})

	// --- P16.2: Voice Realtime WebSocket Endpoints ---
	mux.HandleFunc("/ws/voice/wake", func(w http.ResponseWriter, r *http.Request) {
		if !cfg.Voice.Wake.Enabled {
			http.Error(w, `{"error":"voice wake not enabled"}`, http.StatusServiceUnavailable)
			return
		}
		if s.voiceRealtimeEngine == nil {
			http.Error(w, `{"error":"voice realtime engine not initialized"}`, http.StatusServiceUnavailable)
			return
		}
		s.voiceRealtimeEngine.handleWakeWebSocket(w, r)
	})

	mux.HandleFunc("/ws/voice/realtime", func(w http.ResponseWriter, r *http.Request) {
		if !cfg.Voice.Realtime.Enabled {
			http.Error(w, `{"error":"voice realtime not enabled"}`, http.StatusServiceUnavailable)
			return
		}
		if s.voiceRealtimeEngine == nil {
			http.Error(w, `{"error":"voice realtime engine not initialized"}`, http.StatusServiceUnavailable)
			return
		}
		s.voiceRealtimeEngine.handleRealtimeWebSocket(w, r)
	})
}
