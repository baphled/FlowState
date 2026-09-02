package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentpkg "github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/voice"
)

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
	adapt := &api.TTSSynthesiserAdapter{Synthesiser: stub}
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
	adapt := &api.PipelineDispatcherAdapter{
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
	adapt := &api.PipelineDispatcherAdapter{
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
		t.Fatal("start returned 404; option did not route conversation endpoints")
	}
	if !stub.active {
		t.Fatalf("stub Start not invoked; status %d", resp.StatusCode)
	}
}
