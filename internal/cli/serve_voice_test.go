package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/config"
)

// voiceTestServer builds a bare API server the same way the serve
// boot path receives one, so the wiring under test sees the
// auto-constructed dispatcher absence (nil) just like minimal servers.
func voiceTestServer() *api.Server {
	return api.NewServer(nil, nil, nil, nil)
}

func TestInstallVoiceFromConfig_NoopWhenDisabled(t *testing.T) {
	cfg := &config.AppConfig{Voice: config.VoiceConfig{Enabled: false}}
	srv := voiceTestServer()
	if err := InstallVoiceFromConfig(srv, cfg); err != nil {
		t.Fatalf("expected clean no-op when voice disabled; got %v", err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/voice/settings", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("settings status = %d, want 501 when unwired", rec.Code)
	}
}

func TestInstallVoiceFromConfig_NoopWhenNilConfigOrServer(t *testing.T) {
	if err := InstallVoiceFromConfig(nil, &config.AppConfig{}); err != nil {
		t.Fatalf("nil server must no-op; got %v", err)
	}
	if err := InstallVoiceFromConfig(voiceTestServer(), nil); err != nil {
		t.Fatalf("nil config must no-op; got %v", err)
	}
}

func TestInstallVoiceFromConfig_WiresSettingsWhenEnabled(t *testing.T) {
	cfg := &config.AppConfig{Voice: config.VoiceConfig{
		Enabled:    true,
		TTSEnabled: true,
	}}
	srv := voiceTestServer()
	if err := InstallVoiceFromConfig(srv, cfg); err != nil {
		t.Fatalf("InstallVoiceFromConfig: %v", err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/voice/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("settings status = %d, want 200 when wired; body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("settings body must report tts enabled true; got %s", rec.Body.String())
	}
}

func TestSettingsFromVoiceConfig_SeedsTTSEnabled(t *testing.T) {
	got := settingsFromVoiceConfig(config.VoiceConfig{TTSEnabled: true})
	if !got.TTSEnabled {
		t.Fatal("settingsFromVoiceConfig must seed TTSEnabled from config")
	}
}
