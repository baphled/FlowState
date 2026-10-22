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
	"path/filepath"
	"strings"

	agentpkg "github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/voice"
	"github.com/cucumber/godog"
)

// conversationState is the scenario-scoped state for the
// conversation-mode steps.
type conversationState struct {
	server   *api.Server
	service  *voice.ConversationService
	spy      *conversationDispatchSpy
	synthSpy *conversationSynthSpy
	recorder *httptest.ResponseRecorder
	active   bool
	session  string
	without  bool
}

// conversationDispatchSpy records chat sends.
type conversationDispatchSpy struct {
	// session is the last bound session.
	session string
	// messages holds the dispatched transcripts.
	messages []string
	// reply is the canned agent reply.
	reply string
}

// ConversationReply records the transcript and returns the reply.
func (c *conversationDispatchSpy) ConversationReply(_ context.Context, session, transcript string) (string, error) {
	c.session = session
	c.messages = append(c.messages, transcript)
	return c.reply, nil
}

// conversationSynthSpy records synthesised texts.
type conversationSynthSpy struct {
	// texts holds each synthesised reply.
	texts []string
}

// Synthesize records the text and returns fake WAV bytes.
func (c *conversationSynthSpy) Synthesize(_ context.Context, text string) ([]byte, error) {
	c.texts = append(c.texts, text)
	return []byte("RIFFfakeWAV"), nil
}

// conversation is the scenario-scoped state, reset in place per
// scenario so every bound step method observes the same receiver.
var conversation = &conversationState{}

// fakeSTTEmitsConversation installs a fake STT binary.
func (c *conversationState) fakeSTTEmits(text string) error {
	dir, err := os.MkdirTemp("", "voice-conv-stt-*")
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

// conversationServiceWired builds the server with the conversation
// service, dispatcher spy, and synthesiser spy wired.
func (c *conversationState) conversationServiceWired() error {
	if c.without {
		c.server = api.NewServer(nil, agentpkg.NewRegistry(), nil, nil)
		return nil
	}
	c.spy = &conversationDispatchSpy{}
	c.synthSpy = &conversationSynthSpy{}
	c.service = voice.NewConversationService("", c.spy, c.synthSpy)
	adapter := &conversationServiceAdapter{svc: c.service}
	c.server = api.NewServer(nil, agentpkg.NewRegistry(), nil, nil, api.WithVoiceConversation(adapter))
	return nil
}

// conversationServiceAdapter narrows the service to the API
// surface.
type conversationServiceAdapter struct {
	// svc is the wrapped conversation service.
	svc *voice.ConversationService
}

// Start activates conversation mode.
func (a *conversationServiceAdapter) Start(sessionID string) {
	a.svc.Start(sessionID)
}

// Stop deactivates conversation mode.
func (a *conversationServiceAdapter) Stop() {
	a.svc.Stop()
}

// Active reports the active session.
func (a *conversationServiceAdapter) Active() string {
	return a.svc.Active()
}

// Turn runs one voice conversation turn.
func (a *conversationServiceAdapter) Turn(audio []byte, speakReply bool) (voice.ConversationResult, error) {
	return a.svc.Turn(context.Background(), voice.ConversationTurn{Audio: audio, SpeakReply: speakReply})
}

// startConversation posts the start request.
func (c *conversationState) startConversation(session string) error {
	if err := c.ensureServer(); err != nil {
		return err
	}
	var body bytes.Buffer
	body.WriteString(`{"session_id":"` + session + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice/conversation/start", &body)
	rec := httptest.NewRecorder()
	c.server.Handler().ServeHTTP(rec, req)
	c.recorder = rec
	if rec.Code == http.StatusOK {
		c.active = true
		c.session = session
	}
	return nil
}

// conversationActiveFor activates mode directly.
func (c *conversationState) conversationActiveFor(session string) error {
	if err := c.ensureServer(); err != nil {
		return err
	}
	if c.service == nil {
		return fmt.Errorf("no conversation service")
	}
	c.service.Start(session)
	c.active = true
	c.session = session
	return nil
}

// conversationActive asserts the service reports active.
func (c *conversationState) conversationActive() error {
	if !c.active || c.service.Active() == "" {
		return fmt.Errorf("conversation mode not active")
	}
	return nil
}

// conversationReportSession asserts the active session binding.
func (c *conversationState) conversationReportSession(want string) error {
	if c.service.Active() != want {
		return fmt.Errorf("active session = %q, want %q", c.service.Active(), want)
	}
	return nil
}

// chatSessionReplies installs the canned agent reply.
func (c *conversationState) chatSessionReplies(reply string) error {
	c.spy.reply = strings.Trim(reply, `"`)
	return nil
}

// synthesiserSpyWired is a no-op: the spy is always wired.
func (c *conversationState) synthesiserSpyWired() error {
	return nil
}

// submitConversationTurn posts a WAV conversation turn.
func (c *conversationState) submitConversationTurn(speak bool) error {
	if err := c.ensureServer(); err != nil {
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
	if err := writer.WriteField("speak_reply", fmt.Sprintf("%t", speak)); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice/conversation/turn", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	c.server.Handler().ServeHTTP(rec, req)
	c.recorder = rec
	return nil
}

// responseStatusIs asserts the recorded status code.
func (c *conversationState) responseStatusIs(want int) error {
	if c.recorder == nil {
		return fmt.Errorf("no response recorded: previous step never hit the endpoint")
	}
	if c.recorder.Code != want {
		return fmt.Errorf("status = %d, want %d (body %s)", c.recorder.Code, want, c.recorder.Body.String())
	}
	return nil
}

// responseContainsTranscript asserts the body carries the text.
func (c *conversationState) responseContainsTranscript(text string) error {
	if !strings.Contains(c.recorder.Body.String(), strings.Trim(text, `"`)) {
		return fmt.Errorf("body %s missing %q", c.recorder.Body.String(), text)
	}
	return nil
}

// chatSessionReceives asserts the dispatched transcript.
func (c *conversationState) chatSessionReceives(text string) error {
	want := strings.Trim(text, `"`)
	if len(c.spy.messages) == 0 || c.spy.messages[0] != want {
		return fmt.Errorf("chat messages = %v, want [%s]", c.spy.messages, want)
	}
	return nil
}

// synthesiserReceives asserts the synthesised text.
func (c *conversationState) synthesiserReceives(text string) error {
	want := strings.Trim(text, `"`)
	if len(c.synthSpy.texts) == 0 || c.synthSpy.texts[0] != want {
		return fmt.Errorf("synth texts = %v, want [%s]", c.synthSpy.texts, want)
	}
	return nil
}

// synthesiserReceivesNothing asserts no synthesis happened.
func (c *conversationState) synthesiserReceivesNothing() error {
	if len(c.synthSpy.texts) != 0 {
		return fmt.Errorf("synth texts = %v, want none", c.synthSpy.texts)
	}
	return nil
}

// stopConversation posts the stop request.
func (c *conversationState) stopConversation() error {
	if err := c.ensureServer(); err != nil {
		return err
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice/conversation/stop", nil)
	rec := httptest.NewRecorder()
	c.server.Handler().ServeHTTP(rec, req)
	c.recorder = rec
	c.active = false
	c.session = ""
	return nil
}

// conversationNotActive asserts the service reports inactive.
func (c *conversationState) conversationNotActive() error {
	if c.service == nil || c.service.Active() != "" {
		return fmt.Errorf("conversation mode still active")
	}
	return nil
}

// withoutConversationService flags the server to skip wiring.
func (c *conversationState) withoutConversationService() error {
	c.without = true
	c.server = api.NewServer(nil, agentpkg.NewRegistry(), nil, nil)
	return nil
}

// ensureServer lazily builds the wired server.
func (c *conversationState) ensureServer() error {
	if c.server != nil {
		return nil
	}
	return c.conversationServiceWired()
}

// VoiceConversationContext registers the conversation-mode steps.
func VoiceConversationContext(ctx *godog.ScenarioContext) {
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		// Reset the shared state in place so every registered step
		// — method value or closure — observes the same receiver.
		*conversation = conversationState{}
		return c, nil
	})
	ctx.Step(`^a conversation service wired to a chat session$`, conversation.conversationServiceWired)
	ctx.Step(`^a fake conversation STT command that emits "(.*)"$`, conversation.fakeSTTEmits)
	ctx.Step(`^a synthesiser spy is wired$`, conversation.synthesiserSpyWired)
	ctx.Step(`^I start conversation mode for session "([^"]*)"$`, conversation.startConversation)
	ctx.Step(`^conversation mode is active for session "([^"]*)"$`, conversation.conversationActiveFor)
	ctx.Step(`^conversation mode is active$`, conversation.conversationActive)
	ctx.Step(`^conversation mode is not active$`, conversation.conversationNotActive)
	ctx.Step(`^the conversation report carries session "([^"]*)"$`, conversation.conversationReportSession)
	ctx.Step(`^the chat session replies "(.*)"$`, conversation.chatSessionReplies)
	ctx.Step(`^I submit conversation audio to /api/v1/voice/conversation/turn$`, func() error {
		return conversation.submitConversationTurn(true)
	})
	ctx.Step(`^I submit conversation audio to /api/v1/voice/conversation/turn with speak_reply false$`, func() error {
		return conversation.submitConversationTurn(false)
	})
	ctx.Step(`^the conversation response status is (\d+)$`, conversation.responseStatusIs)
	ctx.Step(`^the conversation response contains the transcript "(.*)"$`, conversation.responseContainsTranscript)
	ctx.Step(`^the chat session receives the message "(.*)"$`, conversation.chatSessionReceives)
	ctx.Step(`^the synthesiser receives the text "(.*)"$`, conversation.synthesiserReceives)
	ctx.Step(`^the synthesiser receives no text$`, conversation.synthesiserReceivesNothing)
	ctx.Step(`^I stop conversation mode$`, conversation.stopConversation)
	ctx.Step(`^a server without a conversation service wired$`, conversation.withoutConversationService)
}
