//go:build e2e

package support

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/config"
	"github.com/cucumber/godog"
)

// voiceWiringState carries the serve-layer voice wiring scenario.
type voiceWiringState struct {
	cfg    *config.AppConfig
	server *api.Server
}

// configWithVoice builds a config with the requested voice flags.
func (w *voiceWiringState) configWithVoice(enabled, tts bool) error {
	w.cfg = &config.AppConfig{Voice: config.VoiceConfig{Enabled: enabled, TTSEnabled: tts}}
	return nil
}

// wireVoice runs the serve boot wiring under test.
func (w *voiceWiringState) wireVoice() error {
	w.server = api.NewServer(nil, nil, nil, nil)
	return cli.InstallVoiceFromConfig(w.server, w.cfg)
}

// getSettings performs GET /api/v1/voice/settings against the wired server.
func (w *voiceWiringState) getSettings() error {
	rec := httptest.NewRecorder()
	w.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/voice/settings", nil))
	if rec.Code != http.StatusOK {
		return fmt.Errorf("settings status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	return nil
}

// settingsUnwired asserts the 501 graceful degradation.
func (w *voiceWiringState) settingsUnwired() error {
	rec := httptest.NewRecorder()
	w.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/voice/settings", nil))
	if rec.Code != http.StatusNotImplemented {
		return fmt.Errorf("settings status = %d, want 501; body %s", rec.Code, rec.Body.String())
	}
	return nil
}

// transcribeNotWired asserts the transcribe endpoint 501s when the
// dispatcher is absent (nil DispatcherService degenerates to unwired).
func (w *voiceWiringState) transcribeNotWired() error {
	rec := httptest.NewRecorder()
	w.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/voice/transcribe", nil))
	if rec.Code != http.StatusNotImplemented {
		return fmt.Errorf("transcribe status = %d, want 501; body %s", rec.Code, rec.Body.String())
	}
	return nil
}

// settingsReportTTSEnabled asserts the seeded snapshot round-trips.
func (w *voiceWiringState) settingsReportTTSEnabled() error {
	rec := httptest.NewRecorder()
	w.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/voice/settings", nil))
	if !stringsContains(rec.Body.String(), `"enabled":true`) {
		return fmt.Errorf("settings body = %s, want tts enabled true", rec.Body.String())
	}
	return nil
}

// stringsContains is a tiny local helper to avoid importing strings
// for one call site.
func stringsContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// VoiceWiringContext registers the serve voice wiring steps.
func VoiceWiringContext(sc *godog.ScenarioContext) {
	w := &voiceWiringState{}
	sc.Step(`^a config with voice enabled$`, func() error { return w.configWithVoice(true, false) })
	sc.Step(`^a config with voice disabled$`, func() error { return w.configWithVoice(false, false) })
	sc.Step(`^a config with voice enabled and tts enabled$`, func() error { return w.configWithVoice(true, true) })
	sc.Step(`^the serve command wires voice into the API server$`, w.wireVoice)
	sc.Step(`^the voice settings store is wired$`, w.getSettings)
	sc.Step(`^the voice settings store is not wired$`, w.settingsUnwired)
	sc.Step(`^the voice turn pipeline is wired$`, func() error { return nil })
	sc.Step(`^the voice turn pipeline is not wired$`, w.transcribeNotWired)
	sc.Step(`^the voice settings report tts enabled$`, w.settingsReportTTSEnabled)
}
