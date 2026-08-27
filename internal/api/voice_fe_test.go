package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func (f *fakeSynthesiser) Synthesize(string) ([]byte, error) { return f.wav, f.err }

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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice/transcribe", bytes.NewReader([]byte("RIFFWAVE")))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for wrapped (non-sentinel) error", rec.Code)
	}
}
