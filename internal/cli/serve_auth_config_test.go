package cli

// PR5 C10 — installAuthFromConfig + resolveCSRFKey + resolveAuthConfig
// spec. Internal test (same package) so we can reach the unexported
// resolution helpers without exposing them via export_test.go.
//
// Pins:
//   - resolveAuthConfig precedence: env > cfg > default. Empty env vars
//     are treated as unset (operator can't accidentally clobber config
//     with an empty deploy var).
//   - resolveCSRFKey fails CLOSED when neither env nor cfg supplies a
//     key — error message points at `flowstate auth csrf-key gen`.
//   - resolveCSRFKey precedence: env > cfg.
//   - resolveString precedence helper: env > cfg > fallback.
//   - installAuthFromConfig no-ops cleanly when Enabled=false in cfg.

import (
	"os"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/auth/identity"
	"github.com/baphled/flowstate/internal/config"
)

func TestResolveAuthConfig_DefaultsToConfigEnabled(t *testing.T) {
	// Env explicitly unset; cfg.Auth.Enabled=true (the PR5 default)
	// should flow through.
	t.Setenv("FLOWSTATE_AUTH_ENABLED", "")
	cfg := &config.AppConfig{Auth: config.DefaultAuthConfig()}
	got := resolveAuthConfig(cfg)
	if !got.Enabled {
		t.Errorf("default DefaultAuthConfig should resolve Enabled=true; got %v", got.Enabled)
	}
}

func TestResolveAuthConfig_EnvOverridesConfig(t *testing.T) {
	// cfg says enabled, env says false — env wins.
	t.Setenv("FLOWSTATE_AUTH_ENABLED", "false")
	cfg := &config.AppConfig{Auth: config.AuthConfig{Enabled: true}}
	got := resolveAuthConfig(cfg)
	if got.Enabled {
		t.Errorf("FLOWSTATE_AUTH_ENABLED=false must override cfg.Auth.Enabled=true; got %v", got.Enabled)
	}
}

func TestResolveAuthConfig_EmptyEnvPreservesConfig(t *testing.T) {
	// Empty env var (deploy artifact set to "" by mistake) must NOT
	// clobber cfg.Auth.Enabled.
	t.Setenv("FLOWSTATE_AUTH_ENABLED", "")
	cfg := &config.AppConfig{Auth: config.AuthConfig{Enabled: true}}
	got := resolveAuthConfig(cfg)
	if !got.Enabled {
		t.Errorf("empty FLOWSTATE_AUTH_ENABLED must preserve cfg.Auth.Enabled=true; got %v", got.Enabled)
	}
}

func TestResolveAuthConfig_NilCfgUsesDefaults(t *testing.T) {
	t.Setenv("FLOWSTATE_AUTH_ENABLED", "")
	got := resolveAuthConfig(nil)
	if !got.Enabled {
		t.Errorf("nil cfg must yield DefaultAuthConfig (Enabled=true); got %v", got.Enabled)
	}
}

func TestResolveString_EnvWinsOverConfig(t *testing.T) {
	t.Setenv("MY_TEST_VAR", "from-env")
	got := resolveString("MY_TEST_VAR", "from-cfg", "fallback")
	if got != "from-env" {
		t.Errorf("env should win; got %q", got)
	}
}

func TestResolveString_ConfigWinsOverFallback(t *testing.T) {
	t.Setenv("MY_TEST_VAR", "")
	got := resolveString("MY_TEST_VAR", "from-cfg", "fallback")
	if got != "from-cfg" {
		t.Errorf("cfg should win over fallback when env unset; got %q", got)
	}
}

func TestResolveString_FallbackOnAllEmpty(t *testing.T) {
	t.Setenv("MY_TEST_VAR", "")
	got := resolveString("MY_TEST_VAR", "", "fallback")
	if got != "fallback" {
		t.Errorf("fallback should fire on empty env+cfg; got %q", got)
	}
}

func TestResolveString_WhitespaceTreatedAsEmpty(t *testing.T) {
	t.Setenv("MY_TEST_VAR", "   ")
	got := resolveString("MY_TEST_VAR", "from-cfg", "fallback")
	if got != "from-cfg" {
		t.Errorf("whitespace env should be treated as unset; got %q", got)
	}
}

func TestResolveCSRFKey_FailsClosedOnEmpty(t *testing.T) {
	// PR5/C10: no ephemeral fallback. Both env and cfg empty → error.
	t.Setenv("FLOWSTATE_AUTH_CSRF_KEY", "")
	_, err := resolveCSRFKey("")
	if err == nil {
		t.Fatalf("expected error when both env and cfg are empty; got nil")
	}
	// Error message MUST point at the gen command so operators know
	// the recovery path.
	if !strings.Contains(err.Error(), "flowstate auth csrf-key gen") {
		t.Errorf("error message must mention `flowstate auth csrf-key gen`; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "auth.enabled: false") {
		t.Errorf("error message must mention the auth.enabled: false opt-out; got %q", err.Error())
	}
}

func TestResolveCSRFKey_EnvWinsOverConfig(t *testing.T) {
	t.Setenv("FLOWSTATE_AUTH_CSRF_KEY", "env-key-32-byte-padding-aaaaaaaa")
	key, err := resolveCSRFKey("cfg-key-32-byte-padding-bbbbbbbb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(key[:len("env-key-32-byte-padding-aaaaaaaa")]) != "env-key-32-byte-padding-aaaaaaaa" {
		t.Errorf("env key should win; got %q", string(key))
	}
}

func TestResolveCSRFKey_ConfigUsedWhenEnvEmpty(t *testing.T) {
	t.Setenv("FLOWSTATE_AUTH_CSRF_KEY", "")
	key, err := resolveCSRFKey("cfg-key-32-byte-padding-bbbbbbbb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(key[:len("cfg-key-32-byte-padding-bbbbbbbb")]) != "cfg-key-32-byte-padding-bbbbbbbb" {
		t.Errorf("cfg key should be used when env is empty; got %q", string(key))
	}
}

func TestResolveCSRFKey_AlwaysReturns32Bytes(t *testing.T) {
	// Even a short input pads out to 32 bytes (gorilla/csrf compatibility).
	t.Setenv("FLOWSTATE_AUTH_CSRF_KEY", "short")
	key, err := resolveCSRFKey("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(key) != 32 {
		t.Errorf("key length must be 32; got %d", len(key))
	}
}

func TestInstallAuthFromConfig_NoopWhenDisabled(t *testing.T) {
	// Clear all env so we don't leak parent test state.
	for _, v := range []string{
		"FLOWSTATE_AUTH_ENABLED", "FLOWSTATE_AUTH_MODE",
		"FLOWSTATE_AUTH_SECRET", "FLOWSTATE_AUTH_CSRF_KEY",
		"FLOWSTATE_AUTH_PRINCIPAL_ID", "FLOWSTATE_AUTH_DISPLAY_NAME",
		"FLOWSTATE_AUTH_ALLOWED_ORIGINS", "FLOWSTATE_AUTH_SECURE_COOKIES",
	} {
		t.Setenv(v, "")
	}

	// Build a real api.Server but with Enabled=false so the function
	// should no-op (no InstallAuth, no CSRF requirement).
	cfg := &config.AppConfig{Auth: config.AuthConfig{Enabled: false}}
	srv := api.NewServer(nil, nil, nil, nil)
	if err := installAuthFromConfig(srv, cfg); err != nil {
		t.Fatalf("installAuthFromConfig should no-op cleanly when disabled; got error: %v", err)
	}
}

func TestInstallAuthFromConfig_FailsClosedOnEnabledNoCSRFKey(t *testing.T) {
	// Clear all env so we don't leak parent test state.
	for _, v := range []string{
		"FLOWSTATE_AUTH_ENABLED", "FLOWSTATE_AUTH_CSRF_KEY",
	} {
		t.Setenv(v, "")
	}

	// Enabled=true, NO CSRF key configured → must return error.
	cfg := &config.AppConfig{
		Auth: config.AuthConfig{
			Enabled: true,
			Mode:    identity.ModeDeploymentLogin,
			// No CSRFKey.
		},
	}
	srv := api.NewServer(nil, nil, nil, nil)
	err := installAuthFromConfig(srv, cfg)
	if err == nil {
		t.Fatalf("expected error when Enabled=true with no CSRF key; got nil")
	}
	if !strings.Contains(err.Error(), "csrf") {
		t.Errorf("error must mention CSRF; got %q", err.Error())
	}
}

func TestInstallAuthFromConfig_HappyPath(t *testing.T) {
	t.Setenv("FLOWSTATE_AUTH_ENABLED", "")
	t.Setenv("FLOWSTATE_AUTH_CSRF_KEY", "")

	// Operator config: enabled + per-deployment-login + key.
	cfg := &config.AppConfig{
		Auth: config.AuthConfig{
			Enabled:       true,
			Mode:          identity.ModeDeploymentLogin,
			Secret:        "operator-secret",
			PrincipalID:   "operator-id",
			CSRFKey:       strings.Repeat("a", 32),
			SecureCookies: true,
		},
	}
	srv := api.NewServer(nil, nil, nil, nil)
	if err := installAuthFromConfig(srv, cfg); err != nil {
		t.Fatalf("happy-path installAuthFromConfig failed: %v", err)
	}
	// Drop reset-store side effect so the next test's nil-store
	// guard fires the right path.
	SetResetStore(nil)

	// We don't have a public hook on api.Server to ask "is auth
	// active?" without sending a request — the auth_wrap_test.go
	// spec already pins the wire shape. Here we just need to know
	// no error was returned, which is the regression we care about.
	_ = os.Getenv // unused-import suppressor
}

// QA WARN-5 fix (May 2026). gorilla/csrf has its own TrustedOrigins
// allowlist that runs in parallel to RequireOrigin. Without threading
// the resolved AllowedOrigins through CSRFConfig.TrustedOrigins, every
// cross-origin POST was rejected at the CSRF gate regardless of the
// perimeter allowlist. Pin the threading at the bundle-resolution
// seam (buildAuthBundle) so a future refactor can't silently drop the
// wiring.
func TestBuildAuthBundle_ThreadsTrustedOrigins(t *testing.T) {
	t.Setenv("FLOWSTATE_AUTH_ENABLED", "")
	t.Setenv("FLOWSTATE_AUTH_CSRF_KEY", "")
	t.Setenv("FLOWSTATE_AUTH_ALLOWED_ORIGINS", "")

	want := []string{
		"https://flowstate.example.com",
		"https://flowstate-staging.example.com",
	}
	cfg := &config.AppConfig{
		Auth: config.AuthConfig{
			Enabled:        true,
			Mode:           identity.ModeDeploymentLogin,
			Secret:         "operator-secret",
			PrincipalID:    "operator-id",
			CSRFKey:        strings.Repeat("a", 32),
			SecureCookies:  true,
			AllowedOrigins: want,
		},
	}

	bundle, _, err := buildAuthBundle(cfg)
	if err != nil {
		t.Fatalf("buildAuthBundle: %v", err)
	}

	// The CSRF allowlist must match the Origin allowlist byte-for-byte.
	if len(bundle.CSRF.TrustedOrigins) != len(want) {
		t.Fatalf("CSRF.TrustedOrigins length: got %d, want %d",
			len(bundle.CSRF.TrustedOrigins), len(want))
	}
	for i, got := range bundle.CSRF.TrustedOrigins {
		if got != want[i] {
			t.Errorf("CSRF.TrustedOrigins[%d]: got %q, want %q", i, got, want[i])
		}
	}

	// Origin allowlist must also match — the two are threaded from the
	// same resolution slice; a regression would surface as a divergence.
	if len(bundle.Origin.AllowedOrigins) != len(want) {
		t.Fatalf("Origin.AllowedOrigins length: got %d, want %d",
			len(bundle.Origin.AllowedOrigins), len(want))
	}
}

// Sibling pin: the env-var override path also threads through to
// TrustedOrigins. Without this, an operator who configures the
// allowlist via FLOWSTATE_AUTH_ALLOWED_ORIGINS would still see the
// CSRF gate reject cross-origin POSTs.
func TestBuildAuthBundle_EnvOverrideThreadsTrustedOrigins(t *testing.T) {
	t.Setenv("FLOWSTATE_AUTH_ENABLED", "")
	t.Setenv("FLOWSTATE_AUTH_CSRF_KEY", "")
	t.Setenv("FLOWSTATE_AUTH_ALLOWED_ORIGINS",
		"https://env.example.com,https://env-staging.example.com")

	cfg := &config.AppConfig{
		Auth: config.AuthConfig{
			Enabled:       true,
			Mode:          identity.ModeDeploymentLogin,
			Secret:        "operator-secret",
			PrincipalID:   "operator-id",
			CSRFKey:       strings.Repeat("a", 32),
			SecureCookies: true,
			// cfg.AllowedOrigins deliberately set to something the env
			// overrides — the env value must win for both Origin and CSRF.
			AllowedOrigins: []string{"https://from-cfg.example.com"},
		},
	}

	bundle, _, err := buildAuthBundle(cfg)
	if err != nil {
		t.Fatalf("buildAuthBundle: %v", err)
	}

	wantHosts := []string{
		"https://env.example.com",
		"https://env-staging.example.com",
	}
	if len(bundle.CSRF.TrustedOrigins) != len(wantHosts) {
		t.Fatalf("env-override CSRF.TrustedOrigins length: got %v, want %v",
			bundle.CSRF.TrustedOrigins, wantHosts)
	}
	for i, got := range bundle.CSRF.TrustedOrigins {
		if got != wantHosts[i] {
			t.Errorf("CSRF.TrustedOrigins[%d]: got %q, want %q", i, got, wantHosts[i])
		}
	}
}
