package app

// Item 1 — session recording isolation.
//
// The legacy defaultSessionRecordingDir hardcoded os.UserCacheDir,
// which meant `flowstate --sessions-dir /tmp/isolated-test/` still
// wrote recordings to ~/.cache/flowstate/session-recordings/. Tests
// that exercise recording paths polluted the real user cache dir,
// which is both a leak and a cross-test-isolation hazard.
//
// This file pins the new precedence chain:
//
//  1. explicit cfg.SessionRecordingDir when set
//  2. <sessionsDir>/recordings derived from --sessions-dir
//  3. UserCacheDir fallback when sessionsDir is empty
//
// The resolver is kept unexported; exposing it here via a _test.go
// file keeps the production surface minimal.

import (
	"path/filepath"
	"testing"

	"github.com/baphled/flowstate/internal/config"
)

// TestResolveSessionRecordingDir_ExplicitConfigWins covers precedence
// rule 1. Operators that point at a shared volume must not have the
// value silently overridden.
func TestResolveSessionRecordingDir_ExplicitConfigWins(t *testing.T) {
	cfg := &config.AppConfig{SessionRecordingDir: "/var/lib/flowstate/recordings"}

	got := resolveSessionRecordingDir(cfg, "/ignored/sessions")

	if got != "/var/lib/flowstate/recordings" {
		t.Fatalf("got %q; want /var/lib/flowstate/recordings", got)
	}
}

// TestResolveSessionRecordingDir_DerivesFromSessionsDir covers rule 2
// and is the core Item 1 regression: a `--sessions-dir /tmp/iso` run
// must write recordings under that directory, not ~/.cache.
func TestResolveSessionRecordingDir_DerivesFromSessionsDir(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	cfg := &config.AppConfig{}

	got := resolveSessionRecordingDir(cfg, sessionsDir)

	want := filepath.Join(sessionsDir, "recordings")
	if got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
}

// TestResolveSessionRecordingDir_FallsBackToUserCacheDir covers rule 3:
// when sessionsDir is empty (embedded or minimal test harness paths)
// the resolver returns the legacy cache-dir location so existing
// consumers continue to work.
func TestResolveSessionRecordingDir_FallsBackToUserCacheDir(t *testing.T) {
	cfg := &config.AppConfig{}

	got := resolveSessionRecordingDir(cfg, "")

	if got == "" {
		t.Fatal("fallback path must not be empty")
	}
	// Don't pin the exact path because os.UserCacheDir is env-dependent;
	// just make sure the "flowstate/session-recordings" suffix survives.
	if filepath.Base(got) != "session-recordings" {
		t.Fatalf("fallback %q must end in session-recordings", got)
	}
}

// TestResolveSessionRecordingDir_NilConfig guards the pathological
// "no config loaded" path used by a couple of embedded tests.
func TestResolveSessionRecordingDir_NilConfig(t *testing.T) {
	got := resolveSessionRecordingDir(nil, "/tmp/sessions")

	want := "/tmp/sessions/recordings"
	if got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
}
