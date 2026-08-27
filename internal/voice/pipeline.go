package voice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	dispatchpkg "github.com/baphled/flowstate/internal/dispatch"
)

// Dispatcher is the minimal dispatch surface the talk pipeline needs.
// Production wires *dispatch.Dispatcher (which satisfies this via its
// DispatchEphemeral method); the narrow local interface keeps the
// voice package free of a hard dependency on the dispatch package's
// wider surface and lets step definitions spy on calls.
type Dispatcher interface {
	// DispatchEphemeral hands a transcript to the dispatcher as a
	// fresh ephemeral turn.
	DispatchEphemeral(ctx context.Context, req dispatchpkg.DispatchRequest, consumer interface{}) (dispatchpkg.EphemeralHandle, error)
}

// Pipeline captures audio, transcribes it, and hands the transcript
// to the Dispatcher. It is the "one talk turn" unit the CLI and API
// surfaces both call.
type Pipeline struct {
	// Capture is the resolved capture command template.
	Capture string
	// STT is the resolved STT command template; empty defers to
	// env/defaults inside NewSTTTool.
	STT string
	// MaxDuration bounds each recording; zero means 30s.
	MaxDuration time.Duration
}

// NewPipeline builds a talk pipeline from explicit command templates.
// Empty strings defer to environment overrides and PATH probes inside
// the respective tools, matching the env > config > default contract.
//
// Expected:
//   - capture is a capture command template (may be empty).
//   - stt is an STT command template (may be empty).
//
// Returns:
//   - A *Pipeline with MaxDuration defaulted to 30s.
//
// Side effects:
//   - None; resolution happens at RunTurn time.
func NewPipeline(capture, stt string) *Pipeline {
	return &Pipeline{Capture: capture, STT: stt, MaxDuration: 30 * time.Second}
}

// RunTurn performs one capture → transcribe → dispatch cycle. The
// transcript is dispatched with ScanMentions true so "@swarm" speech
// routes to the right target without a separate UI step.
//
// Expected:
//   - ctx is non-nil; cancellation aborts capture.
//   - d is a non-nil Dispatcher.
//
// Returns:
//   - The transcript that was dispatched.
//   - ErrCaptureUnavailable when no capture binary resolves.
//   - ErrSTTUnavailable when no STT binary resolves.
//   - The dispatcher's error verbatim when dispatch fails.
//
// Side effects:
//   - Spawns capture and STT child processes; removes the temporary
//     WAV after transcription.
func (p *Pipeline) RunTurn(ctx context.Context, d Dispatcher) (string, error) {
	if d == nil {
		return "", errors.New("voice: nil dispatcher")
	}
	capTool, err := NewCaptureTool(p.Capture)
	if err != nil {
		return "", err
	}
	rec, err := capTool.Record(ctx, p.MaxDuration)
	if err != nil {
		return "", err
	}
	defer rec.Cleanup()

	audio, err := os.ReadFile(rec.Path)
	if err != nil {
		return "", fmt.Errorf("voice: read recording: %w", err)
	}
	return p.DispatchAudio(ctx, d, audio)
}

// DispatchAudio transcribes caller-supplied WAV audio and dispatches
// the transcript as a fresh ephemeral turn. It is the shared
// STT-and-dispatch half of a talk turn, split from host-mic capture
// so API callers (browser uploads) and the CLI share one path.
//
// Expected:
//   - ctx is non-nil; cancellation aborts the STT child.
//   - d is a non-nil Dispatcher.
//   - audio is WAV file bytes; empty audio is a caller error.
//
// Returns:
//   - The transcript that was dispatched.
//   - ErrSTTUnavailable when no STT binary resolves.
//   - The dispatcher's error verbatim when dispatch fails.
//
// Side effects:
//   - Spills audio to a temporary WAV, spawns the STT child, and
//     removes the temporary file on every exit path.
func (p *Pipeline) DispatchAudio(ctx context.Context, d Dispatcher, audio []byte) (string, error) {
	if d == nil {
		return "", errors.New("voice: nil dispatcher")
	}
	if len(audio) == 0 {
		return "", errors.New("voice: empty audio upload")
	}
	tmp, err := os.CreateTemp("", "flowstate-voice-audio-*.wav")
	if err != nil {
		return "", fmt.Errorf("voice: stage audio: %w", err)
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.Write(audio); err != nil {
		tmp.Close()
		return "", fmt.Errorf("voice: stage audio: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("voice: stage audio: %w", err)
	}

	sttTool, err := NewSTTTool(p.STT)
	if err != nil {
		return "", err
	}
	transcript, err := sttTool.Transcribe(ctx, path)
	if err != nil {
		return "", err
	}

	req := dispatchpkg.DispatchRequest{
		Content:      transcript,
		ScanMentions: true,
	}
	handle, err := d.DispatchEphemeral(ctx, req, nil)
	if err != nil {
		return transcript, fmt.Errorf("voice: dispatch: %w", err)
	}
	if handle.Done != nil {
		select {
		case <-ctx.Done():
			return transcript, ctx.Err()
		case <-handle.Done:
		}
	}
	return transcript, nil
}
