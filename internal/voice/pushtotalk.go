package voice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// PushToTalkSession models the two-keypress recording lifecycle of
// `flowstate talk`: Start spawns the capture in the background,
// StopAndTranscribe ends it and hands the WAV to the STT tool, and
// Close guarantees the temporary file and any held terminal state are
// released no matter which exit path the caller takes.
type PushToTalkSession struct {
	// Capture is the capture command template ("{file}" placeholder).
	Capture string
	// STT is the STT command template; empty resolves env/defaults.
	STT string
	// MaxDuration bounds recording when the stop keypress never
	// arrives; zero means 30s.
	MaxDuration time.Duration

	active *ActiveRecording
	closed bool
}

// NewPushToTalkSession builds a push-to-talk session from explicit
// command templates. Empty strings defer to env overrides and PATH
// probes inside the respective tools.
//
// Expected:
//   - capture and stt are command templates (may be empty).
//   - maxDuration bounds each recording; zero clamps to 30s.
//
// Returns:
//   - A configured *PushToTalkSession.
//
// Side effects:
//   - None; subprocesses spawn at Start time.
func NewPushToTalkSession(capture, stt string, maxDuration time.Duration) *PushToTalkSession {
	if maxDuration <= 0 {
		maxDuration = 30 * time.Second
	}
	return &PushToTalkSession{Capture: capture, STT: stt, MaxDuration: maxDuration}
}

// Start spawns the background capture — the first keypress.
//
// Expected:
//   - ctx is non-nil; cancellation kills the capture process.
//
// Returns:
//   - An error when capture is already running, unavailable, or the
//     process fails to spawn.
//
// Side effects:
//   - Creates a temporary WAV and spawns the capture binary.
func (s *PushToTalkSession) Start(ctx context.Context) error {
	if s == nil {
		return errors.New("voice: nil push-to-talk session")
	}
	if s.active != nil {
		return errors.New("voice: recording already in progress")
	}
	if s.closed {
		return errors.New("voice: session closed")
	}
	tool, err := NewCaptureTool(s.Capture)
	if err != nil {
		return err
	}
	tool.MaxDuration = s.MaxDuration
	rec, err := tool.StartRecording(ctx)
	if err != nil {
		return err
	}
	s.active = rec
	return nil
}

// StopAndTranscribe ends the capture and transcribes the WAV — the
// second keypress.
//
// Expected:
//   - ctx is non-nil; Start has been called.
//
// Returns:
//   - The transcript string.
//   - An error when no recording is active or transcription fails.
//
// Side effects:
//   - Kills the capture process and removes the temporary WAV.
func (s *PushToTalkSession) StopAndTranscribe(ctx context.Context) (string, error) {
	if s == nil || s.active == nil {
		return "", errors.New("voice: no active recording")
	}
	rec, err := s.active.Stop()
	s.active = nil
	if err != nil {
		rec.Cleanup()
		return "", err
	}
	defer rec.Cleanup()
	sttTool, err := NewSTTTool(s.STT)
	if err != nil {
		return "", err
	}
	return sttTool.Transcribe(ctx, rec.Path)
}

// Close releases all session resources and marks the terminal state
// restored. Idempotent.
//
// Expected:
//   - Called on every exit path; safe to call twice.
//
// Returns:
//   - An error when an abandoned background capture could not be
//     stopped; the session is marked closed regardless.
//
// Side effects:
//   - Stops any active recording and removes its temporary WAV.
func (s *PushToTalkSession) Close() error {
	if s == nil {
		return nil
	}
	s.closed = true
	if s.active == nil {
		return nil
	}
	rec, err := s.active.Stop()
	s.active = nil
	if rec != nil {
		rec.Cleanup()
	}
	return err
}

// ResolveTurnFallback attempts one voice turn and, when voice is
// unavailable (capture or STT binary absent), emits the fallback
// warning to out and falls back to the supplied text-turn reader.
//
// Expected:
//   - ctx is non-nil; out is a non-nil writer; textTurn yields the
//     fallback turn content (commonly one line from stdin).
//
// Returns:
//   - The transcript from whichever source succeeded.
//   - The textTurn error when both voice and fallback fail.
//
// Side effects:
//   - Spawns capture/STT subprocesses when voice resolves; writes
//     the fallback warning to out when it does not.
func ResolveTurnFallback(ctx context.Context, out io.Writer, textTurn func() (string, error)) (string, error) {
	pipeline := NewPipeline("", "")
	capTool, capErr := NewCaptureTool(pipeline.Capture)
	if capErr == nil {
		rec, recErr := capTool.Record(ctx, pipeline.MaxDuration)
		if recErr == nil {
			defer rec.Cleanup()
			sttTool, sttErr := NewSTTTool(pipeline.STT)
			if sttErr == nil {
				transcript, err := sttTool.Transcribe(ctx, rec.Path)
				if err == nil {
					return transcript, nil
				}
				fmt.Fprintf(out, "voice unavailable, falling back to text-only mode: %v\n", err)
				return textTurn()
			}
			fmt.Fprintf(out, "voice unavailable, falling back to text-only mode: %v\n", sttErr)
			return textTurn()
		}
		fmt.Fprintf(out, "voice unavailable, falling back to text-only mode: %v\n", recErr)
		return textTurn()
	}
	fmt.Fprintf(out, "voice unavailable, falling back to text-only mode: %v\n", capErr)
	return textTurn()
}
