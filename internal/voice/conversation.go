// Package voice — conversation mode: a server-side session that
// ties STT, chat dispatch, and TTS together so a browser user can
// speak with the agent and hear the reply.
package voice

import (
	"context"
	"errors"
	"sync"
)

// ErrConversationInactive reports a turn submitted while no
// conversation session is active.
var ErrConversationInactive = errors.New("voice: conversation mode is not active")

// ConversationTurnDispatcher hands a voice transcript to the chat
// machinery and returns the agent's reply text.
type ConversationTurnDispatcher interface {
	// ConversationReply dispatches the transcript to the bound chat
	// session and returns the assistant reply text.
	ConversationReply(ctx context.Context, sessionID, transcript string) (string, error)
}

// ConversationTurn defines one voice turn: STT audio bytes.
type ConversationTurn struct {
	// Audio is the WAV upload for STT.
	Audio []byte
	// SpeakReply toggles TTS of the agent reply.
	SpeakReply bool
}

// ConversationResult reports one conversation turn outcome.
type ConversationResult struct {
	// Transcript is the local STT output.
	Transcript string
	// Reply is the agent reply text (empty when none).
	Reply string
	// Audio is the synthesised reply WAV when SpeakReply was set
	// and synthesis succeeded; nil otherwise.
	Audio []byte
}

// ConversationService manages at most one active conversation
// session per service instance and runs each turn as STT → chat
// send → optional TTS.
type ConversationService struct {
	// STT is the resolved STT command template; empty defers to
	// env/defaults inside NewSTTTool.
	STT string
	// Dispatcher routes transcripts to the chat session.
	Dispatcher ConversationTurnDispatcher
	// Synthesiser renders reply text as WAV bytes; nil skips TTS.
	Synthesiser interface {
		// Synthesize renders text as concatenated WAV bytes.
		Synthesize(ctx context.Context, text string) ([]byte, error)
	}

	mu      sync.Mutex
	session string
}

// NewConversationService builds a conversation service from the
// STT command template, dispatcher, and optional synthesiser.
//
// Expected:
//   - dispatcher is non-nil.
//
// Returns:
//   - A *ConversationService with no active session.
//
// Side effects:
//   - None.
func NewConversationService(stt string, d ConversationTurnDispatcher, synth interface {
	Synthesize(ctx context.Context, text string) ([]byte, error)
}) *ConversationService {
	return &ConversationService{STT: stt, Dispatcher: d, Synthesiser: synth}
}

// Start activates conversation mode for the given session.
//
// Expected:
//   - sessionID is non-empty.
//
// Returns:
//   - ErrConversationInactive never; a duplicate start rebinds the
//     active session.
//
// Side effects:
//   - Records the active session under the service lock.
func (c *ConversationService) Start(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = sessionID
}

// Stop deactivates conversation mode.
//
// Side effects:
//   - Clears the active session under the service lock.
func (c *ConversationService) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = ""
}

// Active reports the active session ID ("" when inactive).
//
// Returns:
//   - The active session ID, or the empty string.
//
// Side effects:
//   - None.
func (c *ConversationService) Active() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session
}

// Turn runs one voice conversation turn: STT the audio, dispatch
// the transcript to the bound chat session, and synthesise the
// reply when requested.
//
// Expected:
//   - ctx is non-nil; cancellation aborts STT and synthesis.
//   - turn.Audio is WAV bytes; empty audio is a caller error.
//
// Returns:
//   - A ConversationResult carrying the transcript, reply, and
//     synthesised audio when applicable.
//   - ErrConversationInactive when no session is active.
//   - ErrSTTUnavailable when no STT binary resolves.
//   - The dispatcher's error verbatim when dispatch fails.
//   - TTS errors do not fail the turn; the reply is returned as
//     text with a nil Audio field so the SPA can degrade to text.
//
// Side effects:
//   - Spills audio to a temporary WAV, spawns the STT child (and
//     the synthesiser when speaking), and dispatches a chat turn.
func (c *ConversationService) Turn(ctx context.Context, turn ConversationTurn) (ConversationResult, error) {
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if session == "" {
		return ConversationResult{}, ErrConversationInactive
	}
	if len(turn.Audio) == 0 {
		return ConversationResult{}, errors.New("voice: empty audio upload")
	}
	transcript, err := transcribeAudio(ctx, c.STT, turn.Audio)
	if err != nil {
		return ConversationResult{}, err
	}
	result := ConversationResult{Transcript: transcript}
	reply, err := c.Dispatcher.ConversationReply(ctx, session, transcript)
	if err != nil {
		return result, err
	}
	result.Reply = reply
	if !turn.SpeakReply || c.Synthesiser == nil || reply == "" {
		return result, nil
	}
	wav, err := c.Synthesiser.Synthesize(ctx, reply)
	if err == nil {
		result.Audio = wav
	}
	return result, nil
}
