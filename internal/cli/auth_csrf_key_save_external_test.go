package cli_test

// Auth Track follow-up — `flowstate auth csrf-key gen` save-to-config
// behaviour spec.
//
// The original PR5 (commit 4933fdaa) shipped a print-only generator.
// Operators reported the lossy copy-paste UX as a bug:
//
//     > "When running `flowstate auth csrf-key gen` it should save our
//     > token to the config file."
//
// This spec pins the follow-up:
//   - Default invocation persists the key into config.yaml's
//     auth.csrf_key field AND prints to stdout.
//   - --print-only skips the persistence path (PR5 behaviour preserved
//     for scripted pipelines).
//   - FLOWSTATE_AUTH_CSRF_KEY env var set → command refuses with a
//     structured error (env > config precedence at runtime would
//     silently override the just-written key — fail closed).
//   - Config file absent + parent dir present → file created with just
//     auth.csrf_key set; existing-file content is preserved across the
//     write (surgical YAML node-level edit, not a struct round-trip).
//   - Config file absent + parent dir absent → clear error directing
//     the operator to mkdir or pass --config.
//   - atomicwrite usage — no .atomicwrite-* temp file leaks on success.
//
// External _test package + Ginkgo to match the auth_user_test.go seam
// convention (memory feedback_extend_existing_specs — adjacent files
// own the public-CLI surface tests, the internal auth_csrf_key_test.go
// stays focused on the print-only path so it has no filesystem side
// effects).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("flowstate auth csrf-key gen — save-to-config behaviour (Auth Track follow-up)", func() {
	var (
		testApp     *app.App
		tmpDir      string
		configPath  string
		originalXDG string
		originalEnv string
		envWasSet   bool
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "flowstate-auth-csrf-key-*")
		Expect(err).NotTo(HaveOccurred())

		originalXDG = os.Getenv("XDG_CONFIG_HOME")
		Expect(os.Setenv("XDG_CONFIG_HOME", tmpDir)).To(Succeed())

		// Resolve the canonical write path (defaults via config.Dir()).
		// Tests assert against this file directly.
		configPath = filepath.Join(tmpDir, "flowstate", "config.yaml")
		Expect(os.MkdirAll(filepath.Dir(configPath), 0o700)).To(Succeed())

		// Snapshot FLOWSTATE_AUTH_CSRF_KEY so the env-precedence cases
		// can mutate it without leaking across specs.
		originalEnv, envWasSet = os.LookupEnv("FLOWSTATE_AUTH_CSRF_KEY")
		Expect(os.Unsetenv("FLOWSTATE_AUTH_CSRF_KEY")).To(Succeed())

		// app.New requires a default provider key (mirrors auth_user_test.go).
		Expect(os.Setenv("OPENAI_API_KEY", "test-key-auth-csrf-key-suite")).To(Succeed())

		cfg := config.DefaultConfig()
		cfg.Providers.Default = "openai"
		cfg.DataDir = filepath.Join(tmpDir, "data")
		Expect(os.MkdirAll(cfg.DataDir, 0o700)).To(Succeed())

		testApp, err = app.New(cfg)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.Unsetenv("OPENAI_API_KEY")).To(Succeed())
		if envWasSet {
			Expect(os.Setenv("FLOWSTATE_AUTH_CSRF_KEY", originalEnv)).To(Succeed())
		} else {
			Expect(os.Unsetenv("FLOWSTATE_AUTH_CSRF_KEY")).To(Succeed())
		}
		if originalXDG != "" {
			Expect(os.Setenv("XDG_CONFIG_HOME", originalXDG)).To(Succeed())
		} else {
			Expect(os.Unsetenv("XDG_CONFIG_HOME")).To(Succeed())
		}
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	runCmd := func(args ...string) (*bytes.Buffer, error) {
		root := cli.NewRootCmd(testApp)
		out := new(bytes.Buffer)
		root.SetOut(out)
		root.SetErr(out)
		root.SetArgs(args)
		err := root.Execute()
		return out, err
	}

	Describe("default invocation persists to config.yaml", func() {
		It("writes auth.csrf_key into config.yaml at the resolved path", func() {
			out, err := runCmd("auth", "csrf-key", "gen")
			Expect(err).NotTo(HaveOccurred(), "default gen must succeed when env is unset and parent dir exists")

			// Stdout includes the encoded key + a "Saved CSRF key to <path>" line.
			Expect(out.String()).To(ContainSubstring("Saved CSRF key to " + configPath))

			// LoadConfigFromPath reads the just-written value.
			cfg, loadErr := config.LoadConfigFromPath(configPath)
			Expect(loadErr).NotTo(HaveOccurred())
			Expect(cfg.Auth.CSRFKey).NotTo(BeEmpty())
			Expect(len(cfg.Auth.CSRFKey)).To(Equal(43),
				"a 32-byte base64url key should be 43 chars (RawURLEncoding, no padding)")

			// The encoded key is also printed to stdout (script-capture path).
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			Expect(lines).To(HaveLen(2),
				"stdout should be <key>\\n + Saved... — got %q", out.String())
			Expect(lines[0]).To(Equal(cfg.Auth.CSRFKey),
				"first line of stdout must equal the persisted key")
		})

		It("preserves other config fields across the write (surgical YAML edit)", func() {
			// Seed the config file with a non-trivial body that includes
			// keys outside AppConfig (`custom_marker:`) plus a populated
			// `auth:` block with a sibling field. A struct round-trip
			// (yaml.Marshal of AppConfig) would (a) drop custom_marker
			// and (b) overwrite the auth block's `mode` field with
			// applyDefaults output. The surgical Node-level edit must
			// preserve both.
			seed := "" +
				"log_level: info\n" +
				"custom_marker: preserve-me-across-csrf-gen\n" +
				"auth:\n" +
				"  enabled: true\n" +
				"  mode: shared-secret\n"
			Expect(os.WriteFile(configPath, []byte(seed), 0o600)).To(Succeed())

			_, err := runCmd("auth", "csrf-key", "gen")
			Expect(err).NotTo(HaveOccurred())

			raw, readErr := os.ReadFile(configPath)
			Expect(readErr).NotTo(HaveOccurred())
			body := string(raw)

			// Custom keys (unknown to AppConfig) MUST survive.
			Expect(body).To(ContainSubstring("custom_marker: preserve-me-across-csrf-gen"))
			// Sibling auth.mode + auth.enabled MUST survive.
			Expect(body).To(ContainSubstring("mode: shared-secret"))
			Expect(body).To(ContainSubstring("enabled: true"))
			// New auth.csrf_key MUST be present and non-empty.
			Expect(body).To(ContainSubstring("csrf_key:"))

			cfg, loadErr := config.LoadConfigFromPath(configPath)
			Expect(loadErr).NotTo(HaveOccurred())
			Expect(cfg.Auth.Mode).To(Equal("shared-secret"),
				"sibling auth.mode must not be clobbered by the surgical edit")
			Expect(cfg.Auth.Enabled).To(BeTrue())
			Expect(cfg.Auth.CSRFKey).NotTo(BeEmpty())
		})

		It("rewrites auth.csrf_key when it already exists (rotation)", func() {
			seed := "" +
				"auth:\n" +
				"  csrf_key: old-key-value-to-be-rotated\n"
			Expect(os.WriteFile(configPath, []byte(seed), 0o600)).To(Succeed())

			out, err := runCmd("auth", "csrf-key", "gen")
			Expect(err).NotTo(HaveOccurred())

			cfg, loadErr := config.LoadConfigFromPath(configPath)
			Expect(loadErr).NotTo(HaveOccurred())
			Expect(cfg.Auth.CSRFKey).NotTo(Equal("old-key-value-to-be-rotated"),
				"rotation must overwrite the existing key")
			Expect(len(cfg.Auth.CSRFKey)).To(Equal(43))

			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			Expect(lines[0]).To(Equal(cfg.Auth.CSRFKey))
		})

		It("creates config.yaml when absent (parent dir exists)", func() {
			_, statErr := os.Stat(configPath)
			Expect(os.IsNotExist(statErr)).To(BeTrue(), "precondition: config file absent")

			_, err := runCmd("auth", "csrf-key", "gen")
			Expect(err).NotTo(HaveOccurred())

			info, statErr := os.Stat(configPath)
			Expect(statErr).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)),
				"new config file must be 0600 (auth credential surface)")

			cfg, loadErr := config.LoadConfigFromPath(configPath)
			Expect(loadErr).NotTo(HaveOccurred())
			Expect(cfg.Auth.CSRFKey).NotTo(BeEmpty())
		})
	})

	Describe("--print-only flag", func() {
		It("skips the write — PR5 behaviour preserved", func() {
			_, err := runCmd("auth", "csrf-key", "gen", "--print-only")
			Expect(err).NotTo(HaveOccurred())

			_, statErr := os.Stat(configPath)
			Expect(os.IsNotExist(statErr)).To(BeTrue(),
				"--print-only must not create config.yaml")
		})

		It("emits the key on stdout with no Saved line", func() {
			out, err := runCmd("auth", "csrf-key", "gen", "--print-only")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).NotTo(ContainSubstring("Saved CSRF key"))

			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			Expect(lines).To(HaveLen(1))
			Expect(len(lines[0])).To(Equal(43))
		})
	})

	Describe("env-precedence refusal", func() {
		It("refuses to write when FLOWSTATE_AUTH_CSRF_KEY is set in the environment", func() {
			Expect(os.Setenv("FLOWSTATE_AUTH_CSRF_KEY", "env-set-key-that-would-override-config")).To(Succeed())

			out, err := runCmd("auth", "csrf-key", "gen")
			Expect(err).To(HaveOccurred(),
				"command must refuse: writing under env override is silently undone at runtime")
			Expect(err.Error()).To(ContainSubstring("FLOWSTATE_AUTH_CSRF_KEY is set"))
			Expect(err.Error()).To(ContainSubstring("--print-only"))

			// The key was still printed first (script-capture path is
			// useful even when refusing the write).
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			Expect(len(lines[0])).To(Equal(43))

			_, statErr := os.Stat(configPath)
			Expect(os.IsNotExist(statErr)).To(BeTrue(),
				"config file must not be written when refusing")
		})

		It("permits --print-only under env override (no precedence conflict)", func() {
			Expect(os.Setenv("FLOWSTATE_AUTH_CSRF_KEY", "env-set-key")).To(Succeed())

			_, err := runCmd("auth", "csrf-key", "gen", "--print-only")
			Expect(err).NotTo(HaveOccurred(),
				"--print-only doesn't touch config, so the env override is irrelevant")
		})
	})

	Describe("missing parent directory", func() {
		It("returns a clear error pointing at mkdir / --config", func() {
			// Replace XDG_CONFIG_HOME with a non-existent subpath. The
			// gen command must NOT silently mkdir — operators expect
			// the parent dir to exist already.
			nonExistent := filepath.Join(tmpDir, "does-not-exist")
			Expect(os.Setenv("XDG_CONFIG_HOME", nonExistent)).To(Succeed())

			_, err := runCmd("auth", "csrf-key", "gen")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("does not exist"))
			Expect(err.Error()).To(SatisfyAny(
				ContainSubstring("mkdir"),
				ContainSubstring("--config"),
			))
		})
	})

	Describe("atomic write discipline", func() {
		It("leaves no atomicwrite temp file behind on success", func() {
			_, err := runCmd("auth", "csrf-key", "gen")
			Expect(err).NotTo(HaveOccurred())

			entries, readErr := os.ReadDir(filepath.Dir(configPath))
			Expect(readErr).NotTo(HaveOccurred())
			for _, entry := range entries {
				Expect(entry.Name()).ToNot(ContainSubstring(".atomicwrite-"),
					"successful write must clean up its temp file")
			}
		})
	})

	Describe("root --config flag honoured", func() {
		It("writes to the operator-specified path instead of the default", func() {
			// Seed a minimal config at the custom path with default=openai
			// so initApp's reload (root.go:152) finds a viable provider —
			// otherwise it errors on the missing anthropic key before the
			// gen RunE ever fires.
			customPath := filepath.Join(tmpDir, "custom", "config.yaml")
			Expect(os.MkdirAll(filepath.Dir(customPath), 0o700)).To(Succeed())
			Expect(os.WriteFile(customPath, []byte("providers:\n  default: openai\n"), 0o600)).To(Succeed())

			// Root --config flag (defined at root.go:77) is the documented
			// override for the gen write target — passing it before the
			// subcommand path drives initApp's reload AND surfaces through
			// resolveConfigWritePath into the gen RunE.
			_, err := runCmd("--config", customPath, "auth", "csrf-key", "gen")
			Expect(err).NotTo(HaveOccurred())

			cfg, loadErr := config.LoadConfigFromPath(customPath)
			Expect(loadErr).NotTo(HaveOccurred())
			Expect(cfg.Auth.CSRFKey).NotTo(BeEmpty())

			// Default path must NOT have been touched.
			_, statErr := os.Stat(configPath)
			Expect(os.IsNotExist(statErr)).To(BeTrue(),
				"--config override must not also write to the default path")
		})
	})
})
