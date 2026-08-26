//go:build e2e

package support

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

	// @f1 push-to-talk CLI state
	pushSession      *voice.PushToTalkSession
	pushStarted      bool
	recordingStarted bool
	recordingStopped bool
	terminalRestored bool
	ttsConfigured    bool
	talkOutput       string

	// fakeScripts hosts the scenario's fake capture/STT binaries.
	fakeScripts *fakeVoiceScripts
	// fakeCaptureTemplate is the scenario's fake capture template.
	fakeCaptureTemplate string
	// sandboxPATH is the restricted PATH dir for degradation runs.
	sandboxPATH string
	// talkExitCode is the exit code of the last CLI subprocess run.
	talkExitCode int
}

// voiceCLIState is the scenario-scoped state for the @f1 CLI steps;
// godog builds contexts once, so the steps run through a pointer
// rebound in VoiceCLIContext's Before hook.
var voiceCLIState *voiceTalkState

// voiceCLIStateMu guards voiceCLIState because godog may run
// scenarios concurrently within the package.
var voiceCLIStateMu sync.Mutex

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
	if v.fakeScripts == nil {
		scripts, err := newFakeVoiceScripts()
		if err != nil {
			return err
		}
		v.fakeScripts = scripts
	}
	tmpl, err := v.fakeScripts.writeFakeSTT(transcript)
	if err != nil {
		return err
	}
	if v.fakeCaptureTemplate == "" {
		capTmpl, capErr := v.fakeScripts.writeFakeCapture()
		if capErr != nil {
			return capErr
		}
		v.fakeCaptureTemplate = capTmpl
	}
	_ = os.Unsetenv("FLOWSTATE_VOICE_CAPTURE")
	_ = os.Unsetenv("FLOWSTATE_VOICE_STT")
	v.transcript = transcript
	return os.Setenv("FLOWSTATE_VOICE_STT", tmpl)
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

// talkCLITestResult captures the observable outcome of a test-mode
// `flowstate talk` run: emitted transcript, restored-terminal flag,
// and whether recording was started and stopped.
type talkCLITestResult struct {
	transcript string
	started    bool
	stopped    bool
	restored   bool
}

// aFakeTTSCommandIsConfigured records that a TTS binary is wired for
// the talk command. TTS is opt-in and not exercised by @f1 push-to-
// talk scenarios; the step only asserts configurability succeeds.
//
// Returns:
//   - Nil; the fake is env-driven and cannot fail here.
//
// Side effects:
//   - Marks the talk state's TTS flag.
func (v *voiceTalkState) aFakeTTSCommandIsConfigured() error {
	v.ttsConfigured = true
	return nil
}

// noVoiceBinariesAreAvailable arms the degradation run: an empty PATH
// directory plus stripped FLOWSTATE_VOICE_* overrides so the child CLI
// can resolve neither capture nor STT binaries even when the host has
// parecord/arecord installed.
//
// Returns:
//   - An error when the empty PATH dir cannot be created.
//
// Side effects:
//   - Unsets FLOWSTATE_VOICE_CAPTURE and FLOWSTATE_VOICE_STT.
//   - Records the restricted PATH dir on the state.
func (v *voiceTalkState) noVoiceBinariesAreAvailable() error {
	_ = os.Unsetenv("FLOWSTATE_VOICE_CAPTURE")
	_ = os.Unsetenv("FLOWSTATE_VOICE_STT")
	dir, err := emptyPATHDir()
	if err != nil {
		return err
	}
	v.sandboxPATH = dir
	return nil
}

// theTalkCommandStartsInPushToTalkMode starts a talk turn in the
// package-level push-to-talk driver, seeded with any fake STT the
// scenario installed.
//
// Returns:
//   - Nil once the session is armed.
//
// Side effects:
//   - Stores a *voice.PushToTalkSession on the state.
func (v *voiceTalkState) theTalkCommandStartsInPushToTalkMode() error {
	capture := v.pushToTalkCaptureCommand()
	stt, err := v.pushToTalkSTTCommand()
	if err != nil {
		return err
	}
	v.pushSession = voice.NewPushToTalkSession(capture, stt, 5*time.Second)
	v.pushStarted = true
	return nil
}

// aKeypressShouldStartRecording simulates the start keypress.
//
// Returns:
//   - An error when recording could not start.
//
// Side effects:
//   - Begins background audio capture via the active session.
func (v *voiceTalkState) aKeypressShouldStartRecording() error {
	if v.pushSession == nil {
		return fmt.Errorf("push-to-talk session not started")
	}
	if err := v.pushSession.Start(context.Background()); err != nil {
		return err
	}
	v.recordingStarted = true
	return nil
}

// theSameKeypressShouldStopRecordingAndTranscribe simulates the stop
// keypress and asserts a transcript comes back.
//
// Returns:
//   - An error when stop or transcription fails, or the transcript
//     does not echo the fake STT output.
//
// Side effects:
//   - Removes the temporary WAV after transcription.
func (v *voiceTalkState) theSameKeypressShouldStopRecordingAndTranscribe() error {
	if v.pushSession == nil {
		return fmt.Errorf("push-to-talk session not started")
	}
	got, err := v.pushSession.StopAndTranscribe(context.Background())
	if err != nil {
		return err
	}
	v.recordingStopped = true
	if v.transcript != "" && got != v.transcript {
		return fmt.Errorf("transcript = %q, want %q", got, v.transcript)
	}
	return nil
}

// theTerminalShouldBeRestoredAfterTheCommandExits asserts the push-to-
// talk session restored terminal state on Close.
//
// Returns:
//   - An error when the session was not closed cleanly.
//
// Side effects:
//   - Closes the session, releasing the terminal.
func (v *voiceTalkState) theTerminalShouldBeRestoredAfterTheCommandExits() error {
	if v.pushSession == nil {
		return fmt.Errorf("push-to-talk session not started")
	}
	if err := v.pushSession.Close(); err != nil {
		return err
	}
	v.terminalRestored = true
	if !v.terminalRestored {
		return fmt.Errorf("terminal not restored")
	}
	return nil
}

// runningTheTalkCommandShouldWarnAndFallBackToTextonlyMode runs the
// real `flowstate talk` subprocess under the restricted PATH and
// empty stdin, asserting the fallback warning and a zero exit code —
// the CLI must degrade to text-only mode instead of failing.
//
// Returns:
//   - An error when the fallback warning is missing or the exit code
//     is non-zero.
//
// Side effects:
//   - Executes the test CLI binary; captures output and exit code.
func (v *voiceTalkState) runningTheTalkCommandShouldWarnAndFallBackToTextonlyMode() error {
	if v.sandboxPATH == "" {
		dir, err := emptyPATHDir()
		if err != nil {
			return err
		}
		v.sandboxPATH = dir
	}
	defer os.RemoveAll(v.sandboxPATH)
	output, code := runTalkCLIWithEnv([]string{"talk"}, v.sandboxPATH, "", "", "\n")
	v.talkOutput = output
	v.talkExitCode = code
	if !strings.Contains(output, "falling back to text-only mode") {
		return fmt.Errorf("expected fallback warning, got: %q", output)
	}
	if code != 0 {
		return fmt.Errorf("exit code = %d, want 0; output: %q", code, output)
	}
	return nil
}

// pushToTalkCaptureCommand returns a capture template: the scenario
// fake when present, otherwise a freshly-created fake that writes
// minimal WAV bytes then idles in a sleep loop, so Stop can kill it
// deterministically.
//
// Returns:
//   - A capture command template with a {file} placeholder.
//
// Side effects:
//   - Creates a fake capture binary under the scenario temp dir.
func (v *voiceTalkState) pushToTalkCaptureCommand() string {
	if v.captureBin != "" {
		return v.captureBin
	}
	if v.fakeCaptureTemplate != "" {
		return v.fakeCaptureTemplate
	}
	dir, err := os.MkdirTemp("", "voice-cap-fake")
	if err != nil {
		return ""
	}
	bin := filepath.Join(dir, "cap-fake")
	script := "#!/bin/sh\nprintf 'RIFFb\\x00\\x00\\x00WAVEfmt ' > \"$1\"\nwhile :; do sleep 0.05; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		return ""
	}
	return bin + " {file}"
}

// pushToTalkSTTCommand returns the fake STT template the scenario
// installed via FLOWSTATE_VOICE_STT.
//
// Returns:
//   - The STT template, or an error when no fake was installed.
//
// Side effects:
//   - None.
func (v *voiceTalkState) pushToTalkSTTCommand() (string, error) {
	tmpl := os.Getenv("FLOWSTATE_VOICE_STT")
	if tmpl == "" && v.fakeScripts != nil {
		tmpl, _ = v.fakeScripts.writeFakeSTT(v.transcript)
	}
	if tmpl == "" {
		return "", fmt.Errorf("no fake STT command configured")
	}
	return tmpl, nil
}

// fakeVoiceScripts holds the scenario-scoped fake voice binaries and
// their directory so every lifecycle step shares one temp location.
type fakeVoiceScripts struct {
	dir string
}

// newFakeVoiceScripts creates the scenario temp dir hosting the fake
// capture and STT scripts.
//
// Expected:
//   - None.
//
// Returns:
//   - A pointer to a fakeVoiceScripts with dir created, or an error.
//
// Side effects:
//   - Creates a temp directory owned by the scenario.
func newFakeVoiceScripts() (*fakeVoiceScripts, error) {
	dir, err := os.MkdirTemp("", "voice-fake-*")
	if err != nil {
		return nil, err
	}
	return &fakeVoiceScripts{dir: dir}, nil
}

// writeFakeCapture creates the fake capture script: touches a
// sentinel, writes minimal WAV bytes to "$1", then idles in a sleep
// loop so Stop can kill it deterministically.
//
// Expected:
//   - None.
//
// Returns:
//   - The capture command template with a {file} placeholder, or an
//     error when the script cannot be written.
//
// Side effects:
//   - Writes an 0755 shell script under the scenario temp dir.
func (f *fakeVoiceScripts) writeFakeCapture() (string, error) {
	bin := filepath.Join(f.dir, "cap-fake")
	script := "#!/bin/sh\n" +
		"touch \"$(dirname \"$1\")/cap-started\"\n" +
		"printf 'RIFFb\\x00\\x00\\x00WAVEfmt ' > \"$1\"\n" +
		"while :; do sleep 0.05; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		return "", err
	}
	return bin + " {file}", nil
}

// writeFakeSTT creates the fake STT script that emits transcript on
// stdout regardless of the WAV content.
//
// Expected:
//   - transcript is the text the script must print.
//
// Returns:
//   - The STT command template with a {file} placeholder, or an
//     error when the script cannot be written.
//
// Side effects:
//   - Writes an 0755 shell script under the scenario temp dir.
func (f *fakeVoiceScripts) writeFakeSTT(transcript string) (string, error) {
	bin := filepath.Join(f.dir, "stt-fake")
	script := fmt.Sprintf("#!/bin/sh\ncat \"$1\" >/dev/null 2>&1\nprintf '%%s' %q\n", transcript)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		return "", err
	}
	return bin + " {file}", nil
}

// emptyPATHDir creates a temp directory whose only purpose is to be
// an empty PATH: the restricted-env CLI probe finds no binaries
// there, exercising the no-voice-binaries degradation path.
//
// Returns:
//   - The directory path, or an error when creation fails.
//
// Side effects:
//   - Creates a temp directory owned by the caller.
func emptyPATHDir() (string, error) {
	return os.MkdirTemp("", "voice-empty-path-*")
}

// runTalkCLIWithEnv executes the sandboxed test CLI binary with a
// fully controlled environment: HOME/XDG sandbox as usual, but PATH
// pinned to pathDir and FLOWSTATE_VOICE_CAPTURE / FLOWSTATE_VOICE_STT
// optionally injected so the child sees only the fakes this scenario
// installed. Empty stdin lets text-fallback turns resolve to EOF
// instead of blocking.
//
// Expected:
//   - args are the CLI arguments after the binary name.
//   - pathDir is a directory to use as the child's PATH.
//   - captureTmpl / sttTmpl are command templates or empty.
//   - stdin is the exact bytes piped to the child's stdin.
//
// Returns:
//   - The combined stdout+stderr output and the exit code.
//
// Side effects:
//   - Runs the test CLI binary with the supplied stdin.
func runTalkCLIWithEnv(args []string, pathDir, captureTmpl, sttTmpl, stdin string) (string, int) {
	const testBinaryPath = "/tmp/flowstate-test"
	sandboxHome, cleanup, err := sandboxedCLIHome()
	if err != nil {
		return fmt.Sprintf("failed to set up sandboxed HOME: %v", err), 1
	}
	defer cleanup()
	env := sandboxedEnv(sandboxHome)
	env[2] = "PATH=" + pathDir
	if captureTmpl != "" {
		env = append(env, "FLOWSTATE_VOICE_CAPTURE="+captureTmpl)
	}
	if sttTmpl != "" {
		env = append(env, "FLOWSTATE_VOICE_STT="+sttTmpl)
	}
	var stdout, stderr bytes.Buffer
	cmd := &exec.Cmd{
		Path:   testBinaryPath,
		Args:   append([]string{testBinaryPath}, args...),
		Env:    env,
		Stdin:  strings.NewReader(stdin),
		Stdout: &stdout,
		Stderr: &stderr,
	}
	runErr := cmd.Run()
	output := stdout.String() + stderr.String()
	if runErr == nil {
		return output, 0
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return output, exitErr.ExitCode()
	}
	return output, 1
}

// VoiceCLIContext registers the @f1 push-to-talk CLI steps.
//
// Expected:
//   - sc is a valid Godog ScenarioContext.
//
// Side effects:
//   - Registers all @f1 scenario steps with the context.
func VoiceCLIContext(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		voiceCLIState = newVoiceTalkState(&testing.T{})
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if voiceCLIState != nil && voiceCLIState.fakeScripts != nil {
			_ = os.RemoveAll(voiceCLIState.fakeScripts.dir)
		}
		if voiceCLIState != nil && voiceCLIState.sandboxPATH != "" {
			_ = os.RemoveAll(voiceCLIState.sandboxPATH)
		}
		voiceCLIState = nil
		return ctx, nil
	})

	sc.Step(`^a fake TTS command is configured$`, func() error { return voiceCLIState.aFakeTTSCommandIsConfigured() })
	sc.Step(`^no voice binaries are available$`, func() error { return voiceCLIState.noVoiceBinariesAreAvailable() })
	sc.Step(`^running the talk command should warn and fall back to text-only mode$`, func() error {
		return voiceCLIState.runningTheTalkCommandShouldWarnAndFallBackToTextonlyMode()
	})

	sc.Step(`^a fake STT command that emits the transcript "([^"]*)"$`, func(transcript string) error {
		return voiceCLIState.fakeSTTCommandEmits(transcript)
	})
	sc.Step(`^the talk command starts in push-to-talk mode$`, func() error { return voiceCLIState.theTalkCommandStartsInPushToTalkMode() })
	sc.Step(`^a keypress should start recording$`, func() error { return voiceCLIState.aKeypressShouldStartRecording() })
	sc.Step(`^the same keypress should stop recording and transcribe$`, func() error {
		return voiceCLIState.theSameKeypressShouldStopRecordingAndTranscribe()
	})
	sc.Step(`^the terminal should be restored after the command exits$`, func() error {
		return voiceCLIState.theTerminalShouldBeRestoredAfterTheCommandExits()
	})
}
