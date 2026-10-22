package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentpkg "github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/voice"
)

// wiringConversationStub is a minimal VoiceConversationService used to
// assert the conversation endpoints are routed when wired.
type wiringConversationStub struct {
	active bool
}

// Start activates conversation mode for the session.
func (w *wiringConversationStub) Start(sessionID string) { w.active = true }

// Stop deactivates conversation mode.
func (w *wiringConversationStub) Stop() { w.active = false }

// Active reports the active session ID ("" when inactive).
func (w *wiringConversationStub) Active() string {
	if w.active {
		return "sess-1"
	}
	return ""
}

// Turn is unused by these wiring assertions.
func (w *wiringConversationStub) Turn(audio []byte, speakReply bool) (voice.ConversationResult, error) {
	return voice.ConversationResult{}, nil
}

// TestVoiceConversationEndpointsRegistered asserts that with a wired
// conversation service the start endpoint is routed (not 404).
func TestVoiceConversationEndpointsRegistered(t *testing.T) {
	stub := &wiringConversationStub{}
	srv := api.NewServer(nil, agentpkg.NewRegistry(), nil, nil, api.WithVoiceConversation(stub))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/voice/conversation/start", "application/json",
		strings.NewReader(`{"session_id":"s1"}`))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatal("start returned 404; conversation endpoints not routed")
	}
	if !stub.active {
		t.Fatalf("stub Start not invoked; status %d", resp.StatusCode)
	}
}

// TestVoiceConversationUnwiredReturns501 asserts the start endpoint
// fails closed with 501 when no conversation service is installed.
func TestVoiceConversationUnwiredReturns501(t *testing.T) {
	srv := api.NewServer(nil, agentpkg.NewRegistry(), nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/voice/conversation/start", "application/json",
		strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
}
