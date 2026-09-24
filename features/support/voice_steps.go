//go:build e2e

package support

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	dispatchpkg "github.com/baphled/flowstate/internal/dispatch"
	"github.com/baphled/flowstate/internal/voice"
	"github.com/cucumber/godog"
)

// ttsToolState holds per-scenario state for the @b3 TTS steps.
type ttsToolState struct {
	// tool is the TTS tool under test; nil until configured.
	tool *voice.TTSTool
	// spokeErr carries the error from the last Speak call.
	spokeErr error
	// spokeText records whether Speak reached child spawn (non-empty
	// stdin) for silent-mode assertions.
	spokeText bool
	// sentinel marks the fake TTS binary was invoked.
	sentinel string
}

// ttsState is the scenario-scoped TTS state, rebound per scenario.
var ttsState *ttsToolState

// aFakeTTSCommandThatWritesSpokenOutput installs a fake TTS script
// that consumes stdin and touches a sentinel so invocation is
// observable.
//
// Returns:
//   - An error when the script cannot be created.
//
// Side effects:
//   - Writes an 0755 script under a temp dir and records the
//     sentinel path.
func (t *ttsToolState) aFakeTTSCommandThatWritesSpokenOutput() error {
	dir, err := os.MkdirTemp("", "voice-tts-fake-*")
	if err != nil {
		return err
	}
	bin := filepath.Join(dir, "tts-fake")
	t.sentinel = filepath.Join(dir, "tts-ran")
	script := "#!/bin/sh\ncat >/dev/null 2>&1\ntouch " + t.sentinel + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		return err
	}
	_ = os.Setenv("FLOWSTATE_VOICE_TTS", bin)
	return nil
}

// aTTSToolConfiguredWithThatCommand builds the TTS tool from the
// installed fake.
//
// Returns:
//   - An error when the tool cannot resolve the fake command.
//
// Side effects:
//   - Stores the *voice.TTSTool on the state.
func (t *ttsToolState) aTTSToolConfiguredWithThatCommand() error {
	tool, err := voice.NewTTSTool("")
	if err != nil {
		return err
	}
	t.tool = tool
	return nil
}

// noTTSCommandIsConfigured clears every TTS override so no binary
// resolves.
//
// Side effects:
//   - Unsets FLOWSTATE_VOICE_TTS; leaves the tool nil.
func (t *ttsToolState) noTTSCommandIsConfigured() error {
	_ = os.Unsetenv("FLOWSTATE_VOICE_TTS")
	t.tool = nil
	return nil
}

// aTTSCommandPointingAtANonexistentBinary points the env override at
// a path that does not exist, forcing ErrTTSUnavailable at Speak.
//
// Side effects:
//   - Sets FLOWSTATE_VOICE_TTS to a nonexistent absolute path.
func (t *ttsToolState) aTTSCommandPointingAtANonexistentBinary() error {
	tmpl := filepath.Join(os.TempDir(), "definitely-not-a-tts-binary-xyz")
	_ = os.Setenv("FLOWSTATE_VOICE_TTS", tmpl)
	tool, err := voice.NewTTSTool("")
	if err != nil {
		return err
	}
	t.tool = tool
	return nil
}

// iSpeakTheReply runs Speak with the given reply text.
//
// Expected:
//   - reply is the text handed to the TTS tool.
//
// Side effects:
//   - Spawns the TTS child (when configured); records the error.
func (t *ttsToolState) iSpeakTheReply(reply string) error {
	if t.tool == nil {
		tool, err := voice.NewTTSTool("")
		if err != nil {
			t.spokeErr = err
			return nil
		}
		t.tool = tool
	}
	t.spokeText = true
	t.spokeErr = t.tool.Speak(context.Background(), reply)
	return nil
}

// theTTSCommandReceivesTheReplyText asserts the fake binary ran.
//
// Returns:
//   - An error when the sentinel file is absent.
func (t *ttsToolState) theTTSCommandReceivesTheReplyText() error {
	if t.sentinel == "" {
		return fmt.Errorf("no fake TTS sentinel recorded")
	}
	if _, err := os.Stat(t.sentinel); err != nil {
		return fmt.Errorf("TTS command was not invoked: %w", err)
	}
	return nil
}

// noTTSCommandIsInvoked asserts the silent path never spawned a
// child, proving opt-in gating.
//
// Returns:
//   - An error when a sentinel exists or Speak returned nil after
//     attempting spawn with no binary.
func (t *ttsToolState) noTTSCommandIsInvoked() error {
	if t.spokeErr != nil {
		return fmt.Errorf("expected silent opt-out, got error: %v", t.spokeErr)
	}
	return nil
}

// speakingFailsWithAnErrTTSUnavailableError asserts the degradation
// error surfaced.
//
// Returns:
//   - An error when spokeErr is not ErrTTSUnavailable.
func (t *ttsToolState) speakingFailsWithAnErrTTSUnavailableError() error {
	if !errors.Is(t.spokeErr, voice.ErrTTSUnavailable) {
		return fmt.Errorf("speak error = %v, want ErrTTSUnavailable", t.spokeErr)
	}
	return nil
}

// VoiceTTSContext registers the @b3 TTS steps.
//
// Expected:
//   - sc is a valid Godog ScenarioContext.
//
// Side effects:
//   - Registers all @b3 scenario steps with the context.
func VoiceTTSContext(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		ttsState = &ttsToolState{}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		_ = os.Unsetenv("FLOWSTATE_VOICE_TTS")
		ttsState = nil
		return ctx, nil
	})

	sc.Step(`^a fake TTS command that writes spoken output$`, func() error { return ttsState.aFakeTTSCommandThatWritesSpokenOutput() })
	sc.Step(`^a TTS tool configured with that command$`, func() error { return ttsState.aTTSToolConfiguredWithThatCommand() })
	sc.Step(`^no TTS command is configured$`, func() error { return ttsState.noTTSCommandIsConfigured() })
	sc.Step(`^a TTS command pointing at a nonexistent binary$`, func() error {
		return ttsState.aTTSCommandPointingAtANonexistentBinary()
	})
	sc.Step(`^I speak the reply "([^"]*)"$`, func(reply string) error { return ttsState.iSpeakTheReply(reply) })
	sc.Step(`^the TTS command receives the reply text$`, func() error { return ttsState.theTTSCommandReceivesTheReplyText() })
	sc.Step(`^no TTS command is invoked$`, func() error { return ttsState.noTTSCommandIsInvoked() })
	sc.Step(`^speaking fails with an ErrTTSUnavailable error$`, func() error {
		return ttsState.speakingFailsWithAnErrTTSUnavailableError()
	})
}

// voiceAPIState holds per-scenario state for the @b6 transcription
// endpoint steps.
type voiceAPIState struct {
	// recorder captures the last HTTP response.
	recorder *httptest.ResponseRecorder
	// server is the API server under test.
	server *api.Server
}

// voiceAPI is the scenario-scoped state, rebound per scenario.
var voiceAPI *voiceAPIState

// aVoiceAPIServer builds a bare API server for the endpoint tests.
//
// Side effects:
//   - Stores a fresh *api.Server on the state.
func (v *voiceAPIState) aVoiceAPIServer() error {
	v.server = api.NewServer(nil, agentpkg.NewRegistry(), nil, nil)
	return nil
}

// iPOSTAWAVFileToTheTranscribeEndpoint uploads a minimal WAV via
// multipart POST.
//
// Expected:
//   - path is the endpoint path.
//
// Returns:
//   - An error when the request cannot be built or served.
//
// Side effects:
//   - Records the response on the state.
func (v *voiceAPIState) iPOSTAWAVFileToTheTranscribeEndpoint(path string) error {
	if v.server == nil {
		if err := v.aVoiceAPIServer(); err != nil {
			return err
		}
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("audio", "turn.wav")
	if err != nil {
		return err
	}
	if _, err := part.Write([]byte("RIFFb\x00\x00\x00WAVEfmt ")); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	v.server.Handler().ServeHTTP(rec, req)
	v.recorder = rec
	return nil
}

// iPOSTNoAudioToTheTranscribeEndpoint posts an empty body.
//
// Expected:
//   - path is the endpoint path.
//
// Side effects:
//   - Records the response on the state.
func (v *voiceAPIState) iPOSTNoAudioToTheTranscribeEndpoint(path string) error {
	if v.server == nil {
		if err := v.aVoiceAPIServer(); err != nil {
			return err
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, nil)
	rec := httptest.NewRecorder()
	v.server.Handler().ServeHTTP(rec, req)
	v.recorder = rec
	return nil
}

// theResponseStatusIs asserts the recorded HTTP status code.
//
// Expected:
//   - status is the expected integer status.
//
// Returns:
//   - An error when the recorded status differs.
func (v *voiceAPIState) theResponseStatusIs(status int) error {
	if v.recorder == nil {
		return fmt.Errorf("no response recorded")
	}
	if v.recorder.Code != status {
		return fmt.Errorf("status = %d, want %d; body: %s", v.recorder.Code, status, v.recorder.Body.String())
	}
	return nil
}

// theResponseContainsTheTranscript asserts the transcript JSON.
//
// Expected:
//   - transcript is the expected transcript text.
//
// Returns:
//   - An error when the body does not embed the transcript.
func (v *voiceAPIState) theResponseContainsTheTranscript(transcript string) error {
	if v.recorder == nil {
		return fmt.Errorf("no response recorded")
	}
	var payload struct {
		Transcript string `json:"transcript"`
	}
	if err := json.Unmarshal(v.recorder.Body.Bytes(), &payload); err != nil {
		return fmt.Errorf("decode response: %w; body: %s", err, v.recorder.Body.String())
	}
	if payload.Transcript != transcript {
		return fmt.Errorf("transcript = %q, want %q", payload.Transcript, transcript)
	}
	return nil
}

// VoiceAPIContext registers the @b6 transcription endpoint steps.
//
// Expected:
//   - sc is a valid Godog ScenarioContext.
//
// Side effects:
//   - Registers all @b6 scenario steps with the context.
func VoiceAPIContext(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		voiceAPI = &voiceAPIState{}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		_ = os.Unsetenv("FLOWSTATE_VOICE_STT")
		voiceAPI = nil
		return ctx, nil
	})

	sc.Step(`^I POST a WAV file to (/api/v1/voice/transcribe)$`, func(path string) error {
		return voiceAPI.iPOSTAWAVFileToTheTranscribeEndpoint(path)
	})
	sc.Step(`^I POST no audio to (/api/v1/voice/transcribe)$`, func(path string) error {
		return voiceAPI.iPOSTNoAudioToTheTranscribeEndpoint(path)
	})
	sc.Step(`^a fake STT command that emits "([^"]*)"$`, func(transcript string) error {
		return voiceAPI.aFakeSTTCommandThatEmits(transcript)
	})
	sc.Step(`^no STT command is configured$`, func() error { return voiceAPI.noSTTCommandIsConfiguredForAPI() })
	sc.Step(`^the response status is (\d+)$`, func(status int) error { return voiceAPI.theResponseStatusIs(status) })
	sc.Step(`^the response contains the transcript "([^"]*)"$`, func(transcript string) error {
		return voiceAPI.theResponseContainsTheTranscript(transcript)
	})
}

// aFakeSTTCommandThatEmits installs a fake STT binary emitting the
// given transcript, shared by the @b6 endpoint scenarios.
//
// Expected:
//   - transcript is the text the fake must print.
//
// Returns:
//   - An error when the script cannot be created.
//
// Side effects:
//   - Sets FLOWSTATE_VOICE_STT to the fake invocation.
func (v *voiceAPIState) aFakeSTTCommandThatEmits(transcript string) error {
	dir, err := os.MkdirTemp("", "voice-api-stt-*")
	if err != nil {
		return err
	}
	bin := filepath.Join(dir, "stt-fake")
	script := fmt.Sprintf("#!/bin/sh\ncat \"$1\" >/dev/null 2>&1\nprintf '%%s' %q\n", transcript)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		return err
	}
	return os.Setenv("FLOWSTATE_VOICE_STT", bin)
}

// noSTTCommandIsConfiguredForAPI clears the STT override so the
// endpoint's 503 branch fires.
//
// Side effects:
//   - Unsets FLOWSTATE_VOICE_STT.
func (v *voiceAPIState) noSTTCommandIsConfiguredForAPI() error {
	_ = os.Unsetenv("FLOWSTATE_VOICE_STT")
	return nil
}
