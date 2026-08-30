// Package api — conversation-mode voice endpoints: start/stop the
// spoken conversation session and submit voice turns whose reply is
// synthesised server-side.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/baphled/flowstate/internal/voice"
)

// maxVoiceConversationTurnBytes bounds the accepted WAV size for a
// conversation turn (10 MiB), matching the transcribe endpoint.
const maxVoiceConversationTurnBytes = 10 << 20

// VoiceConversationService is the conversation-mode surface: one
// active session per server plus per-turn STT → chat → TTS.
type VoiceConversationService interface {
	// Start activates conversation mode for the session.
	Start(sessionID string)
	// Stop deactivates conversation mode.
	Stop()
	// Active reports the active session ID ("" when inactive).
	Active() string
	// Turn runs one voice conversation turn.
	Turn(audio []byte, speakReply bool) (voice.ConversationResult, error)
}

// voiceConversationStatus is the wire shape for start/stop
// responses and the active-session report.
type voiceConversationStatus struct {
	// Active reports whether conversation mode is on.
	Active bool `json:"active"`
	// Session is the bound chat session ID ("" when inactive).
	Session string `json:"session"`
}

// voiceConversationTurnResponse is the 200 wire shape for POST
// /api/v1/voice/conversation/turn.
type voiceConversationTurnResponse struct {
	// Transcript is the local STT output for the turn.
	Transcript string `json:"transcript"`
	// Reply is the agent reply text (empty when none).
	Reply string `json:"reply"`
	// Audio is base64 WAV of the spoken reply when speak_reply was
	// set and synthesis succeeded; empty otherwise.
	Audio []byte `json:"audio,omitempty"`
}

// handleVoiceConversationStart activates conversation mode for the
// requested session.
//
// Expected:
//   - w and r are wired by the mux; body is
//     {"session_id": "..."}.
//
// Returns:
//   - 200 with the conversation status.
//   - 400 invalid_request when session_id is missing.
//   - 501 voice_not_wired when no conversation service is wired.
//
// Side effects:
//   - Binds the active conversation session.
func (s *Server) handleVoiceConversationStart(w http.ResponseWriter, r *http.Request) {
	if s.voiceConversation == nil {
		writeVoiceError(w, http.StatusNotImplemented, "voice_not_wired", "voice conversation service is not wired")
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil || body.SessionID == "" {
		writeVoiceError(w, http.StatusBadRequest, "invalid_request", "a non-empty session_id is required")
		return
	}
	s.voiceConversation.Start(body.SessionID)
	writeJSON(w, voiceConversationStatus{Active: true, Session: s.voiceConversation.Active()})
}

// handleVoiceConversationStop deactivates conversation mode.
//
// Expected:
//   - w and r are wired by the mux.
//
// Returns:
//   - 200 with the conversation status.
//   - 501 voice_not_wired when no conversation service is wired.
//
// Side effects:
//   - Clears the active conversation session.
func (s *Server) handleVoiceConversationStop(w http.ResponseWriter, _ *http.Request) {
	if s.voiceConversation == nil {
		writeVoiceError(w, http.StatusNotImplemented, "voice_not_wired", "voice conversation service is not wired")
		return
	}
	s.voiceConversation.Stop()
	writeJSON(w, voiceConversationStatus{Active: false})
}

// handleVoiceConversationTurn accepts a WAV upload plus optional
// speak_reply form flag, runs STT, dispatches the transcript to the
// bound chat session, and returns the reply (spoken when enabled).
//
// Expected:
//   - w and r are wired by the mux.
//
// Returns:
//   - 200 with transcript, reply, and audio when speaking.
//   - 400 invalid_request for missing or non-WAV audio.
//   - 409 conversation_inactive when no session is active.
//   - 501 voice_not_wired when no conversation service is wired.
//   - 503 voice_unavailable when no STT binary resolves.
//   - 500 internal on dispatch failure.
//
// Side effects:
//   - Spawns the STT child, a chat turn, and optionally the
//     synthesiser.
func (s *Server) handleVoiceConversationTurn(w http.ResponseWriter, r *http.Request) {
	if s.voiceConversation == nil {
		writeVoiceError(w, http.StatusNotImplemented, "voice_not_wired", "voice conversation service is not wired")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxVoiceConversationTurnBytes+voiceUploadBodySlack)
	audio, errErr := readVoiceUpload(r)
	if errErr != nil {
		writeVoiceError(w, errErr.status, errErr.code, errErr.msg)
		return
	}
	result, err := s.voiceConversation.Turn(audio, r.FormValue("speak_reply") == "true")
	if err != nil {
		switch {
		case errors.Is(err, voice.ErrConversationInactive):
			writeVoiceError(w, http.StatusConflict, "conversation_inactive", "conversation mode is not active; start it first")
		case errors.Is(err, voice.ErrSTTUnavailable):
			writeVoiceError(w, http.StatusServiceUnavailable, "voice_unavailable", "speech-to-text is unavailable; install whisper or configure the STT command")
		default:
			slog.Error("voice conversation turn failed", "error", err)
			writeVoiceError(w, http.StatusInternalServerError, "internal", "conversation turn failed")
		}
		return
	}
	writeJSON(w, voiceConversationTurnResponse{Transcript: result.Transcript, Reply: result.Reply, Audio: result.Audio})
}
