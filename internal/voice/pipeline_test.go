package voice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	dispatchpkg "github.com/baphled/flowstate/internal/dispatch"
)

// fakeDispatchSpy records DispatchEphemeral calls for DispatchAudio tests.
type fakeDispatchSpy struct {
	requests []dispatchpkg.DispatchRequest
}

// DispatchEphemeral records the request and resolves immediately.
func (s *fakeDispatchSpy) DispatchEphemeral(_ context.Context, req dispatchpkg.DispatchRequest, _ interface{}) (dispatchpkg.EphemeralHandle, error) {
	s.requests = append(s.requests, req)
	done := make(chan error, 1)
	done <- nil
	return dispatchpkg.EphemeralHandle{Done: done}, nil
}

// writeFakeSTTBin writes an 0755 script emitting transcript from $1.
func writeFakeSTTBin(t *testing.T, transcript string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "stt-fake")
	script := "#!/bin/sh\ncat \"$1\" >/dev/null 2>&1\nprintf '%s' '" + transcript + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake stt: %v", err)
	}
	return bin + " {file}"
}

// TestDispatchAudioTranscribesAndDispatchs asserts the shared path
// transcribes caller audio and dispatches with mention scanning.
func TestDispatchAudioTranscribesAndDispatchs(t *testing.T) {
	spy := &fakeDispatchSpy{}
	p := NewPipeline("", writeFakeSTTBin(t, "hello @swarm from browser"))
	got, err := p.DispatchAudio(context.Background(), spy, []byte("RIFFb\x00\x00\x00WAVEfmt "))
	if err != nil {
		t.Fatalf("DispatchAudio: %v", err)
	}
	if got != "hello @swarm from browser" {
		t.Fatalf("transcript = %q", got)
	}
	if len(spy.requests) != 1 {
		t.Fatalf("dispatch calls = %d, want 1", len(spy.requests))
	}
	if !spy.requests[0].ScanMentions {
		t.Fatal("ScanMentions = false, want true")
	}
	if spy.requests[0].Content != got {
		t.Fatalf("dispatch content = %q, want %q", spy.requests[0].Content, got)
	}
}

// TestDispatchAudioRejectsEmptyAudio asserts the caller error for empty bytes.
func TestDispatchAudioRejectsEmptyAudio(t *testing.T) {
	p := NewPipeline("", "")
	if _, err := p.DispatchAudio(context.Background(), &fakeDispatchSpy{}, nil); err == nil {
		t.Fatal("expected error for empty audio")
	}
}

// TestDispatchAudioNilDispatcher asserts the nil-dispatcher guard.
func TestDispatchAudioNilDispatcher(t *testing.T) {
	p := NewPipeline("", "")
	if _, err := p.DispatchAudio(context.Background(), nil, []byte("wav")); err == nil {
		t.Fatal("expected error for nil dispatcher")
	}
}

// TestDispatchAudioSttUnavailable asserts the STT degradation error
// propagates from the shared path.
func TestDispatchAudioSttUnavailable(t *testing.T) {
	t.Setenv("FLOWSTATE_VOICE_STT", "definitely-not-an-stt-binary-xyz {file}")
	p := NewPipeline("", "")
	_, err := p.DispatchAudio(context.Background(), &fakeDispatchSpy{}, []byte("RIFF"))
	if !errors.Is(err, ErrSTTUnavailable) {
		t.Fatalf("err = %v, want ErrSTTUnavailable", err)
	}
}
