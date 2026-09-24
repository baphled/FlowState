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

// Pipeline transcribes audio and hands the transcript to the
// Dispatcher. It is the "one talk turn" unit the API surface calls.
type Pipeline struct {
	// STT is the resolved STT command template; empty defers to
	// env/defaults inside NewSTTTool.
	STT string
	// MaxDuration bounds each recording; zero means 30s.
	MaxDuration time.Duration
}

// NewPipeline builds a talk pipeline from an explicit STT command
// template. An empty string defers to environment overrides and PATH
// probes inside the tool, matching the env > config > default
// contract.
//
// Expected:
//   - stt is an STT command template (may be empty).
//
// Returns:
//   - A *Pipeline with MaxDuration defaulted to 30s.
//
// Side effects:
//   - None; resolution happens at dispatch time.
func NewPipeline(stt string) *Pipeline {
	return &Pipeline{STT: stt, MaxDuration: 30 * time.Second}
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
