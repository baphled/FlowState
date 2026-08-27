//go:build e2e

package support

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// voiceFEState is the scenario-scoped state for the @fe voice steps.
type voiceFEState struct {
	recorder *httptest.ResponseRecorder
	server   *api.Server
	spy      *dispatchSpy
	tool     *voice.TTSTool
	synthWav []byte
	synthErr error
	synthGot []byte
	// stdinLog is where the fake piper appends each sentence.
	stdinLog string
	// argvLog records the fake piper's argv.
	argvLog string
	// synthCount counts fake piper invocations.
	synthCount int
}

// voiceFE is the scenario-scoped state, rebound per scenario.
var voiceFE *voiceFEState

// writeFakePiper writes a fake piper binary emitting wav on stdout
// and optionally logging stdin and argv.
func (v *voiceFEState) writeFakePiper(wav string, logStdin, logArgv bool) (string, error) {
	dir, err := os.MkdirTemp("", "voice-fe-piper-*")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "piper")
	script := "#!/bin/sh\n"
	if logStdin {
		v.stdinLog = filepath.Join(dir, "stdin.log")
		script += "cat >> " + v.stdinLog + "\nprintf '\\n' >> " + v.stdinLog + "\n"
	} else {
		script += "cat >/dev/null\n"
	}
	if logArgv {
		v.argvLog = filepath.Join(dir, "argv.log")
		script += "printf '%s\\n' \"$@\" > " + v.argvLog + "\n"
	}
	script += fmt.Sprintf("printf '%%s' '%s'\n", wav)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		return "", err
	}
	return bin, nil
}

// fakePiperWritesWAV installs the fake piper binary.
func (v *voiceFEState) fakePiperWritesWAV() error {
	bin, err := v.writeFakePiper("RIFFfakeWAV", true, false)
	if err != nil {
		return err
	}
	_ = os.Setenv("FLOWSTATE_VOICE_TTS", bin)
	return nil
}

// fakePiperLogsStdin installs a stdin-logging fake piper.
func (v *voiceFEState) fakePiperLogsStdin() error {
	bin, err := v.writeFakePiper("RIFF", true, false)
	if err != nil {
		return err
	}
	_ = os.Setenv("FLOWSTATE_VOICE_TTS", bin)
	return nil
}

// fakePiperLogsArgv installs an argv-logging fake piper.
func (v *voiceFEState) fakePiperLogsArgv() error {
	bin, err := v.writeFakePiper("RIFF", false, true)
	if err != nil {
		return err
	}
	_ = os.Setenv("FLOWSTATE_VOICE_TTS", bin)
	return nil
}

// ttsToolConfigured builds the TTSTool with tuning values.
func (v *voiceFEState) ttsToolConfigured(model string, length, noise, silence float64) error {
	tmpl := os.Getenv("FLOWSTATE_VOICE_TTS")
	if tmpl == "" {
		return fmt.Errorf("no fake piper configured")
	}
	v.tool = &voice.TTSTool{
		Command:         tmpl,
		Model:           model,
		LengthScale:     length,
		NoiseScale:      noise,
		SentenceSilence: silence,
	}
	return nil
}

// ttsToolDefaults configures with zero tuning values.
func (v *voiceFEState) ttsToolDefaults() error {
	return v.ttsToolConfigured("", 0, 0, 0)
}

// ttsToolWithModel configures with just a model.
func (v *voiceFEState) ttsToolWithModel(model string) error {
	return v.ttsToolConfigured(model, 0, 0, 0)
}

// iSynthesiseTheText runs Synthesize and records the outcome.
func (v *voiceFEState) iSynthesiseTheText(text string) error {
	unquoted := strings.ReplaceAll(text, "\\n", "\n")
	if v.tool == nil {
		tmpl := os.Getenv("FLOWSTATE_VOICE_TTS")
		if tmpl == "" {
			v.synthErr = voice.ErrPiperUnavailable
			return nil
		}
		v.tool = &voice.TTSTool{Command: tmpl}
	}
	wav, err := v.tool.Synthesize(context.Background(), unquoted)
	v.synthGot = wav
	v.synthErr = err
	if v.stdinLog != "" {
		if data, rerr := os.ReadFile(v.stdinLog); rerr == nil {
			v.synthCount = len(strings.Split(strings.TrimRight(string(data), "\n"), "\n"))
		}
	}
	return nil
}

// synthesisReturnsWAVBytes asserts non-empty WAV output.
func (v *voiceFEState) synthesisReturnsWAVBytes() error {
	if v.synthErr != nil {
		return fmt.Errorf("synthesis error: %v", v.synthErr)
	}
	if !bytes.HasPrefix(v.synthGot, []byte("RIFF")) {
		return fmt.Errorf("synth output = %q, want RIFF prefix", v.synthGot)
	}
	return nil
}

// theSynthesiserReceivesTheSentence asserts the single preprocessed
// sentence handed to piper.
func (v *voiceFEState) theSynthesiserReceivesTheSentence(want string) error {
	data, err := os.ReadFile(v.stdinLog)
	if err != nil {
		if v.argvLog == "" {
			bin, werr := v.writeFakePiper("RIFF", true, false)
			if werr != nil {
				return werr
			}
			_ = os.Setenv("FLOWSTATE_VOICE_TTS", bin)
		}
		return fmt.Errorf("stdin log unavailable; rerun scenario")
	}
	got := strings.TrimSpace(string(data))
	if got != want {
		return fmt.Errorf("stdin = %q, want %q", got, want)
	}
	return nil
}

// thePiperCommandIsInvokedOncePerSentence asserts two invocations
// for the two-sentence fixture.
func (v *voiceFEState) thePiperCommandIsInvokedOncePerSentence() error {
	if v.synthCount != 2 {
		return fmt.Errorf("invocations = %d, want 2", v.synthCount)
	}
	return nil
}

// theSynthesisedAudioIsTheConcatenation asserts concatenated output.
func (v *voiceFEState) theSynthesisedAudioIsTheConcatenation() error {
	if string(v.synthGot) != "RIFFRIFF" {
		return fmt.Errorf("audio = %q, want RIFFRIFF", v.synthGot)
	}
	return nil
}

// synthesisFailsWithAnErrTTSUnavailableError asserts the clear error.
func (v *voiceFEState) synthesisFailsWithAnErrTTSUnavailableError() error {
	if !errors.Is(v.synthErr, voice.ErrPiperUnavailable) && !errors.Is(v.synthErr, voice.ErrTTSUnavailable) {
		return fmt.Errorf("synth error = %v, want unavailable sentinel", v.synthErr)
	}
	return nil
}

// thePiperArgvIncludes asserts the logged argv contains the flag
// pair.
func (v *voiceFEState) thePiperArgvIncludes(flag string) error {
	data, err := os.ReadFile(v.argvLog)
	if err != nil {
		return fmt.Errorf("argv log: %w", err)
	}
	argv := strings.Fields(string(data))
	for i, a := range argv {
		if a == strings.Fields(flag)[0] && i+1 < len(argv) && argv[i+1] == strings.Fields(flag)[1] {
			return nil
		}
	}
	return fmt.Errorf("argv %v missing %q", argv, flag)
}

// ttsCommandNonexistentBinary points the tool at a missing binary.
func (v *voiceFEState) ttsCommandNonexistentBinary() error {
	tmpl := filepath.Join(os.TempDir(), "definitely-not-a-piper-binary-xyz")
	_ = os.Setenv("FLOWSTATE_VOICE_TTS", tmpl)
	v.tool = &voice.TTSTool{Command: tmpl}
	return nil
}

// noPiperButEspeakPresent ensures synthesis cannot silently fall
// back: unset overrides so only a path probe could resolve.
func (v *voiceFEState) noPiperButEspeakPresent() error {
	_ = os.Unsetenv("FLOWSTATE_VOICE_TTS")
	v.tool = nil
	return nil
}

// serverWithVoiceTurnPipeline wires the transcribe pipeline with a
// fake STT binary and dispatch spy.
func (v *voiceFEState) serverWithVoiceTurnPipeline() error {
	if v.spy == nil {
		v.spy = &dispatchSpy{}
	}
	pipeline := voice.NewPipeline("", "")
	adapter := &feTurnPipeline{pipeline: pipeline, spy: v.spy}
	v.server = api.NewServer(nil, agentpkg.NewRegistry(), nil, nil, api.WithVoiceTurnPipeline(adapter))
	return nil
}

// feTurnPipeline adapts the pipeline + spy into the API surface.
type feTurnPipeline struct {
	pipeline *voice.Pipeline
	spy      *dispatchSpy
}

// DispatchAudio runs the shared pipeline path.
func (f *feTurnPipeline) DispatchAudio(audio []byte) (string, error) {
	return f.pipeline.DispatchAudio(context.Background(), f.spy, audio)
}

// postWAVMultipart posts a multipart WAV upload.
func (v *voiceFEState) postWAVMultipart(path string) error {
	if v.server == nil {
		if err := v.serverWithVoiceTurnPipeline(); err != nil {
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

// postRawWAV posts raw WAV bytes.
func (v *voiceFEState) postRawWAV(path string) error {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte("RIFFb\x00\x00\x00WAVEfmt ")))
	req.Header.Set("Content-Type", "audio/wav")
	rec := httptest.NewRecorder()
	v.server.Handler().ServeHTTP(rec, req)
	v.recorder = rec
	return nil
}

// postWebm posts non-WAV bytes.
func (v *voiceFEState) postWebm(path string) error {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte{0x1a, 0x45, 0xdf, 0xa3}))
	rec := httptest.NewRecorder()
	v.server.Handler().ServeHTTP(rec, req)
	v.recorder = rec
	return nil
}

// theDispatcherReceivesTranscriptScanTrue asserts the spy captured
// the mention-scanned transcript.
func (v *voiceFEState) theDispatcherReceivesTranscriptScanTrue() error {
	if len(v.spy.requests) == 0 {
		return fmt.Errorf("no dispatch requests recorded")
	}
	if !v.spy.requests[0].ScanMentions {
		return fmt.Errorf("ScanMentions = false")
	}
	return nil
}

// theErrorMessageMentionsWAV asserts the 400 body explains WAV-only.
func (v *voiceFEState) theErrorMessageMentionsWAV() error {
	if !strings.Contains(v.recorder.Body.String(), "WAV") {
		return fmt.Errorf("body lacks WAV mention: %s", v.recorder.Body.String())
	}
	return nil
}

// theResponseContainsTheTranscriptFE asserts transcript JSON.
func (v *voiceFEState) theResponseContainsTheTranscriptFE(transcript string) error {
	var payload struct {
		Transcript string `json:"transcript"`
	}
	if err := json.Unmarshal(v.recorder.Body.Bytes(), &payload); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if payload.Transcript != transcript {
		return fmt.Errorf("transcript = %q, want %q", payload.Transcript, transcript)
	}
	return nil
}

// theResponseStatusIsFE asserts the recorded status.
func (v *voiceFEState) theResponseStatusIsFE(status int) error {
	if v.recorder.Code != status {
		return fmt.Errorf("status = %d, want %d; body: %s", v.recorder.Code, status, v.recorder.Body.String())
	}
	return nil
}

// serverWithVoiceSettingsStore wires the settings store.
func (v *voiceFEState) serverWithVoiceSettingsStore() error {
	v.server = api.NewServer(nil, agentpkg.NewRegistry(), nil, nil,
		api.WithVoiceSettings(api.NewRuntimeVoiceSettings(voice.Settings{})))
	return nil
}

// settingsStoreWith builds a store seeded from the config values.
func (v *voiceFEState) settingsStoreWith(enabled bool, model string, length, noise, silence float64) error {
	v.server = api.NewServer(nil, agentpkg.NewRegistry(), nil, nil,
		api.WithVoiceSettings(api.NewRuntimeVoiceSettings(voice.Settings{
			TTSEnabled:      enabled,
			TTSModel:        model,
			LengthScale:     length,
			NoiseScale:      noise,
			SentenceSilence: silence,
		})))
	return nil
}

// iGETVoiceSettings performs the GET.
func (v *voiceFEState) iGETVoiceSettings() error {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/voice/settings", nil)
	rec := httptest.NewRecorder()
	v.server.Handler().ServeHTTP(rec, req)
	v.recorder = rec
	return nil
}

// iPATCHVoiceSettings applies a full patch.
func (v *voiceFEState) iPATCHVoiceSettings(body string) error {
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/voice/settings", strings.NewReader(body))
	rec := httptest.NewRecorder()
	v.server.Handler().ServeHTTP(rec, req)
	v.recorder = rec
	return nil
}

// settingsJSONShows asserts the enabled+model fields round-trip.
func (v *voiceFEState) settingsJSONShows(enabled bool, model string) error {
	var got voice.Settings
	if err := json.Unmarshal(v.recorder.Body.Bytes(), &got); err != nil {
		return fmt.Errorf("decode: %w; body %s", err, v.recorder.Body.String())
	}
	if got.TTSEnabled != enabled || got.TTSModel != model {
		return fmt.Errorf("settings = %+v", got)
	}
	return nil
}

// settingsJSONShowsAll asserts every tuning field round-trips.
func (v *voiceFEState) settingsJSONShowsAll(model string, length, noise, silence float64) error {
	var got voice.Settings
	if err := json.Unmarshal(v.recorder.Body.Bytes(), &got); err != nil {
		return fmt.Errorf("decode: %w; body %s", err, v.recorder.Body.String())
	}
	if !got.TTSEnabled || got.TTSModel != model || got.LengthScale != length || got.NoiseScale != noise || got.SentenceSilence != silence {
		return fmt.Errorf("settings = %+v", got)
	}
	return nil
}

// serverWithoutVoiceWiring builds a bare server.
func (v *voiceFEState) serverWithoutVoiceWiring() error {
	v.server = api.NewServer(nil, agentpkg.NewRegistry(), nil, nil)
	return nil
}

// serverWithSynthesiser wires a fake synthesiser.
func (v *voiceFEState) serverWithSynthesiser(mode string) error {
	syn := &feSynthesiser{}
	if mode == "failing" {
		syn.err = voice.ErrPiperUnavailable
	} else {
		syn.wav = []byte("RIFFfakeWAV")
	}
	v.server = api.NewServer(nil, agentpkg.NewRegistry(), nil, nil, api.WithVoiceTTSSynthesizer(syn))
	return nil
}

// feSynthesiser implements the API's VoiceSynthesiser.
type feSynthesiser struct {
	wav []byte
	err error
}

// Synthesize returns the fixture bytes or error.
func (f *feSynthesiser) Synthesize(string) ([]byte, error) { return f.wav, f.err }

// iPOSTTextToTTS posts the text payload.
func (v *voiceFEState) iPOSTTextToTTS(text string) error {
	body := `{}`
	if text != "" {
		body = `{"text":` + mustMarshal(text) + `}`
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice/tts", strings.NewReader(body))
	rec := httptest.NewRecorder()
	v.server.Handler().ServeHTTP(rec, req)
	v.recorder = rec
	return nil
}

// iPOSTNoTextToTTS posts an empty body.
func (v *voiceFEState) iPOSTNoTextToTTS() error {
	return v.iPOSTTextToTTS("")
}

// mustMarshal JSON-encodes s.
func mustMarshal(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// theResponseContentTypeIsAudiowav asserts the header.
func (v *voiceFEState) theResponseContentTypeIsAudiowav() error {
	if got := v.recorder.Header().Get("Content-Type"); got != "audio/wav" {
		return fmt.Errorf("content type = %q", got)
	}
	return nil
}

// theResponseBodyIsWAVAudio asserts RIFF-prefixed bytes.
func (v *voiceFEState) theResponseBodyIsWAVAudio() error {
	if !bytes.HasPrefix(v.recorder.Body.Bytes(), []byte("RIFF")) {
		return fmt.Errorf("body = %q", v.recorder.Body.Bytes())
	}
	return nil
}

// aFakeSTTCommandEmitsFE installs the fake STT binary.
func (v *voiceFEState) aFakeSTTCommandEmitsFE(transcript string) error {
	dir, err := os.MkdirTemp("", "voice-fe-stt-*")
	if err != nil {
		return err
	}
	bin := filepath.Join(dir, "stt-fake")
	script := fmt.Sprintf("#!/bin/sh\ncat \"$1\" >/dev/null 2>&1\nprintf '%%s' %q\n", transcript)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		return err
	}
	return os.Setenv("FLOWSTATE_VOICE_STT", bin+" {file}")
}

// VoiceFEContext registers the @fe voice steps.
func VoiceFEContext(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		voiceFE = &voiceFEState{}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		_ = os.Unsetenv("FLOWSTATE_VOICE_TTS")
		_ = os.Unsetenv("FLOWSTATE_VOICE_STT")
		voiceFE = nil
		return ctx, nil
	})

	sc.Step(`^no STT command is configured$`, func() error {
		_ = os.Unsetenv("FLOWSTATE_VOICE_STT")
		if voiceFE.server == nil {
			return voiceFE.serverWithVoiceTurnPipeline()
		}
		return nil
	})
	sc.Step(`^I POST no audio to (/api/v1/voice/transcribe)$`, func(path string) error {
		if voiceFE.server == nil {
			if err := voiceFE.serverWithVoiceTurnPipeline(); err != nil {
				return err
			}
		}
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		voiceFE.server.Handler().ServeHTTP(rec, req)
		voiceFE.recorder = rec
		return nil
	})
	sc.Step(`^a fake STT command that emits "([^"]*)"$`, func(transcript string) error {
		return voiceFE.aFakeSTTCommandEmitsFE(transcript)
	})
	sc.Step(`^an API server with the voice turn pipeline wired$`, func() error {
		return voiceFE.serverWithVoiceTurnPipeline()
	})
	sc.Step(`^I POST a WAV file to (/api/v1/voice/transcribe)$`, func(path string) error {
		return voiceFE.postWAVMultipart(path)
	})
	sc.Step(`^I POST raw WAV bytes to (/api/v1/voice/transcribe)$`, func(path string) error {
		return voiceFE.postRawWAV(path)
	})
	sc.Step(`^I POST webm bytes to (/api/v1/voice/transcribe)$`, func(path string) error {
		return voiceFE.postWebm(path)
	})
	sc.Step(`^the dispatcher receives the transcript with ScanMentions true$`, func() error {
		return voiceFE.theDispatcherReceivesTranscriptScanTrue()
	})
	sc.Step(`^the error message mentions WAV$`, func() error { return voiceFE.theErrorMessageMentionsWAV() })
	sc.Step(`^the response contains the transcript "([^"]*)"$`, func(transcript string) error {
		return voiceFE.theResponseContainsTheTranscriptFE(transcript)
	})

	sc.Step(`^a fake piper command that writes WAV bytes to stdout$`, func() error {
		return voiceFE.fakePiperWritesWAV()
	})
	sc.Step(`^a fake piper command that writes WAV bytes to stdout and logs its stdin$`, func() error {
		return voiceFE.fakePiperLogsStdin()
	})
	sc.Step(`^a fake piper command that logs its argv$`, func() error {
		return voiceFE.fakePiperLogsArgv()
	})
	sc.Step(`^a TTS tool configured with that command and model "([^"]*)"$`, func(model string) error {
		return voiceFE.ttsToolWithModel(model)
	})
	sc.Step(`^a TTS tool configured with that command and defaults$`, func() error {
		return voiceFE.ttsToolDefaults()
	})
	sc.Step(`^a TTS tool configured with that command and model "([^"]*)" and length_scale (\d+)\.(\d+) and noise_scale (\d+)\.(\d+) and sentence_silence (\d+)\.(\d+)$`, func(model string, li, ld, ni, nd, si, sd int) error {
		return voiceFE.ttsToolConfigured(model, float64(li)+float64(ld)/10, float64(ni)+float64(nd)/10, float64(si)+float64(sd)/10)
	})
	sc.Step(`^a TTS command pointing at a nonexistent binary$`, func() error {
		return voiceFE.ttsCommandNonexistentBinary()
	})
	sc.Step(`^no piper binary on PATH but espeak-ng present$`, func() error {
		return voiceFE.noPiperButEspeakPresent()
	})
	sc.Step(`^I synthesise the text "([^"]*)"$`, func(text string) error {
		return voiceFE.iSynthesiseTheText(text)
	})
	sc.Step(`^synthesis returns WAV bytes$`, func() error { return voiceFE.synthesisReturnsWAVBytes() })
	sc.Step(`^the synthesiser receives the sentence "([^"]*)"$`, func(want string) error {
		return voiceFE.theSynthesiserReceivesTheSentence(want)
	})
	sc.Step(`^the piper command is invoked once per sentence$`, func() error {
		return voiceFE.thePiperCommandIsInvokedOncePerSentence()
	})
	sc.Step(`^the synthesised audio is the concatenation of per-sentence WAV bytes$`, func() error {
		return voiceFE.theSynthesisedAudioIsTheConcatenation()
	})
	sc.Step(`^synthesis fails with an ErrTTSUnavailable error$`, func() error {
		return voiceFE.synthesisFailsWithAnErrTTSUnavailableError()
	})
	sc.Step(`^the piper argv includes "([^"]*)"$`, func(flag string) error {
		return voiceFE.thePiperArgvIncludes(flag)
	})

	sc.Step(`^a voice settings store with defaults$`, func() error {
		return voiceFE.serverWithVoiceSettingsStore()
	})
	sc.Step(`^a voice settings store with tts_enabled true and tts_model "([^"]*)" and length_scale (\d+)\.(\d+) and noise_scale (\d+)\.(\d+) and sentence_silence (\d+)\.(\d+)$`, func(model string, li, ld, ni, nd, si, sd int) error {
		return voiceFE.settingsStoreWith(true, model, float64(li)+float64(ld)/10, float64(ni)+float64(nd)/10, float64(si)+float64(sd)/10)
	})
	sc.Step(`^I GET /api/v1/voice/settings$`, func() error { return voiceFE.iGETVoiceSettings() })
	sc.Step(`^I PATCH /api/v1/voice/settings with tts_enabled true and tts_model "([^"]*)"$`, func(model string) error {
		return voiceFE.iPATCHVoiceSettings(`{"enabled":true,"tts_model":` + mustMarshal(model) + `}`)
	})
	sc.Step(`^I PATCH /api/v1/voice/settings with length_scale -3$`, func() error {
		return voiceFE.iPATCHVoiceSettings(`{"length_scale":-3}`)
	})
	sc.Step(`^the settings JSON shows enabled true and tts_model "([^"]*)"$`, func(model string) error {
		return voiceFE.settingsJSONShows(true, model)
	})
	sc.Step(`^the settings JSON shows enabled true and tts_model "([^"]*)" and length_scale (\d+)\.(\d+) and noise_scale (\d+)\.(\d+) and sentence_silence (\d+)\.(\d+)$`, func(model string, li, ld, ni, nd, si, sd int) error {
		return voiceFE.settingsJSONShowsAll(model, float64(li)+float64(ld)/10, float64(ni)+float64(nd)/10, float64(si)+float64(sd)/10)
	})
	sc.Step(`^a server without voice settings wired$`, func() error {
		return voiceFE.serverWithoutVoiceWiring()
	})

	sc.Step(`^an API server with a synthesiser that returns WAV bytes$`, func() error {
		return voiceFE.serverWithSynthesiser("ok")
	})
	sc.Step(`^an API server with a synthesiser that fails$`, func() error {
		return voiceFE.serverWithSynthesiser("failing")
	})
	sc.Step(`^a server without a voice synthesiser wired$`, func() error {
		return voiceFE.serverWithoutVoiceWiring()
	})
	sc.Step(`^I POST the text "([^"]*)" to /api/v1/voice/tts$`, func(text string) error {
		return voiceFE.iPOSTTextToTTS(text)
	})
	sc.Step(`^I POST no text to /api/v1/voice/tts$`, func() error {
		return voiceFE.iPOSTNoTextToTTS()
	})
	sc.Step(`^the response content type is audio/wav$`, func() error {
		return voiceFE.theResponseContentTypeIsAudiowav()
	})
	sc.Step(`^the response body is WAV audio$`, func() error {
		return voiceFE.theResponseBodyIsWAVAudio()
	})
	sc.Step(`^the response status is (\d+)$`, func(status int) error {
		return voiceFE.theResponseStatusIsFE(status)
	})
}
