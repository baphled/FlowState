// Package api — voice transcription endpoint. POST /api/v1/voice/
// transcribe accepts a multipart "audio" WAV upload, runs it through
// the local STT tool (whisper-cli or the configured override), and
// returns the transcript JSON. Local-only by design: no audio or
// transcript ever leaves the machine.
package api

import (
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/baphled/flowstate/internal/voice"
)

// maxVoiceUploadBytes bounds the accepted WAV size (10 MiB) so a
// runaway client cannot exhaust temp disk.
const maxVoiceUploadBytes = 10 << 20

// handleVoiceTranscribe accepts a multipart WAV upload under the
// "audio" field and returns {"transcript": "..."} using the local
// STT tool.
//
// Expected:
//   - w and r are wired by the mux.
//
// Returns:
//   - Writes 200 {"transcript":...} on success.
//   - 400 invalid_request when the audio field is missing, too
//     large, or undecodable.
//   - 503 voice_unavailable when no STT binary resolves.
//   - 500 internal on transcription failure.
//
// Side effects:
//   - Spills the upload to a temp file and spawns the STT binary;
//     the temp file is removed on every exit path.
func (s *Server) handleVoiceTranscribe(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxVoiceUploadBytes)
	if err := r.ParseMultipartForm(maxVoiceUploadBytes); err != nil {
		writeVoiceError(w, http.StatusBadRequest, "invalid_request", "multipart body required with an 'audio' WAV field")
		return
	}
	file, _, err := r.FormFile("audio")
	if err != nil {
		writeVoiceError(w, http.StatusBadRequest, "invalid_request", "missing 'audio' field")
		return
	}
	defer file.Close()

	tmp, err := os.CreateTemp("", "flowstate-voice-upload-*.wav")
	if err != nil {
		writeVoiceError(w, http.StatusInternalServerError, "internal", "could not stage upload")
		return
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		writeVoiceError(w, http.StatusBadRequest, "invalid_request", "audio too large or unreadable")
		return
	}
	if err := tmp.Close(); err != nil {
		writeVoiceError(w, http.StatusInternalServerError, "internal", "could not stage upload")
		return
	}

	sttTool, err := voice.NewSTTTool("")
	if err != nil {
		writeVoiceError(w, http.StatusServiceUnavailable, "voice_unavailable", err.Error())
		return
	}
	transcript, err := sttTool.Transcribe(r.Context(), path)
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

// voiceTranscribeResponse is the 200 wire shape for POST
// /api/v1/voice/transcribe.
type voiceTranscribeResponse struct {
	// Transcript is the local STT output for the uploaded WAV.
	Transcript string `json:"transcript"`
}

// writeVoiceError writes the endpoint's JSON error shape:
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
