package cli

import (
	"context"
	"errors"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/config"
	dispatchpkg "github.com/baphled/flowstate/internal/dispatch"
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
	pipeline := voice.NewPipeline(cfg.Voice.CaptureCmd, cfg.Voice.STTCmd)
	apiServer.ApplyOption(api.WithVoiceTurnPipeline(&voicePipelineAdapter{
		pipeline:   pipeline,
		dispatcher: &voiceDispatchAdapter{svc: dispatcher},
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
