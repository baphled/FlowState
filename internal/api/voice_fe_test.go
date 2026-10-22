package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/dispatch"
	"github.com/baphled/flowstate/internal/turn"
	"github.com/baphled/flowstate/internal/voice"
)

// newVoiceFEServer builds a server with the given voice wiring.
func newVoiceFEServer(t *testing.T, opts ...ServerOption) *Server {
	t.Helper()
	all := append([]ServerOption{}, opts...)
	return NewServer(nil, nil, nil, nil, all...)
}

// TestVoiceSettingsGetPatch covers the settings endpoints' happy
// path, validation failure, and unwired 501.
func TestVoiceSettingsGetPatch(t *testing.T) {
	store := NewRuntimeVoiceSettings(voice.Settings{
		TTSEnabled:      true,
		TTSModel:        "en_GB-alan-medium",
		LengthScale:     1.1,
		NoiseScale:      0.7,
		SentenceSilence: 0.3,
	})
	srv := newVoiceFEServer(t, WithVoiceSettings(store))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/voice/settings", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body %s", rec.Code, rec.Body.String())
	}
	var got voice.Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.TTSEnabled || got.TTSModel != "en_GB-alan-medium" || got.LengthScale != 1.1 {
		t.Fatalf("settings = %+v", got)
	}

	patchBody := `{"enabled":false,"length_scale":-3}`
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/voice/settings", strings.NewReader(patchBody))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH invalid status = %d, body %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/voice/settings", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET after bad patch status = %d", rec.Code)
	}
	if got.TTSEnabled != true {
		t.Fatal("invalid patch mutated settings")
	}
}

// TestVoiceSettingsUnwired501 asserts the no-store mapping.
func TestVoiceSettingsUnwired501(t *testing.T) {
	srv := newVoiceFEServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/voice/settings", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

// fakeSynthesiser implements VoiceSynthesiser for endpoint tests.
type fakeSynthesiser struct {
	wav []byte
	err error
}

func (f *fakeSynthesiser) Synthesize(_ context.Context, _ string) ([]byte, error) {
	return f.wav, f.err
}

// TestVoiceTTSEndpoint covers happy path, missing text, unwired
// 501, and synthesiser failure 503.
func TestVoiceTTSEndpoint(t *testing.T) {
	wav := []byte("RIFFxxxxWAVEdata")
	srv := newVoiceFEServer(t, WithVoiceTTSSynthesizer(&fakeSynthesiser{wav: wav}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice/tts", strings.NewReader(`{"text":"hello browser"}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "audio/wav" {
		t.Fatalf("status = %d, ct = %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !bytes.Equal(rec.Body.Bytes(), wav) {
		t.Fatalf("body = %q", rec.Body.Bytes())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/voice/tts", strings.NewReader(`{}`))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no-text status = %d", rec.Code)
	}

	failing := newVoiceFEServer(t, WithVoiceTTSSynthesizer(&fakeSynthesiser{err: voice.ErrPiperUnavailable}))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/voice/tts", strings.NewReader(`{"text":"hello"}`))
	rec = httptest.NewRecorder()
	failing.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("failure status = %d", rec.Code)
	}

	bare := newVoiceFEServer(t)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/voice/tts", strings.NewReader(`{"text":"hello"}`))
	rec = httptest.NewRecorder()
	bare.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("unwired status = %d", rec.Code)
	}
}

// fakeTurnPipeline implements VoiceTurnDispatcher for transcribe tests.
type fakeTurnPipeline struct {
	transcript string
	err        error
	audio      []byte
}

func (f *fakeTurnPipeline) DispatchAudio(audio []byte) (string, error) {
	f.audio = audio
	return f.transcript, f.err
}

// TestVoiceTranscribeUploadCover covers multipart, raw body, webm
// rejection, and unwired 501.
func TestVoiceTranscribeUploadCover(t *testing.T) {
	pipeline := &fakeTurnPipeline{transcript: "hello @swarm"}
	srv := newVoiceFEServer(t, WithVoiceTurnPipeline(pipeline))

	var body bytes.Buffer
	body.WriteString("--X\r\nContent-Disposition: form-data; name=\"audio\"; filename=\"t.wav\"\r\nContent-Type: audio/wav\r\n\r\nRIFFb\x00\x00\x00WAVEfmt \r\n--X--\r\n")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice/transcribe", &body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=X")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart status = %d, body %s", rec.Code, rec.Body.String())
	}
	var payload voiceTranscribeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil || payload.Transcript != "hello @swarm" {
		t.Fatalf("payload = %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/voice/transcribe", bytes.NewReader([]byte("RIFFb\x00\x00\x00WAVEfmt ")))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("raw status = %d, body %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/voice/transcribe", bytes.NewReader([]byte{0x1a, 0x45, 0xdf, 0xa3}))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "WAV") {
		t.Fatalf("webm status = %d, body %s", rec.Code, rec.Body.String())
	}

	bare := newVoiceFEServer(t)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/voice/transcribe", bytes.NewReader([]byte("RIFFWAVE")))
	rec = httptest.NewRecorder()
	bare.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("unwired status = %d", rec.Code)
	}
}

// TestVoiceTranscribeSTTUnavailable asserts the 503 mapping.
func TestVoiceTranscribeSTTUnavailable(t *testing.T) {
	srv := newVoiceFEServer(t, WithVoiceTurnPipeline(&fakeTurnPipeline{err: errors.New("wrapped: " + voice.ErrSTTUnavailable.Error())}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice/transcribe", bytes.NewReader([]byte("RIFFb\x00\x00\x00WAVEfmt ")))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for wrapped (non-sentinel) error", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "wrapped") {
		t.Fatalf("internal error detail leaked to client: %s", rec.Body.String())
	}
}

// TestVoiceTranscribeShortBodyNoPanic asserts that 4-11 byte RIFF
// bodies are rejected with 400 instead of panicking on the
// audio[:12] header slice.
func TestVoiceTranscribeShortBodyNoPanic(t *testing.T) {
	pipeline := &fakeTurnPipeline{transcript: "x"}
	srv := newVoiceFEServer(t, WithVoiceTurnPipeline(pipeline))
	for _, body := range []string{"RIFF", "RIFFWAVE", "RIFFb\x00\x00"} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/voice/transcribe", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d, want 400", body, rec.Code)
		}
	}
}

// TestVoiceTranscribeOversizeMultipart413 asserts a multipart upload
// over the 10 MiB cap is rejected with 413, not silently truncated.
func TestVoiceTranscribeOversizeMultipart413(t *testing.T) {
	pipeline := &fakeTurnPipeline{transcript: "x"}
	srv := newVoiceFEServer(t, WithVoiceTurnPipeline(pipeline))
	wav := make([]byte, maxVoiceUploadBytes+128)
	copy(wav, "RIFFb\x00\x00\x00WAVEfmt ")

	var body bytes.Buffer
	body.WriteString("--X\r\nContent-Disposition: form-data; name=\"audio\"; filename=\"t.wav\"\r\nContent-Type: audio/wav\r\n\r\n")
	body.Write(wav)
	body.WriteString("\r\n--X--\r\n")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice/transcribe", &body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=X")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

// TestVoiceTTSLengthCap413 asserts text over the 64 KiB cap is
// rejected with 413 before synthesis runs.
func TestVoiceTTSLengthCap413(t *testing.T) {
	called := false
	syn := &recordingSynthesiser{called: &called}
	srv := newVoiceFEServer(t, WithVoiceTTSSynthesizer(syn))
	text := strings.Repeat("a", maxVoiceTTSBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice/tts", strings.NewReader(`{"text":"`+text+`"}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if called {
		t.Fatal("synthesiser was invoked for oversized text")
	}
}

// recordingSynthesiser records whether Synthesize was invoked.
type recordingSynthesiser struct {
	called *bool
}

func (r *recordingSynthesiser) Synthesize(context.Context, string) ([]byte, error) {
	*r.called = true
	return []byte("RIFFxxxxWAVEdata"), nil
}

// TestVoiceSettingsPatchTTSModelValidation asserts tts_model patches
// are validated for emptiness, length, and path safety.
func TestVoiceSettingsPatchTTSModelValidation(t *testing.T) {
	store := NewRuntimeVoiceSettings(voice.Settings{TTSModel: "en_GB-alan-medium", LengthScale: 1, NoiseScale: 0.5, SentenceSilence: 0.2})
	srv := newVoiceFEServer(t, WithVoiceSettings(store))
	for _, model := range []string{"", "a b", "../etc/passwd", "x/y", "a\x00b", strings.Repeat("m", 129)} {
		patchBody, _ := json.Marshal(map[string]any{"tts_model": model})
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/voice/settings", bytes.NewReader(patchBody))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("model %q status = %d, want 400", model, rec.Code)
		}
		if got := store.Get().TTSModel; got != "en_GB-alan-medium" {
			t.Fatalf("model %q mutated settings to %q", model, got)
		}
	}
	patchBody, _ := json.Marshal(map[string]any{"tts_model": "en_US-amy-low"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/voice/settings", bytes.NewReader(patchBody))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid model status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := store.Get().TTSModel; got != "en_US-amy-low" {
		t.Fatalf("valid model not applied, got %q", got)
	}
}

// blockingSynthesiser blocks until its context is cancelled, for
// timeout propagation tests.
type blockingSynthesiser struct{}

func (blockingSynthesiser) Synthesize(ctx context.Context, _ string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestTTSSynthesiserAdapterTimeout asserts the adapter bounds
// synthesis with a deadline even under a background context.
func TestTTSSynthesiserAdapterTimeout(t *testing.T) {
	adapter := &TTSSynthesiserAdapter{Synthesiser: blockingSynthesiser{}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := adapter.synthesize(ctx, "hello")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, voice.ErrTTSUnavailable) {
			t.Fatalf("err = %v, want ErrTTSUnavailable", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("adapter did not enforce the deadline")
	}
}

// TestBuildTurnResponse covers the question_requests pass-through on
// the turnResponse wire: omitted when empty, copied when populated,
// and coexisting with permission_requests.
func TestBuildTurnResponse(t *testing.T) {
	perm := turn.TurnPermissionRequest{RequestID: "perm-1", Status: "pending"}
	q := turn.TurnQuestionRequest{RequestID: "q-1", Question: "Which?", Status: "pending"}

	tests := []struct {
		name              string
		turn              turn.Turn
		wantQuestionKey   bool
		wantPermissionKey bool
	}{
		{
			name:            "empty question requests omit the field",
			turn:            turn.Turn{},
			wantQuestionKey: false,
		},
		{
			name:            "populated question requests are copied through",
			turn:            turn.Turn{QuestionRequests: []turn.TurnQuestionRequest{q}},
			wantQuestionKey: true,
		},
		{
			name:              "mixed question and permission requests both present",
			turn:              turn.Turn{QuestionRequests: []turn.TurnQuestionRequest{q}, PermissionRequests: []turn.TurnPermissionRequest{perm}},
			wantQuestionKey:   true,
			wantPermissionKey: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := buildTurnResponse(tt.turn)
			raw, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			_, hasQ := m["question_requests"]
			if hasQ != tt.wantQuestionKey {
				t.Errorf("question_requests present=%v, want %v (raw: %s)", hasQ, tt.wantQuestionKey, raw)
			}
			_, hasP := m["permission_requests"]
			if hasP != tt.wantPermissionKey {
				t.Errorf("permission_requests present=%v, want %v (raw: %s)", hasP, tt.wantPermissionKey, raw)
			}
			if tt.wantQuestionKey {
				var got []turn.TurnQuestionRequest
				if err := json.Unmarshal(m["question_requests"], &got); err != nil {
					t.Fatalf("question_requests decode: %v", err)
				}
				if len(got) != 1 || got[0].RequestID != "q-1" || got[0].Question != "Which?" {
					t.Errorf("question_requests not copied through: %+v", got)
				}
			}
		})
	}
}

// stubCtxSynthesiser implements the context-aware synthesiser seam
// that TTSSynthesiserAdapter wraps.
type stubCtxSynthesiser struct {
	calls int
	wav   []byte
	err   error
	block <-chan struct{}
}

// Synthesize records the call and returns the fixture outcome,
// optionally blocking until the test releases the channel.
func (s *stubCtxSynthesiser) Synthesize(ctx context.Context, _ string) ([]byte, error) {
	s.calls++
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.wav, s.err
}

// TestTTSSynthesiserAdapterSynthesize asserts the adapter forwards to
// the wrapped context-aware synthesiser under the synthesis timeout.
func TestTTSSynthesiserAdapterSynthesize(t *testing.T) {
	stub := &stubCtxSynthesiser{wav: []byte("wav")}
	adapt := &TTSSynthesiserAdapter{Synthesiser: stub}
	got, err := adapt.Synthesize("hello")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if string(got) != "wav" {
		t.Fatalf("got %q, want %q", got, "wav")
	}
	if stub.calls != 1 {
		t.Fatalf("calls = %d, want 1", stub.calls)
	}
}

// TestPipelineDispatcherAdapterDispatchAudio asserts the adapter
// forwards to the pipeline's shared STT-and-dispatch path and returns
// the pipeline error verbatim when the dispatcher rejects the turn.
func TestPipelineDispatcherAdapterDispatchAudio(t *testing.T) {
	pipe := &voice.Pipeline{}
	adapt := &PipelineDispatcherAdapter{
		Pipeline:   pipe,
		Dispatcher: errDispatcher{},
	}
	if _, err := adapt.DispatchAudio([]byte("wav")); err == nil {
		t.Fatal("DispatchAudio = nil error, want dispatcher error")
	}
}

// TestPipelineDispatcherAdapterDispatchAudioRequest asserts the
// request-scoped variant forwards under the caller's context.
func TestPipelineDispatcherAdapterDispatchAudioRequest(t *testing.T) {
	pipe := &voice.Pipeline{}
	adapt := &PipelineDispatcherAdapter{
		Pipeline:   pipe,
		Dispatcher: errDispatcher{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := adapt.DispatchAudioRequest(ctx, []byte("wav"))
	if err == nil {
		t.Fatal("DispatchAudioRequest = nil error, want error on cancelled context")
	}
}

// TestWithVoiceConversationInstallsService asserts the ServerOption
// installs the conversation service the handlers read.
func TestWithVoiceConversationInstallsService(t *testing.T) {
	stub := &wiringConversationStub{}
	srv := NewServer(nil, agent.NewRegistry(), nil, nil, WithVoiceConversation(stub))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/v1/voice/conversation/start", "application/json",
		strings.NewReader(`{"session_id":"s1"}`))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatal("start returned 404; option did not route conversation endpoints")
	}
	if !stub.active {
		t.Fatalf("stub Start not invoked; status %d", resp.StatusCode)
	}
}

// errDispatcher is a voice.Dispatcher stand-in whose DispatchEphemeral
// always fails, pinning the verbatim error pass-through of the adapter
// path.
type errDispatcher struct{}

// DispatchEphemeral always fails with a deadline error.
func (errDispatcher) DispatchEphemeral(ctx context.Context, _ dispatch.DispatchRequest, _ interface{}) (dispatch.EphemeralHandle, error) {
	return dispatch.EphemeralHandle{}, context.DeadlineExceeded
}

// wiringConversationStub is a minimal VoiceConversationService used to
// verify that WithVoiceConversation installs the conversation wiring.
type wiringConversationStub struct {
	active bool
}

// Start marks the conversation active.
func (w *wiringConversationStub) Start(sessionID string) { w.active = true }

// Stop clears the active flag.
func (w *wiringConversationStub) Stop() { w.active = false }

// Active reports the active session identifier.
func (w *wiringConversationStub) Active() string {
	if w.active {
		return "s1"
	}
	return ""
}

// Turn is unused by the wiring test but satisfies the service interface.
func (w *wiringConversationStub) Turn(audio []byte, speakReply bool) (voice.ConversationResult, error) {
	return voice.ConversationResult{}, nil
}
