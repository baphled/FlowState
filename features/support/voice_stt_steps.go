//go:build e2e

package support

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/voice"
)

// sttSteps holds scenario state for the STT wrapper scenarios: the
// configured tool plus the outcome of the latest transcription.
type sttSteps struct {
	tool        *voice.STTTool
	wavPath     string
	transcript  string
	transErr    error
	transcribed bool
}

// sttState is the scenario-scoped state, reset per scenario.
var sttState = &sttSteps{}

// fakeSTTCommand installs a whisper-cli fake emitting text on stdout.
func (s *sttSteps) fakeSTTCommand(text string) error {
	dir, err := os.MkdirTemp("", "voice-stt-steps-*")
	if err != nil {
		return err
	}
	bin := filepath.Join(dir, "whisper-cli")
	script := "#!/bin/sh\nprintf '%s'\n"
	if err := os.WriteFile(bin, []byte(sprintfSTT(script, text)), 0o755); err != nil {
		return err
	}
	tool, err := voice.NewSTTTool(bin + " {file}")
	if err != nil {
		return err
	}
	s.tool = tool
	return nil
}

// sprintfSTT formats the shell script with the transcript payload.
func sprintfSTT(script, text string) string {
	return strings.Replace(script, "%s", text, 1)
}

// noSTTCommand clears any configured STT command. The wrapper itself
// refuses construction without a command, so the step records the
// expected unavailability sentinel and leaves the tool nil.
func (s *sttSteps) noSTTCommand() error {
	tool, err := voice.NewSTTTool("")
	if err == nil {
		s.tool = tool
		return nil
	}
	s.tool = nil
	if errors.Is(err, voice.ErrSTTUnavailable) {
		return nil
	}
	return err
}

// writeWAV seeds a minimal WAV fixture for transcription.
func (s *sttSteps) writeWAV() error {
	dir, err := os.MkdirTemp("", "voice-stt-wav-*")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "recording.wav")
	if err := os.WriteFile(path, []byte("RIFFfakeWAV"), 0o644); err != nil {
		return err
	}
	s.wavPath = path
	return nil
}

// transcribe drives the wrapper over the recorded WAV fixture. An
// unconfigured wrapper surfaces the unavailability sentinel directly.
func (s *sttSteps) transcribe() error {
	if s.tool == nil {
		s.transcribed = true
		s.transErr = voice.ErrSTTUnavailable
		return nil
	}
	if err := s.writeWAV(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transcript, err := s.tool.Transcribe(ctx, s.wavPath)
	s.transcript = transcript
	s.transErr = err
	s.transcribed = true
	return nil
}

// transcriptIs asserts the wrapper returned the quoted transcript.
func (s *sttSteps) transcriptIs(want string) error {
	if !s.transcribed {
		return errors.New("no transcription was attempted")
	}
	if s.transErr != nil {
		return s.transErr
	}
	if s.transcript != want {
		return errors.New("expected transcript " + want + ", got " + s.transcript)
	}
	return nil
}

// transcriptionFailsWith asserts the failure wraps the quoted error.
func (s *sttSteps) transcriptionFailsWith(target error) error {
	if !s.transcribed {
		return errors.New("no transcription was attempted")
	}
	if s.transErr == nil {
		return errors.New("expected a transcription failure, got none")
	}
	if !errors.Is(s.transErr, target) {
		return errors.New("expected failure wrapping " + target.Error() + ", got " + s.transErr.Error())
	}
	return nil
}

// resetSTTState installs a fresh scenario state, invoked before every
// scenario so bindings capture the current receiver.
func resetSTTState(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
	sttState = &sttSteps{}
	return bddCtx, nil
}

// RegisterSTTSteps wires the STT wrapper steps onto the scenario
// context.
func RegisterSTTSteps(ctx *godog.ScenarioContext) {
	ctx.Before(resetSTTState)
	ctx.Step(`^a fake STT command that emits "([^"]*)" on stdout$`, func(text string) error {
		return sttState.fakeSTTCommand(text)
	})
	ctx.Step(`^a fake STT command that emits nothing on stdout$`, func() error {
		return sttState.fakeSTTCommand("")
	})
	ctx.Step(`^the STT wrapper is unconfigured$`, func() error {
		return sttState.noSTTCommand()
	})
	ctx.Step(`^I transcribe a recorded WAV file$`, func() error {
		return sttState.transcribe()
	})
	ctx.Step(`^the transcript is "([^"]*)"$`, func(want string) error {
		return sttState.transcriptIs(want)
	})
	ctx.Step(`^transcription fails with an empty-transcript error$`, func() error {
		return sttState.transcriptionFailsWith(voice.ErrEmptyTranscript)
	})
	ctx.Step(`^transcription fails with an error mentioning whisper-cli$`, func() error {
		return sttState.transcriptionFailsWith(voice.ErrSTTUnavailable)
	})
}
