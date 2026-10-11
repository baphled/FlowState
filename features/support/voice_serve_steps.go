//go:build e2e

package support

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/config"
	dispatchpkg "github.com/baphled/flowstate/internal/dispatch"
	"github.com/baphled/flowstate/internal/streaming"
	turnpkg "github.com/baphled/flowstate/internal/turn"
	"github.com/baphled/flowstate/internal/voice"
	"github.com/cucumber/godog"
)

// voiceServeState carries the serve-side voice wiring scenarios: the
// conversation endpoints wired through the boot-time installer and
// the removal of the standalone talk CLI command.
type voiceServeState struct {
	cfg    *config.AppConfig
	server *api.Server
}

// voiceServe is the scenario-scoped state, reset per scenario.
var voiceServe = &voiceServeState{}

// serveConversation holds the serve-wired conversation service.
var serveConversation *voice.ConversationService

// serveDispatcher records dispatched transcripts and replies.
type serveDispatcher struct {
	messages []string
	reply    string
}

// ConversationReply records the transcript and returns the canned
// reply.
func (d *serveDispatcher) ConversationReply(_ context.Context, _, transcript string) (string, error) {
	d.messages = append(d.messages, transcript)
	return d.reply, nil
}

// serveSynth records synthesised texts.
type serveSynth struct {
	texts []string
}

// Synthesize records the text and returns fake WAV bytes.
func (s *serveSynth) Synthesize(_ context.Context, text string) ([]byte, error) {
	s.texts = append(s.texts, text)
	return []byte("RIFFfakeWAV"), nil
}

// serveDispatcherInstance is the scenario dispatcher spy.
var serveDispatcherInstance *serveDispatcher

// serveSynthInstance is the scenario synthesiser spy.
var serveSynthInstance *serveSynth

// wireServeVoiceWithDispatcher runs the serve boot wiring with voice
// enabled and a stub dispatcher so the installer path is exercised.
func (v *voiceServeState) wireServeVoiceWithDispatcher() error {
	v.cfg = &config.AppConfig{Voice: config.VoiceConfig{Enabled: true}}
	v.server = api.NewServer(nil, nil, nil, nil, api.WithDispatcher(&serveDispatcherStub{}))
	return cli.InstallVoiceFromConfig(v.server, v.cfg)
}

// serveDispatcherStub satisfies api.DispatcherService for the wiring
// scenario without a streamer chain.
type serveDispatcherStub struct{}

// DispatchEphemeral returns a completed handle.
func (s *serveDispatcherStub) DispatchEphemeral(context.Context, dispatchpkg.DispatchRequest, streaming.StreamConsumer) (dispatchpkg.EphemeralHandle, error) {
	return dispatchpkg.EphemeralHandle{}, nil
}

// DispatchSessioned returns an empty handle.
func (s *serveDispatcherStub) DispatchSessioned(context.Context, dispatchpkg.DispatchRequest, streaming.StreamConsumer) (dispatchpkg.SessionedHandle, error) {
	return dispatchpkg.SessionedHandle{}, nil
}

// CancelQueuedPrompt reports nothing queued.
func (s *serveDispatcherStub) CancelQueuedPrompt(string, string) bool { return false }

// CloseSessionQueue is a no-op.
func (s *serveDispatcherStub) CloseSessionQueue(string) {}

// TurnRegistry returns a fresh registry.
func (s *serveDispatcherStub) TurnRegistry() *turnpkg.Registry { return turnpkg.NewRegistry() }

// conversationEndpointsWired asserts the conversation status
// endpoint no longer returns 501 after the boot wiring.
func (v *voiceServeState) conversationEndpointsWired() error {
	rec := httptest.NewRecorder()
	v.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/voice/conversation/start", strings.NewReader(`{"session_id":"s"}`)))
	if rec.Code == http.StatusNotImplemented {
		return fmt.Errorf("conversation start 501, want wired; body %s", rec.Body.String())
	}
	return nil
}

// configVoiceDisabled records the disabled config.
func (v *voiceServeState) configVoiceDisabled() error {
	v.cfg = &config.AppConfig{Voice: config.VoiceConfig{Enabled: false}}
	return nil
}

// wireServeVoice runs the serve boot wiring against the recorded
// config.
func (v *voiceServeState) wireServeVoice() error {
	v.server = api.NewServer(nil, nil, nil, nil)
	return cli.InstallVoiceFromConfig(v.server, v.cfg)
}

// conversationEndpointsNotWired asserts the 501 graceful
// degradation when voice is disabled.
func (v *voiceServeState) conversationEndpointsNotWired() error {
	rec := httptest.NewRecorder()
	v.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/voice/conversation/start", strings.NewReader(`{"session_id":"s"}`)))
	if rec.Code != http.StatusNotImplemented {
		return fmt.Errorf("conversation start = %d, want 501; body %s", rec.Code, rec.Body.String())
	}
	return nil
}

// fakeServeSTT installs a fake STT binary emitting the canned text.
func (v *voiceServeState) fakeServeSTT(text string) error {
	dir, err := os.MkdirTemp("", "voice-serve-stt-*")
	if err != nil {
		return err
	}
	bin := filepath.Join(dir, "whisper-cli")
	script := "#!/bin/sh\nprintf '%s'\n"
	if err := os.WriteFile(bin, []byte(fmt.Sprintf(script, text)), 0o755); err != nil {
		return err
	}
	return os.Setenv("FLOWSTATE_VOICE_STT", bin)
}

// startServeConversation activates conversation mode on the wired
// server.
func (v *voiceServeState) startServeConversation(session string) error {
	if err := v.ensureConversation(); err != nil {
		return err
	}
	serveConversation.Start(session)
	return nil
}

// ensureConversation wires the conversation service onto the server
// once per scenario.
func (v *voiceServeState) ensureConversation() error {
	if serveConversation != nil {
		return nil
	}
	if v.server == nil {
		if err := v.wireServeVoiceWithDispatcher(); err != nil {
			return err
		}
	}
	serveDispatcherInstance = &serveDispatcher{}
	serveSynthInstance = &serveSynth{}
	serveConversation = voice.NewConversationService("", serveDispatcherInstance, serveSynthInstance)
	v.server.ApplyOption(api.WithVoiceConversation(&conversationServiceAdapter{svc: serveConversation}))
	return nil
}

// submitServeTurn posts one conversation turn and asserts the 200
// transcript round-trip.
func (v *voiceServeState) submitServeTurn() error {
	if err := v.ensureConversation(); err != nil {
		return err
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
	if err := writer.WriteField("speak_reply", "true"); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice/conversation/turn", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	v.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("turn status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello agent") {
		return fmt.Errorf("turn body %s missing transcript", rec.Body.String())
	}
	return nil
}

// chatReplies installs the canned agent reply on the dispatcher spy.
func (v *voiceServeState) chatReplies(reply string) error {
	if err := v.ensureConversation(); err != nil {
		return err
	}
	serveDispatcherInstance.reply = strings.Trim(reply, `"`)
	return nil
}

// chatReceives asserts the dispatched transcript.
func (v *voiceServeState) chatReceives(text string) error {
	want := strings.Trim(text, `"`)
	if len(serveDispatcherInstance.messages) == 0 || serveDispatcherInstance.messages[0] != want {
		return fmt.Errorf("chat messages = %v, want [%s]", serveDispatcherInstance.messages, want)
	}
	return nil
}

// synthReceives asserts the synthesised text.
func (v *voiceServeState) synthReceives(text string) error {
	want := strings.Trim(text, `"`)
	if len(serveSynthInstance.texts) == 0 || serveSynthInstance.texts[0] != want {
		return fmt.Errorf("synth texts = %v, want [%s]", serveSynthInstance.texts, want)
	}
	return nil
}

// talkCommandAbsent asserts the built CLI no longer registers talk.
func (v *voiceServeState) talkCommandAbsent() error {
	if err := EnsureFlowStateTestBinary(); err != nil {
		return err
	}
	out, err := exec.Command(FlowStateTestBinaryPath(), "talk").CombinedOutput()
	if err == nil {
		return fmt.Errorf("talk command unexpectedly succeeded: %s", out)
	}
	if !strings.Contains(string(out), "unknown command") {
		return fmt.Errorf("talk output = %s, want unknown command", out)
	}
	return nil
}

// VoiceServeContext registers the served-voice steps.
func VoiceServeContext(ctx *godog.ScenarioContext) {
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*voiceServe = voiceServeState{}
		serveConversation = nil
		serveDispatcherInstance = nil
		serveSynthInstance = nil
		return c, nil
	})
	ctx.Step(`^the serve command wires voice into the API server with a chat dispatcher$`, voiceServe.wireServeVoiceWithDispatcher)
	ctx.Step(`^the conversation mode endpoints are wired$`, voiceServe.conversationEndpointsWired)
	ctx.Step(`^a serve config with voice disabled$`, voiceServe.configVoiceDisabled)
	ctx.Step(`^the serve command wires serve voice from the config$`, voiceServe.wireServeVoice)
	ctx.Step(`^the conversation mode endpoints are not wired$`, voiceServe.conversationEndpointsNotWired)
	ctx.Step(`^a fake serve STT command that emits "(.*)"$`, voiceServe.fakeServeSTT)
	ctx.Step(`^serve conversation mode is active for session "([^"]*)"$`, voiceServe.startServeConversation)
	ctx.Step(`^the serve chat session replies "(.*)"$`, voiceServe.chatReplies)
	ctx.Step(`^I submit serve conversation audio to /api/v1/voice/conversation/turn$`, voiceServe.submitServeTurn)
	ctx.Step(`^the serve chat session receives the message "(.*)"$`, voiceServe.chatReceives)
	ctx.Step(`^the serve synthesiser receives the text "(.*)"$`, voiceServe.synthReceives)
	ctx.Step(`^the flowstate talk command does not exist$`, voiceServe.talkCommandAbsent)
}
