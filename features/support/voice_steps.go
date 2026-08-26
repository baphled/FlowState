//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	dispatchpkg "github.com/baphled/flowstate/internal/dispatch"
	"github.com/baphled/flowstate/internal/voice"
	"github.com/cucumber/godog"
)

// dispatchSpy records what the voice pipeline handed to the
// dispatcher, standing in for the production *dispatch.Dispatcher.
type dispatchSpy struct {
	requests []dispatchpkg.DispatchRequest
}

// DispatchEphemeral records the request and returns an immediately
// successful ephemeral handle.
//
// Expected:
//   - req is the dispatch request issued by the voice pipeline.
//
// Returns:
//   - An EphemeralHandle whose Done channel already carries nil.
//
// Side effects:
//   - Appends req to the spy's recorded requests.
func (s *dispatchSpy) DispatchEphemeral(_ context.Context, req dispatchpkg.DispatchRequest, _ interface{}) (dispatchpkg.EphemeralHandle, error) {
	s.requests = append(s.requests, req)
	done := make(chan error, 1)
	done <- nil
	return dispatchpkg.EphemeralHandle{Done: done}, nil
}

// DispatchSessioned records the request and returns an empty
// sessioned handle.
//
// Expected:
//   - req is the dispatch request issued by the voice pipeline.
//
// Returns:
//   - A zero-valued SessionedHandle and nil error.
//
// Side effects:
//   - Appends req to the spy's recorded requests.
func (s *dispatchSpy) DispatchSessioned(_ context.Context, req dispatchpkg.DispatchRequest, _ interface{}) (dispatchpkg.SessionedHandle, error) {
	s.requests = append(s.requests, req)
	return dispatchpkg.SessionedHandle{}, nil
}

// voiceTalkState holds per-scenario state for the voice talk
// pipeline BDD steps.
type voiceTalkState struct {
	t           *testing.T
	pipeline    *voice.Pipeline
	spy         *dispatchSpy
	err         error // set by runOneTalkTurn; read by returnsCaptureUnavailable
	sttBinDir   string
	captureBin  string
	transcript  string
	dispatchSet bool
}

// newVoiceTalkState constructs a fresh voiceTalkState bound to the
// scenario's testing.T.
//
// Expected:
//   - t is the scenario's testing.T.
//
// Returns:
//   - A pointer to a zero-value initialised voiceTalkState.
func newVoiceTalkState(t *testing.T) *voiceTalkState {
	t.Helper()
	return &voiceTalkState{t: t}
}

// fakeSTTCommandEmits installs a fake STT binary that emits the given
// transcript verbatim.
//
// Expected:
//   - transcript is the text the fake STT binary must print.
//
// Returns:
//   - Any error from temp directory or script creation.
//
// Side effects:
//   - Sets FLOWSTATE_VOICE_STT to the fake binary invocation.
func (v *voiceTalkState) fakeSTTCommandEmits(transcript string) error {
	dir, err := os.MkdirTemp("", "voice-stt-fake")
	if err != nil {
		return err
	}
	v.sttBinDir = dir
	bin := filepath.Join(dir, "stt-fake")
	script := fmt.Sprintf("#!/bin/sh\ncat /dev/null >/dev/null 2>&1\nprintf '%%s' %q\n", transcript)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		return err
	}
	v.transcript = transcript
	return os.Setenv("FLOWSTATE_VOICE_STT", bin+" {file}")
}

// dispatcherSpyWired arms the scenario with a dispatch spy.
//
// Returns:
//   - Nil; wiring cannot fail.
//
// Side effects:
//   - Sets the scenario's spy and marks dispatch as wired.
func (v *voiceTalkState) dispatcherSpyWired() error {
	v.spy = &dispatchSpy{}
	v.dispatchSet = true
	return nil
}

// runOneTalkTurn executes a single talk turn against the wired spy,
// fabricating a capture binary when none is configured.
//
// Expected:
//   - dispatcherSpyWired has already run.
//
// Returns:
//   - godog.ErrPending when no dispatcher is wired; otherwise nil.
//
// Side effects:
//   - Stores the pipeline result error in v.err.
func (v *voiceTalkState) runOneTalkTurn() error {
	if !v.dispatchSet {
		return godog.ErrPending
	}
	capture := v.captureBin
	if capture == "" {
		dir, err := os.MkdirTemp("", "voice-cap-fake")
		if err != nil {
			return err
		}
		bin := filepath.Join(dir, "cap-fake")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\nwhile [ ! -f \"$1\" ]; do sleep 0.01; done\nexit 0\n"), 0o755); err != nil {
			return err
		}
		capture = bin + " {file}"
	}
	v.pipeline = voice.NewPipeline(capture, "")
	_, v.err = v.pipeline.RunTurn(context.Background(), v.spy)
	return nil
}

// dispatcherReceivesContent asserts the first dispatch request carries
// the expected content.
//
// Expected:
//   - want is the transcript text the dispatcher must receive.
//
// Returns:
//   - An error when no request was recorded or content mismatches.
func (v *voiceTalkState) dispatcherReceivesContent(want string) error {
	if len(v.spy.requests) == 0 {
		return fmt.Errorf("no dispatch requests recorded")
	}
	got := v.spy.requests[0].Content
	if got != want {
		return fmt.Errorf("dispatch content = %q, want %q", got, want)
	}
	return nil
}

// dispatcherReivesScanMentions asserts the first dispatch request has
// the expected ScanMentions flag.
//
// Expected:
//   - want is the expected ScanMentions value.
//
// Returns:
//   - An error when the recorded flag mismatches want.
func (v *voiceTalkState) dispatcherReivesScanMentions(want bool) error {
	got := v.spy.requests[0].ScanMentions
	if got != want {
		return fmt.Errorf("ScanMentions = %v, want %v", got, want)
	}
	return nil
}

// noCaptureBinary points the pipeline at a nonexistent capture binary
// and clears any ambient capture override.
//
// Returns:
//   - Nil; configuration cannot fail.
//
// Side effects:
//   - Unsets FLOWSTATE_VOICE_CAPTURE and sets v.captureBin.
func (v *voiceTalkState) noCaptureBinary() error {
	_ = os.Unsetenv("FLOWSTATE_VOICE_CAPTURE")
	v.captureBin = "definitely-not-a-binary-xyz {file}"
	return nil
}

// returnsCaptureUnavailable asserts the last talk turn failed with
// voice.ErrCaptureUnavailable.
//
// Returns:
//   - An error when the stored pipeline error does not match.
func (v *voiceTalkState) returnsCaptureUnavailable() error {
	if !errors.Is(v.err, voice.ErrCaptureUnavailable) {
		return fmt.Errorf("pipeline error = %v, want ErrCaptureUnavailable", v.err)
	}
	return nil
}

// VoiceTalkContext registers the @b5 dispatcher-wiring steps.
//
// Expected:
//   - sc is a valid Godog ScenarioContext.
//
// Side effects:
//   - Registers all voice talk scenario steps with the context.
func VoiceTalkContext(sc *godog.ScenarioContext) {
	st := newVoiceTalkState(&testing.T{})
	sc.Step(`^a voice pipeline with a fake STT command emitting "([^"]*)"$`, st.fakeSTTCommandEmits)
	sc.Step(`^a dispatcher spy is wired$`, st.dispatcherSpyWired)
	sc.Step(`^the pipeline runs one talk turn$`, st.runOneTalkTurn)
	sc.Step(`^the dispatcher receives content "([^"]*)"$`, st.dispatcherReceivesContent)
	sc.Step(`^the dispatcher receives ScanMentions true$`, func() error { return st.dispatcherReivesScanMentions(true) })
	sc.Step(`^a voice pipeline with no capture binary$`, st.noCaptureBinary)
	sc.Step(`^the pipeline returns ErrCaptureUnavailable$`, st.returnsCaptureUnavailable)
}
