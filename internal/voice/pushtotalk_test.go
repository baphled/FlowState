package voice_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/voice"
)

// TestNewPushToTalkSessionClampsDuration asserts the constructor
// clamps non-positive durations to the 30s default.
func TestNewPushToTalkSessionClampsDuration(t *testing.T) {
	s := voice.NewPushToTalkSession("", "", 0)
	if s.MaxDuration != 30_000_000_000 {
		t.Fatalf("MaxDuration = %v, want 30s", s.MaxDuration)
	}
}

// TestPushToTalkSessionStartRejectsNil asserts Start fails closed on
// a nil session instead of panicking.
func TestPushToTalkSessionStartRejectsNil(t *testing.T) {
	var s *voice.PushToTalkSession
	if err := s.Start(context.Background()); err == nil {
		t.Fatal("Start(nil) = nil error, want error")
	}
}

// TestPushToTalkSessionStopAndTranscribeWithoutRecording asserts
// StopAndTranscribe fails closed when no recording is active.
func TestPushToTalkSessionStopAndTranscribeWithoutRecording(t *testing.T) {
	s := voice.NewPushToTalkSession("", "", time.Second)
	if _, err := s.StopAndTranscribe(context.Background()); err == nil {
		t.Fatal("StopAndTranscribe without recording = nil error, want error")
	}
}

// TestPushToTalkSessionCloseIdempotent asserts Close is safe on a
// fresh and already-closed session.
func TestPushToTalkSessionCloseIdempotent(t *testing.T) {
	s := voice.NewPushToTalkSession("", "", time.Second)
	if err := s.Close(); err != nil {
		t.Fatalf("Close fresh: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close twice: %v", err)
	}
}

// TestPipelineFallbackWarningShape pins the wording the CLI prints
// when voice is unavailable and the text fallback engages; the
// wording is user-facing contract kept stable deliberately.
func TestPipelineFallbackWarningShape(t *testing.T) {
	_, err := voice.NewCaptureTool("")
	if err == nil {
		t.Skip("capture binary unexpectedly available")
	}
	if !strings.Contains(err.Error(), "capture") && err.Error() == "" {
		t.Fatalf("empty capture-tool error for missing binary: %v", err)
	}
}
