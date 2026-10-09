//go:build e2e

package support

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/voice"
)

// voiceSTTState carries the STT scenario state across steps.
type voiceSTTState struct {
	// transcript holds the last transcription result.
	transcript string
	// err holds the last transcription error.
	err error
	// stt restores the prior FLOWSTATE_VOICE_STT value.
	stt string
	// hadSTT records whether the env var was previously set.
	hadSTT bool
	// wav is the recorded WAV path handed to the tool.
	wav string
}

// voiceSTT is the scenario-scoped STT state.
var voiceSTT *voiceSTTState

// sttStateKey types the scenario context slot for STT state.
type sttStateKey struct{}

// aFakeSTTCommandThatEmits configures a shell fake emitting text.
//
// Expected:
//   - text is a single stdout line without embedded quotes.
//
// Returns:
//   - nil on success; the temp-dir or write error otherwise.
//
// Side effects:
//   - Creates an executable fake STT script and exports
//     FLOWSTATE_VOICE_STT pointing at it.
func (s *voiceSTTState) aFakeSTTCommandThatEmits(text string) error {
	dir, err := os.MkdirTemp("", "voice-stt-")
	if err != nil {
		return err
	}
	script := filepath.Join(dir, "fake-stt")
	payload := "#!/bin/sh\necho \"" + text + "\"\n"
	if err := os.WriteFile(script, []byte(payload), 0o755); err != nil {
		return err
	}
	s.sttEnv(script + " {file}")
	return nil
}

// aFakeSTTCommandThatEmitsNothingOnStdout configures a silent fake.
//
// Expected:
//   - The fake exits zero with no stdout, exercising the empty
//     transcript error path.
//
// Returns:
//   - nil on success; the temp-dir or write error otherwise.
//
// Side effects:
//   - Creates an executable fake STT script and exports
//     FLOWSTATE_VOICE_STT pointing at it.
func (s *voiceSTTState) aFakeSTTCommandThatEmitsNothingOnStdout() error {
	dir, err := os.MkdirTemp("", "voice-stt-")
	if err != nil {
		return err
	}
	script := filepath.Join(dir, "fake-stt-silent")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		return err
	}
	s.sttEnv(script + " {file}")
	return nil
}

// sttEnv sets or clears FLOWSTATE_VOICE_STT, recording the prior value.
//
// Expected:
//   - value is a command template; empty clears the variable.
//
// Side effects:
//   - Mutates the process environment for the scenario.
func (s *voiceSTTState) sttEnv(value string) {
	if !s.hadSTT {
		s.stt, s.hadSTT = os.Getenv("FLOWSTATE_VOICE_STT"), true
	}
	if value == "" {
		os.Unsetenv("FLOWSTATE_VOICE_STT")
		return
	}
	os.Setenv("FLOWSTATE_VOICE_STT", value) //nolint:errcheck // test-only env mutation
}

// iTranscribeARecordedWAVFile runs the STT tool over a fixture WAV.
//
// Expected:
//   - FLOWSTATE_VOICE_STT resolves to a fake set by an earlier step;
//     tool-construction and transcription errors are captured on the
//     state rather than failing the step.
//
// Returns:
//   - nil; transcription failures are recorded in s.err for the
//     assertion steps.
//
// Side effects:
//   - Writes a fixture WAV and spawns the fake STT subprocess.
func (s *voiceSTTState) iTranscribeARecordedWAVFile() error {
	wav, err := writeSTTFixtureWAV()
	if err != nil {
		return err
	}
	s.wav = wav
	cmd := os.Getenv("FLOWSTATE_VOICE_STT")
	tool, err := voice.NewSTTTool(cmd)
	if err != nil {
		s.err = err
		return nil
	}
	s.transcript, s.err = tool.Transcribe(context.Background(), wav)
	return nil
}

// writeSTTFixtureWAV writes a minimal silent WAV into a temp dir.
//
// Expected:
//   - The header is a valid zero-length 16-bit mono PCM container.
//
// Returns:
//   - The fixture path and nil on success; the write error otherwise.
//
// Side effects:
//   - Creates a temp directory and file owned by the scenario.
func writeSTTFixtureWAV() (string, error) {
	dir, err := os.MkdirTemp("", "voice-stt-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "turn.wav")
	header := "RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00" +
		"\x80\xbb\x00\x00\x00\x77\x01\x00\x02\x00\x10\x00data\x00\x00\x00\x00"
	if err := os.WriteFile(path, []byte(header), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// theTranscriptIs asserts the transcription result.
//
// Expected:
//   - A prior transcription step succeeded.
//
// Returns:
//   - nil when the transcript matches want; a descriptive error
//     otherwise.
//
// Side effects:
//   - None.
func (s *voiceSTTState) theTranscriptIs(want string) error {
	if s.err != nil {
		return s.err
	}
	if s.transcript != want {
		return errors.New("transcript = " + s.transcript + ", want " + want)
	}
	return nil
}

// transcriptionFailsWithAnEmptytranscriptError asserts the empty error.
//
// Expected:
//   - A prior transcription step recorded an error.
//
// Returns:
//   - nil when the error mentions "empty"; a descriptive error
//     otherwise.
//
// Side effects:
//   - None.
func (s *voiceSTTState) transcriptionFailsWithAnEmptytranscriptError() error {
	if s.err == nil {
		return errors.New("expected an empty-transcript error, got none")
	}
	if !strings.Contains(s.err.Error(), "empty") {
		return errors.New("error does not mention empty: " + s.err.Error())
	}
	return nil
}

// transcriptionFailsWithAnErrorMentioningWhispercli asserts guidance.
//
// Expected:
//   - A prior transcription step recorded an STT-unavailable error.
//
// Returns:
//   - nil when the error mentions "whisper-cli"; a descriptive
//     error otherwise.
//
// Side effects:
//   - None.
func (s *voiceSTTState) transcriptionFailsWithAnErrorMentioningWhispercli() error {
	if s.err == nil {
		return errors.New("expected an STT-unavailable error, got none")
	}
	if !strings.Contains(s.err.Error(), "whisper-cli") {
		return errors.New("error does not mention whisper-cli: " + s.err.Error())
	}
	return nil
}

// VoiceSTTContext registers the voice STT tool steps.
//
// Expected:
//   - sc is the scenario context godog hands the suite initialiser.
//
// Side effects:
//   - Resets scenario-scoped STT state and registers six steps.
func VoiceSTTContext(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		voiceSTT = &voiceSTTState{}
		if v, ok := ctx.Value(sttStateKey{}).(*voiceSTTState); ok {
			voiceSTT = v
		}
		return context.WithValue(ctx, sttStateKey{}, voiceSTT), nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if voiceSTT.hadSTT {
			os.Setenv("FLOWSTATE_VOICE_STT", voiceSTT.stt) //nolint:errcheck // test-only env restore
		}
		voiceSTT = nil
		return ctx, nil
	})

	sc.Step(`^a fake STT command that emits "([^"]*)" on stdout$`, func(text string) error {
		return voiceSTT.aFakeSTTCommandThatEmits(text)
	})
	sc.Step(`^a fake STT command that emits nothing on stdout$`, func() error {
		return voiceSTT.aFakeSTTCommandThatEmitsNothingOnStdout()
	})
	sc.Step(`^I transcribe a recorded WAV file$`, func() error {
		return voiceSTT.iTranscribeARecordedWAVFile()
	})
	sc.Step(`^the transcript is "([^"]*)"$`, func(want string) error {
		return voiceSTT.theTranscriptIs(want)
	})
	sc.Step(`^transcription fails with an empty-transcript error$`, func() error {
		return voiceSTT.transcriptionFailsWithAnEmptytranscriptError()
	})
	sc.Step(`^transcription fails with an error mentioning whisper-cli$`, func() error {
		return voiceSTT.transcriptionFailsWithAnErrorMentioningWhispercli()
	})
}
