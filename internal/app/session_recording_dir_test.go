package app

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

// Item 1 — session recording isolation.
//
// The legacy defaultSessionRecordingDir hardcoded os.UserCacheDir, which
// meant `flowstate --sessions-dir /tmp/isolated-test/` still wrote
// recordings to ~/.cache/flowstate/session-recordings/. Tests that
// exercise recording paths polluted the real user cache dir, which is
// both a leak and a cross-test-isolation hazard.
//
// This file pins the new precedence chain:
//
//  1. explicit cfg.SessionRecordingDir when set
//  2. <sessionsDir>/recordings derived from --sessions-dir
//  3. UserCacheDir fallback when sessionsDir is empty
//
// The resolver is kept unexported; package-internal access via
// `package app` keeps the production surface minimal.
var _ = Describe("resolveSessionRecordingDir", func() {
	It("rule 1: explicit cfg.SessionRecordingDir wins over the derived path", func() {
		cfg := &config.AppConfig{SessionRecordingDir: "/var/lib/flowstate/recordings"}
		Expect(resolveSessionRecordingDir(cfg, "/ignored/sessions")).
			To(Equal("/var/lib/flowstate/recordings"))
	})

	It("rule 2: derives <sessionsDir>/recordings when no explicit path is set", func() {
		sessionsDir := filepath.Join(GinkgoT().TempDir(), "sessions")
		cfg := &config.AppConfig{}

		Expect(resolveSessionRecordingDir(cfg, sessionsDir)).
			To(Equal(filepath.Join(sessionsDir, "recordings")))
	})

	It("rule 3: falls back to a UserCacheDir-shaped path when sessionsDir is empty", func() {
		cfg := &config.AppConfig{}

		got := resolveSessionRecordingDir(cfg, "")
		Expect(got).NotTo(BeEmpty(), "fallback path must not be empty")
		// Don't pin the exact path because os.UserCacheDir is
		// env-dependent; just make sure the "session-recordings" suffix
		// survives.
		Expect(filepath.Base(got)).To(Equal("session-recordings"))
	})

	It("nil config still resolves the derived path from sessionsDir", func() {
		Expect(resolveSessionRecordingDir(nil, "/tmp/sessions")).
			To(Equal("/tmp/sessions/recordings"))
	})
})
