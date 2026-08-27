// Package api — frontend-facing voice endpoints: WAV upload
// transcription with dispatch, runtime voice settings, and TTS
// synthesis. Local-only by design: no audio or transcript ever
// leaves the machine.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/baphled/flowstate/internal/voice"
)

// maxVoiceUploadBytes bounds the accepted WAV size (10 MiB) so a
// runaway client cannot exhaust temp disk.
const maxVoiceUploadBytes = 10 << 20

// VoiceTurnDispatcher is the dispatch surface the transcribe
// endpoint needs after STT: one ephemeral, mention-scanned turn.
type VoiceTurnDispatcher interface {
	// DispatchAudio transcribes WAV bytes and dispatches the
	// transcript as an ephemeral turn.
	DispatchAudio(audio []byte) (string, error)
}

// VoiceSettingsStore exposes runtime-mutable voice settings for the
// GET/PATCH settings endpoints. The production implementation is
// in-memory runtime state seeded from the loaded VoiceConfig; values
// do not persist across restarts (documented behaviour).
type VoiceSettingsStore interface {
	// Get returns the current runtime settings snapshot.
	Get() voice.Settings
	// Patch applies a partial update and returns the result.
	Patch(patch voice.SettingsPatch) (voice.Settings, error)
}

// VoiceSynthesiser converts reply text to WAV bytes for browser
// playback. Production wires *voice.TTSTool; there is no fallback
// when piper is unavailable.
type VoiceSynthesiser interface {
	// Synthesize renders text as concatenated WAV bytes.
	Synthesize(text string) ([]byte, error)
}

// handleVoiceTranscribe accepts a multipart WAV upload under the
// "audio" field (or a raw WAV body) and returns
// {"transcript": "..."} after running STT and dispatching the
// transcript as a mention-scanned ephemeral turn.
//
// Expected:
//   - w and r are wired by the mux.
//
// Returns:
//   - Writes 200 {"transcript":...} on success.
//   - 400 invalid_request when the audio is missing, not WAV, too
//     large, or undecodable.
//   - 501 voice_not_wired when no turn pipeline is wired.
//   - 503 voice_unavailable when no STT binary resolves.
//   - 500 internal on transcription failure.
//
// Side effects:
//   - Spawns the STT binary and a dispatcher turn.
func (s *Server) handleVoiceTranscribe(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxVoiceUploadBytes)
	if s.voiceTurnPipeline == nil {
		writeVoiceError(w, http.StatusNotImplemented, "voice_not_wired", "voice turn pipeline is not wired")
		return
	}
	audio, errErr := readVoiceUpload(r)
	if errErr != nil {
		writeVoiceError(w, errErr.status, errErr.code, errErr.msg)
		return
	}
	transcript, err := s.voiceTurnPipeline.DispatchAudio(audio)
	if err != nil {
		if errors.Is(err, voice.ErrSTTUnavailable) {
			writeVoiceError(w, http.StatusServiceUnavailable, "voice_unavailable", err.Error())
			return
		}
		writeVoiceError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, voiceTranscribeResponse{Transcript: transcript})
}

// voiceHTTPError carries the endpoint error mapping.
type voiceHTTPError struct {
	status int
	code   string
	msg    string
}

// readVoiceUpload extracts WAV bytes from a multipart "audio" field
// or the raw body, rejecting non-WAV uploads.
//
// Expected:
//   - r is the transcribe request with a bounded body.
//
// Returns:
//   - The WAV bytes.
//   - A voiceHTTPError for the 400 mapping on bad input.
//
// Side effects:
//   - Parses the multipart form when the content type is multipart.
func readVoiceUpload(r *http.Request) ([]byte, *voiceHTTPError) {
	var audio []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(maxVoiceUploadBytes); err != nil {
			return nil, &voiceHTTPError{http.StatusBadRequest, "invalid_request", "multipart body required with an 'audio' WAV field"}
		}
		file, _, err := r.FormFile("audio")
		if err != nil {
			return nil, &voiceHTTPError{http.StatusBadRequest, "invalid_request", "missing 'audio' field"}
		}
		defer file.Close()
		audio, err = io.ReadAll(io.LimitReader(file, maxVoiceUploadBytes))
		if err != nil {
			return nil, &voiceHTTPError{http.StatusBadRequest, "invalid_request", "audio too large or unreadable"}
		}
	} else {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, &voiceHTTPError{http.StatusBadRequest, "invalid_request", "audio too large or unreadable"}
		}
		audio = body
	}
	if len(audio) == 0 {
		return nil, &voiceHTTPError{http.StatusBadRequest, "invalid_request", "empty upload; send WAV audio"}
	}
	if !bytes.HasPrefix(audio, []byte("RIFF")) || !bytes.Contains(audio[:12], []byte("WAVE")) {
		return nil, &voiceHTTPError{http.StatusBadRequest, "invalid_request", "only WAV audio is supported; convert webm/other formats to WAV before uploading"}
	}
	return audio, nil
}

// voiceTranscribeResponse is the 200 wire shape for POST
// /api/v1/voice/transcribe.
type voiceTranscribeResponse struct {
	// Transcript is the local STT output for the uploaded WAV.
	Transcript string `json:"transcript"`
}

// handleGetVoiceSettings serves the runtime voice settings.
//
// Expected:
//   - w and r are wired by the mux.
//
// Returns:
//   - 200 with the settings JSON.
//   - 501 voice_not_wired when no settings store is wired.
//
// Side effects:
//   - None.
func (s *Server) handleGetVoiceSettings(w http.ResponseWriter, _ *http.Request) {
	if s.voiceSettings == nil {
		writeVoiceError(w, http.StatusNotImplemented, "voice_not_wired", "voice settings store is not wired")
		return
	}
	writeJSON(w, s.voiceSettings.Get())
}

// handlePatchVoiceSettings applies a partial runtime settings update.
//
// Expected:
//   - w and r are wired by the mux; the body is a settings JSON.
//
// Returns:
//   - 200 with the updated settings JSON.
//   - 400 invalid_request on a malformed or out-of-range patch.
//   - 501 voice_not_wired when no settings store is wired.
//
// Side effects:
//   - Mutates the runtime settings store in memory.
func (s *Server) handlePatchVoiceSettings(w http.ResponseWriter, r *http.Request) {
	if s.voiceSettings == nil {
		writeVoiceError(w, http.StatusNotImplemented, "voice_not_wired", "voice settings store is not wired")
		return
	}
	var patch voice.SettingsPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&patch); err != nil {
		writeVoiceError(w, http.StatusBadRequest, "invalid_request", "settings body must be JSON")
		return
	}
	updated, err := s.voiceSettings.Patch(patch)
	if err != nil {
		writeVoiceError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, updated)
}

// handleVoiceTTS synthesises reply text to WAV audio for browser
// playback.
//
// Expected:
//   - w and r are wired by the mux; the body is {"text": "..."}.
//
// Returns:
//   - 200 audio/wav with the synthesised bytes.
//   - 400 invalid_request when text is missing or empty.
//   - 501 voice_not_wired when no synthesiser is wired.
//   - 503 voice_unavailable when piper cannot synthesise.
//
// Side effects:
//   - Spawns piper children; writes WAV bytes to the response.
func (s *Server) handleVoiceTTS(w http.ResponseWriter, r *http.Request) {
	if s.voiceSynthesiser == nil {
		writeVoiceError(w, http.StatusNotImplemented, "voice_not_wired", "voice synthesiser is not wired")
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeVoiceError(w, http.StatusBadRequest, "invalid_request", "body must be JSON with a 'text' field")
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeVoiceError(w, http.StatusBadRequest, "invalid_request", "'text' is required")
		return
	}
	wav, err := s.voiceSynthesiser.Synthesize(body.Text)
	if err != nil {
		if errors.Is(err, voice.ErrPiperUnavailable) || errors.Is(err, voice.ErrTTSUnavailable) {
			writeVoiceError(w, http.StatusServiceUnavailable, "voice_unavailable", err.Error())
			return
		}
		writeVoiceError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(wav)
}

// writeVoiceError writes the voice endpoints' JSON error shape:
// {"error":{"code":...,"message":...}}.
//
// Expected:
//   - w is the response writer; statusCode the HTTP status.
//   - code is the machine-readable error category.
//   - msg is the human-readable detail.
//
// Returns:
//   - Nothing; writes the JSON body.
//
// Side effects:
//   - Sets the Content-Type header and writes the response.
func writeVoiceError(w http.ResponseWriter, statusCode int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	body := `{"error":{"code":` + jsonString(code) + `,"message":` + jsonString(msg) + `}}`
	_, _ = w.Write([]byte(body))
}

// RuntimeVoiceSettings is the in-memory VoiceSettingsStore seeded
// from the loaded config. Changes live for the process lifetime
// only; persistence to YAML is intentionally out of scope.
type RuntimeVoiceSettings struct {
	mu       sync.Mutex
	settings voice.Settings
}

// NewRuntimeVoiceSettings seeds the runtime store from a snapshot.
//
// Expected:
//   - initial is the loaded VoiceConfig-derived snapshot.
//
// Returns:
//   - A *RuntimeVoiceSettings.
//
// Side effects:
//   - None.
func NewRuntimeVoiceSettings(initial voice.Settings) *RuntimeVoiceSettings {
	return &RuntimeVoiceSettings{settings: initial}
}

// Get returns the current settings snapshot under the store lock.
//
// Expected:
//   - None.
//
// Returns:
//   - The current voice.Settings snapshot.
//
// Side effects:
//   - None beyond taking the store lock.
func (s *RuntimeVoiceSettings) Get() voice.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// Patch applies a partial update, validating tuning ranges.
//
// Expected:
//   - patch carries only the fields to change.
//
// Returns:
//   - The updated snapshot.
//   - An error when a tuning value is out of range.
//
// Side effects:
//   - Mutates the stored settings under the lock.
func (s *RuntimeVoiceSettings) Patch(patch voice.SettingsPatch) (voice.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.settings
	if patch.TTSEnabled != nil {
		next.TTSEnabled = *patch.TTSEnabled
	}
	if patch.TTSModel != nil {
		next.TTSModel = *patch.TTSModel
	}
	if patch.LengthScale != nil {
		if *patch.LengthScale <= 0 || *patch.LengthScale > 3 {
			return s.settings, errors.New("length_scale must be between 0 (exclusive) and 3")
		}
		next.LengthScale = *patch.LengthScale
	}
	if patch.NoiseScale != nil {
		if *patch.NoiseScale < 0 || *patch.NoiseScale > 1 {
			return s.settings, errors.New("noise_scale must be between 0 and 1")
		}
		next.NoiseScale = *patch.NoiseScale
	}
	if patch.SentenceSilence != nil {
		if *patch.SentenceSilence < 0 || *patch.SentenceSilence > 5 {
			return s.settings, errors.New("sentence_silence must be between 0 and 5 seconds")
		}
		next.SentenceSilence = *patch.SentenceSilence
	}
	s.settings = next
	return next, nil
}

// PipelineDispatcherAdapter adapts a voice.Dispatcher plus pipeline
// into the endpoint's VoiceTurnDispatcher, so callers that already
// hold a *voice.Pipeline and dispatcher can wire the transcribe
// endpoint without a bespoke adapter.
type PipelineDispatcherAdapter struct {
	// Pipeline is the shared STT-and-dispatch pipeline.
	Pipeline *voice.Pipeline
	// Dispatcher receives the mention-scanned ephemeral turn.
	Dispatcher voice.Dispatcher
}

// DispatchAudio runs the pipeline's shared STT-and-dispatch path.
//
// Expected:
//   - audio is non-empty WAV bytes.
//
// Returns:
//   - The dispatched transcript.
//   - The pipeline's error verbatim on failure.
//
// Side effects:
//   - Spawns the STT binary and one ephemeral dispatch turn.
func (a *PipelineDispatcherAdapter) DispatchAudio(audio []byte) (string, error) {
	return a.Pipeline.DispatchAudio(context.Background(), a.Dispatcher, audio)
}
