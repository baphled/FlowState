package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/config"
	dispatchpkg "github.com/baphled/flowstate/internal/dispatch"
	turnpkg "github.com/baphled/flowstate/internal/turn"
	"github.com/baphled/flowstate/internal/voice"
)

// voiceDispatchAdapter narrows the API server's DispatcherService to
// the voice package's Dispatcher. voice.Dispatcher declares the
// consumer as interface{} so the voice package does not depend on the
// streaming package; the production DispatcherService takes a
// streaming.StreamConsumer. The adapter passes nil through — the
// ephemeral dispatch from a voice turn has no live consumer, matching
// the voice.Pipeline contract.
type voiceDispatchAdapter struct {
	svc api.DispatcherService
}

// DispatchEphemeral forwards to the underlying DispatcherService with
// a nil consumer.
//
// Expected:
//   - ctx is non-nil; req carries the transcript.
//
// Returns:
//   - The handle from the underlying service.
//
// Side effects:
//   - Spawns the dispatch goroutine inside the service.
func (a *voiceDispatchAdapter) DispatchEphemeral(ctx context.Context, req dispatchpkg.DispatchRequest, _ interface{}) (dispatchpkg.EphemeralHandle, error) {
	return a.svc.DispatchEphemeral(ctx, req, nil)
}

// voicePipelineAdapter adapts a *voice.Pipeline plus a dispatcher to
// the api.VoiceTurnDispatcher surface the transcribe endpoint calls.
// The API-side interface takes bare WAV bytes and no context, so the
// adapter supplies a background context per call; STT children are
// bounded by the pipeline's own timeouts.
type voicePipelineAdapter struct {
	pipeline   *voice.Pipeline
	dispatcher voice.Dispatcher
}

// DispatchAudio transcribes WAV bytes and dispatches the transcript
// as an ephemeral, mention-scanned turn.
//
// Expected:
//   - audio is non-empty WAV file bytes.
//
// Returns:
//   - The dispatched transcript.
//   - voice.ErrSTTUnavailable when no STT binary resolves.
//   - The dispatcher's error verbatim when dispatch fails.
//
// Side effects:
//   - Spills audio to a temporary WAV, spawns the STT child, and
//     removes the temporary file on every exit path.
func (a *voicePipelineAdapter) DispatchAudio(audio []byte) (string, error) {
	return a.pipeline.DispatchAudio(context.Background(), a.dispatcher, audio)
}

// settingsFromVoiceConfig seeds the runtime voice settings snapshot
// from the loaded VoiceConfig. Only the tunable TTS subset is
// runtime-mutable; capture/STT templates resolve per-call.
//
// Expected:
//   - cfg is the loaded VoiceConfig.
//
// Returns:
//   - A voice.Settings seeded from cfg.
//
// Side effects:
//   - None.
func settingsFromVoiceConfig(cfg config.VoiceConfig) voice.Settings {
	return voice.Settings{
		TTSEnabled: cfg.TTSEnabled,
	}
}

// InstallVoiceFromConfig wires the voice pipeline, runtime settings
// store, and TTS synthesiser onto the API server when the resolved
// voice.enabled is true. When disabled the call is a no-op and every
// voice endpoint keeps returning 501 voice_not_wired (graceful
// degradation). The TTS synthesiser is wired only when a TTS tool
// resolves; the transcribe pipeline and settings store are wired
// in every case inside the enabled branch because STT resolution
// happens per-call (a missing whisper surfaces as 503 per-request,
// not a boot failure).
//
// Expected:
//   - apiServer is non-nil with a wired DispatcherService (nil
//     dispatcher behaves as voice disabled — no ephemeral target to
//     hand transcripts to).
//   - cfg may be nil; nil cfg behaves as voice disabled.
//
// Returns:
//   - An error only when TTS construction fails for a reason other
//     than unavailability.
//
// Side effects:
//   - Installs WithVoice* options on the server when enabled.
func InstallVoiceFromConfig(apiServer *api.Server, cfg *config.AppConfig) error {
	if apiServer == nil || cfg == nil || !cfg.Voice.Enabled {
		return nil
	}
	apiServer.ApplyOption(api.WithVoiceSettings(
		api.NewRuntimeVoiceSettings(settingsFromVoiceConfig(cfg.Voice))))
	dispatcher := apiServer.DispatcherService()
	if dispatcher == nil {
		return nil
	}
	pipeline := voice.NewPipeline(cfg.Voice.STTCmd)
	apiServer.ApplyOption(api.WithVoiceTurnPipeline(&voicePipelineAdapter{
		pipeline:   pipeline,
		dispatcher: &voiceDispatchAdapter{svc: dispatcher},
	}))
	apiServer.ApplyOption(api.WithVoiceConversation(&voiceConversationInstaller{
		stt:          cfg.Voice.STTCmd,
		svc:          dispatcher,
		synth:        resolveServeSynthesiser(cfg),
		sessionAgent: apiServer.SessionManagerSnapshot(),
	}))
	if cfg.Voice.TTSEnabled {
		tts, err := voice.NewTTSTool(cfg.Voice.TTSCmd)
		if err != nil {
			if errors.Is(err, voice.ErrTTSUnavailable) {
				return nil
			}
			return err
		}
		apiServer.ApplyOption(api.WithVoiceTTSSynthesizer(tts))
	}
	return nil
}

// resolveServeSynthesiser builds the conversational TTS synthesiser
// from the loaded config, degrading to nil when TTS is disabled or
// unavailable.
//
// Expected:
//   - cfg is the loaded application config; a nil-safe read of the
//     voice section.
//
// Returns:
//   - A *voice.TTSTool when TTS is enabled and the binary resolves;
//     nil otherwise (a nil synthesiser skips spoken replies without
//     failing the turn).
//
// Side effects:
//   - None.
func resolveServeSynthesiser(cfg *config.AppConfig) *voice.TTSTool {
	if cfg == nil || !cfg.Voice.TTSEnabled {
		return nil
	}
	tts, err := voice.NewTTSTool(cfg.Voice.TTSCmd)
	if err != nil {
		return nil
	}
	return tts
}

// replyWaitTimeout bounds how long a served voice turn waits for the
// dispatched chat turn to complete before returning the transcript
// without a reply.
const replyWaitTimeout = 90 * time.Second

// voiceConversationInstaller adapts the API DispatcherService to the
// voice package's ConversationService, dispatching each transcript as
// a sessioned turn and awaiting the turn registry for the reply
// text.
type voiceConversationInstaller struct {
	stt   string
	svc   api.DispatcherService
	synth *voice.TTSTool
	// sessionAgent resolves a session's baseline agent id, matching
	// the /messages handler's CurrentAgentID override.
	sessionAgent func(sessionID string) string
	once         sync.Once
	inner        *voice.ConversationService
}

// Start activates conversation mode for the session.
//
// Expected:
//   - sessionID is non-empty.
//
// Side effects:
//   - Delegates to the lazily built conversation service.
func (v *voiceConversationInstaller) Start(sessionID string) {
	v.service().Start(sessionID)
}

// Stop deactivates conversation mode.
//
// Expected:
//   - None.
//
// Side effects:
//   - Delegates to the lazily built conversation service.
func (v *voiceConversationInstaller) Stop() {
	v.service().Stop()
}

// Active reports the active session ID.
//
// Returns:
//   - The bound session ID, or the empty string when inactive.
//
// Side effects:
//   - None.
func (v *voiceConversationInstaller) Active() string {
	return v.service().Active()
}

// Turn runs one served voice conversation turn: STT the upload,
// dispatch the transcript to the bound session's chat machinery, and
// await the turn registry for the reply.
//
// Expected:
//   - audio is non-empty WAV bytes.
//
// Returns:
//   - A voice.ConversationResult carrying the transcript and, when
//     the dispatched turn completes within replyWaitTimeout, the
//     reply text and synthesised audio when speakReply is set.
//   - The STT or dispatch error verbatim.
//
// Side effects:
//   - Builds the conversation service on first use; spawns the STT
//     child and one ephemeral dispatch per turn.
func (v *voiceConversationInstaller) Turn(audio []byte, speakReply bool) (voice.ConversationResult, error) {
	service := v.service()
	original := service.Dispatcher
	service.Dispatcher = v
	defer func() { service.Dispatcher = original }()
	return service.Turn(context.Background(), voice.ConversationTurn{
		Audio:      audio,
		SpeakReply: speakReply,
	})
}

// service lazily builds the wrapped conversation service.
//
// Returns:
//   - A *voice.ConversationService bound to this installer's
//     dispatcher and synthesiser.
//
// Side effects:
//   - Caches the service on first call.
func (v *voiceConversationInstaller) service() *voice.ConversationService {
	v.once.Do(func() {
		v.inner = voice.NewConversationService(v.stt, v, v.synth)
	})
	return v.inner
}

// ConversationReply dispatches the transcript to the bound session
// via the shared DispatcherService and awaits the turn registry for
// the assistant reply.
//
// Expected:
//   - sessionID is the conversation-mode session; transcript is the
//     STT output.
//
// Returns:
//   - The assistant reply text (empty when the turn does not
//     complete within replyWaitTimeout).
//   - The dispatch error verbatim on failure.
//
// Side effects:
//   - Spawns one ephemeral dispatch goroutine and polls the turn
//     registry until the turn reaches a terminal state.
func (v *voiceConversationInstaller) ConversationReply(ctx context.Context, sessionID, transcript string) (string, error) {
	registry := v.svc.TurnRegistry()
	if registry == nil {
		return "", errors.New("voice serve: turn registry unavailable")
	}
	agentID := v.agentForSession(sessionID)
	handle, err := v.svc.DispatchSessioned(ctx, dispatchpkg.DispatchRequest{
		SessionID:    sessionID,
		AgentID:      agentID,
		Content:      transcript,
		ScanMentions: true,
	}, nil)
	if err != nil {
		return "", err
	}
	turnID := handle.TurnID
	deadline := time.Now().Add(replyWaitTimeout)
	for {
		t, getErr := registry.Get(turnID)
		if getErr != nil {
			return "", getErr
		}
		if t.Status != turnpkg.StatusRunning {
			return replyText(t), nil
		}
		if time.Now().After(deadline) {
			return "", nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// replyText joins the assistant messages recorded on a turn.
//
// Expected:
//   - t is a terminal-state turn snapshot.
//
// Returns:
//   - The concatenated assistant reply text (empty when none).
//
// Side effects:
//   - None.
func replyText(t turnpkg.Turn) string {
	var reply strings.Builder
	for _, msg := range t.MessagesAdded {
		if msg.Role == "assistant" {
			reply.WriteString(msg.Content)
		}
	}
	return reply.String()
}

// agentForSession resolves the session's baseline agent id so the
// dispatched turn matches the /messages handler's resolution.
//
// Expected:
//   - sessionID is a known conversation-mode session.
//
// Returns:
//   - The session's agent id (empty when the session is unknown).
//
// Side effects:
//   - None.
func (v *voiceConversationInstaller) agentForSession(sessionID string) string {
	if v.sessionAgent == nil {
		return ""
	}
	return v.sessionAgent(sessionID)
}
