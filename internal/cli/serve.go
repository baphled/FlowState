package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/auth"
	"github.com/baphled/flowstate/internal/auth/identity"
	"github.com/baphled/flowstate/internal/auth/store"
	"github.com/baphled/flowstate/internal/config"
	quotastore "github.com/baphled/flowstate/internal/provider/quota/store"
)

// httpShutdowner is the narrow slice of *http.Server the serve
// shutdown path needs. Expressed as an interface so Item 6's
// regression test can drive the ordering without standing up a real
// HTTP listener.
type httpShutdowner interface {
	Shutdown(ctx context.Context) error
}

// engineShutdowner is the narrow slice of *engine.Engine the serve
// shutdown path needs. Keeps the seam private; the fake lives in
// export_test.go alongside the propagation-test helpers.
type engineShutdowner interface {
	Shutdown(ctx context.Context) error
}

// ServeOptions configures the HTTP API server.
type ServeOptions struct {
	Port int
	Host string
}

// newServeCmd creates the serve command for starting the HTTP API server.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with serve options.
//
// Side effects:
//   - Registers serve command flags.
func newServeCmd(getApp func() *app.App) *cobra.Command {
	opts := &ServeOptions{
		Port: 8080,
		Host: "localhost",
	}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the FlowState HTTP API server",
		Long: "Start the FlowState HTTP API server.\n\n" +
			"The server exposes a Prometheus /metrics endpoint. Compression " +
			"counters registered there (flowstate_compression_tokens_saved_total, " +
			"flowstate_compression_overhead_tokens_total, " +
			"flowstate_context_window_tokens) reflect THIS serve process's engine " +
			"only. Ephemeral `flowstate run` invocations are separate processes " +
			"with their own Prometheus registry and do NOT feed these counters. " +
			"Use `flowstate run --stats` for a per-turn summary on the CLI path.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd, getApp(), opts)
		},
	}

	flags := cmd.Flags()
	flags.IntVar(&opts.Port, "port", opts.Port, "Port to bind the HTTP server to")
	flags.StringVar(&opts.Host, "host", opts.Host, "Host interface to bind the HTTP server to")

	return cmd
}

// runServe starts the HTTP API server and handles graceful shutdown.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance with a configured API handler.
//   - opts is a non-nil ServeOptions with valid port and host.
//
// Returns:
//   - nil on successful shutdown, or an error if server startup or shutdown fails.
//
// Side effects:
//   - Starts HTTP server, listens for interrupt signals, performs graceful shutdown.
func runServe(cmd *cobra.Command, application *app.App, opts *ServeOptions) error {
	addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)

	// PR5/C10 — boot-time auth wiring. Composes config layer (cfg.Auth)
	// with env vars (FLOWSTATE_AUTH_*) per plan §"Bootstrap UX"
	// precedence (env → flag → config → default). PR4/C9 shipped the
	// env-only path; PR5 lifts the config layer + flips Enabled to
	// true by default + removes the ephemeral-random CSRF key fallback.
	if err := installAuthFromConfig(application.API, application.Config); err != nil {
		return fmt.Errorf("auth boot-time wiring: %w", err)
	}

	// PR1 of the Provider Quota and Spend Visibility plan (May 2026):
	// reject the `quota.store.backend = memory + deployment_topology
	// = multi-instance` pairing at boot per plan §"Boot validation"
	// lines 289-291. The only silent-double-count pairing — all four
	// other combinations boot quietly.
	if application.Config != nil {
		q := application.Config.Quota.Store
		if err := quotastore.ValidateDeploymentTopology(q.Backend, q.DeploymentTopology); err != nil {
			return fmt.Errorf("quota boot-time validation: %w", err)
		}
		// PR2: reject pricing.registry.enabled=true with empty URL
		// (memory feedback_default_urls_must_be_provisioned_or_disabled).
		// v1 ships no canonical FlowState URL — the operator opts in
		// AND provides their own.
		if err := config.ValidatePricingRegistry(application.Config.Quota.Pricing.Registry); err != nil {
			return fmt.Errorf("quota boot-time validation: %w", err)
		}
		// PR2: validate per-provider cap + period + thresholds. PR4
		// will enforce these against runtime spend; PR2 just rejects
		// malformed entries at boot so the misconfiguration surfaces
		// here rather than at first-spend-event time.
		for providerID, p := range application.Config.Quota.Providers {
			if err := config.ValidateProviderQuota(providerID, p); err != nil {
				return fmt.Errorf("quota boot-time validation: %w", err)
			}
		}
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           application.API.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errChan := make(chan error, 1)
	go func() {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Starting server on %s\n", addr)
		errChan <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Shutting down server...")
		// PR6 of the Provider Quota and Spend Visibility plan: drain
		// the persist-loop ticker BEFORE engine.Shutdown so the final
		// flush reads from the Tracker while it's still live. The
		// drain is bounded — on timeout the engine shutdown still
		// proceeds and the last few seconds of spend deltas are lost
		// (acceptable failure mode per plan).
		quotaCacheDrainCtx, quotaCacheCancel := context.WithTimeout(
			context.Background(), quotaCacheShutdownTimeout)
		if qErr := application.ShutdownQuotaCache(quotaCacheDrainCtx); qErr != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"warning: quota cache shutdown: %v\n", qErr)
		}
		quotaCacheCancel()
		var eng engineShutdowner
		if application.Engine != nil {
			eng = application.Engine
		}
		return performServeShutdown(server, eng, cmd.OutOrStdout(), cmd.ErrOrStderr())
	case err := <-errChan:
		return err
	}
}

// quotaCacheShutdownTimeout bounds the wait for the PR6 persist-loop
// goroutine to finish its final flush after SIGTERM. Tight (2s)
// because the underlying work is a single Snapshots() read + atomic
// write; longer would just delay the engine drain without any chance
// of completing.
const quotaCacheShutdownTimeout = 2 * time.Second

// performServeShutdown drains the HTTP server and then the engine,
// in that order. Extracted so the Item 6 regression test can drive
// the sequence with a fake httpShutdowner and engineShutdowner
// without spinning up a real listener, binding a port, or waiting on
// signals. The function must always invoke engineShutdowner.Shutdown
// when it is non-nil — that is the behaviour a future refactor of
// runServe is most likely to regress, and the resulting orphaned
// extractor goroutines leave `.tmp` files on disk with no log
// signal.
//
// Expected:
//   - server is non-nil; callers pass *http.Server from runServe or a
//     test double implementing httpShutdowner.
//   - eng may be nil (e.g. tests with no Engine assembled); the engine
//     drain is skipped in that case.
//   - out and errOut are the command's stdout/stderr sinks.
//
// Returns:
//   - nil on clean shutdown of both layers.
//   - The http.Server.Shutdown error when that fails (engine drain is
//     skipped, matching the previous behaviour).
//
// Side effects:
//   - Blocks the caller until both shutdowns complete or error.
//   - Emits a warning on errOut when the engine drain times out;
//     engine-drain failure is not promoted to a return error because
//     the HTTP server has already shut down and the operator cannot
//     usefully retry.
func performServeShutdown(server httpShutdowner, eng engineShutdowner, _ io.Writer, errOut io.Writer) error {
	if err := server.Shutdown(context.Background()); err != nil {
		return err
	}
	// H3: drain engine-owned background work before returning.
	// http.Server.Shutdown only waits for HTTP handlers; without this
	// call, session splitters' persist workers and L3 knowledge-
	// extraction goroutines get killed at process exit, orphaning
	// `.tmp` files on disk.
	if eng != nil {
		drainCtx, drainCancel := context.WithTimeout(context.Background(), engineShutdownTimeout)
		defer drainCancel()
		if err := eng.Shutdown(drainCtx); err != nil {
			_, _ = fmt.Fprintf(errOut,
				"warning: engine shutdown did not complete within %s: %v\n",
				engineShutdownTimeout, err,
			)
		}
	}
	return nil
}

// engineShutdownTimeout bounds the wait for engine-owned background
// goroutines (splitter persist workers + L3 extractions) to drain
// after http.Server.Shutdown returns. 30s matches the L3
// extractor's per-run LLM deadline so an extraction in flight at
// SIGTERM has headroom to finish; persist workers complete in
// sub-second for realistic channel depths.
const engineShutdownTimeout = 30 * time.Second

// installAuthFromConfig composes cfg.Auth with FLOWSTATE_AUTH_* env vars
// per plan §"Bootstrap UX" precedence (env → flag → config → default).
// When the resolved Enabled is true, constructs an api.AuthBundle and
// installs it on the server. Also wires the SessionStore used by
// `flowstate auth reset`'s store-cleanup step (memory
// feedback_close_latent_surfaces_too — every place that uses the store
// should see the same instance).
//
// PR5/C10 changes vs PR4/C9 (installAuthFromEnv):
//
//   - Reads cfg.Auth as the config layer; env vars override per field
//     (the precedence is field-level, not all-or-nothing).
//   - CSRF key resolution removes the ephemeral-random fallback. When
//     Enabled=true and neither env nor config supplies a key, the call
//     returns an error pointing the operator at
//     `flowstate auth csrf-key gen`. This is the load-bearing change —
//     it fails CLOSED rather than the prior fail-OPEN with a warn.
//   - DefaultConfig().Auth.Enabled = true (the PR5 flip). Operators who
//     don't configure auth still need credentials + a CSRF key; the
//     fail-closed CSRF key check is the safety net that catches a
//     misconfigured deployment.
//
// Field resolution order (per field, highest precedence first):
//
//   - Enabled:        env FLOWSTATE_AUTH_ENABLED (truthy/falsy) → cfg.Auth.Enabled
//   - Mode:           env FLOWSTATE_AUTH_MODE → cfg.Auth.Mode → "per-deployment-login"
//   - Secret:         env FLOWSTATE_AUTH_SECRET → cfg.Auth.Secret
//   - PrincipalID:    env FLOWSTATE_AUTH_PRINCIPAL_ID → cfg.Auth.PrincipalID
//   - DisplayName:    env FLOWSTATE_AUTH_DISPLAY_NAME → cfg.Auth.DisplayName
//   - AllowedOrigins: env FLOWSTATE_AUTH_ALLOWED_ORIGINS (CSV) →
//                     cfg.Auth.AllowedOrigins → ["localhost:*"]
//   - SecureCookies:  env FLOWSTATE_AUTH_SECURE_COOKIES → cfg.Auth.SecureCookies
//   - CSRFKey:        env FLOWSTATE_AUTH_CSRF_KEY → cfg.Auth.CSRFKey →
//                     FAIL (no ephemeral fallback per PR5/C10)
//
// Expected:
//   - apiServer is non-nil.
//   - cfg may be nil; nil cfg behaves as DefaultConfig() for auth purposes.
//
// Returns:
//   - nil when the resolved Enabled is false (no-op).
//   - Error on misconfig (unknown mode, missing CSRF key with auth.enabled=true,
//     unparseable users.json for multi-user).
//
// Side effects:
//   - Calls apiServer.InstallAuth(...) which replaces the route map.
//   - Calls SetResetStore(store) so `flowstate auth reset` operates on
//     the same store the live server uses.
//   - May log slog.Warn for first-boot misconfigs (multi-user with no
//     users.json — plan §"Bootstrap UX" multi-user lines 707-709).
func installAuthFromConfig(apiServer *api.Server, cfg *config.AppConfig) error {
	bundle, memStore, err := buildAuthBundle(cfg)
	if err != nil {
		return err
	}
	if memStore == nil {
		// Disabled — no bundle to install, no reset-store to wire.
		return nil
	}

	apiServer.InstallAuth(bundle)
	SetResetStore(memStore)

	slog.Info("auth boot-time wiring active",
		"mode", bundle.Auth.Mode,
		"allowed_origins", bundle.Origin.AllowedOrigins,
		"secure_cookies", bundle.CSRF.SecureCookies,
		"phase", "PR5/C10",
	)
	return nil
}

// buildAuthBundle resolves the auth configuration through the env-/cfg-/
// default precedence and returns the AuthBundle ready to install on the
// API server. Extracted from installAuthFromConfig so the resolved
// bundle is testable in isolation (QA WARN-5 spec needs to assert
// CSRF.TrustedOrigins is threaded from AllowedOrigins).
//
// Expected:
//   - cfg may be nil; nil cfg behaves as DefaultConfig() for auth purposes.
//
// Returns:
//   - When the resolved Enabled is false: zero AuthBundle, nil store, nil
//     error. The caller skips InstallAuth.
//   - On success: AuthBundle with every field resolved, the MemoryStore the
//     SessionManager was constructed against (for SetResetStore), and
//     nil error.
//   - On misconfig: zero AuthBundle, nil store, error.
//
// Side effects:
//   - Reads FLOWSTATE_AUTH_* env vars.
//   - Reads users.json from disk in multi-user mode.
//   - Emits slog.Info when auth is disabled (the caller suppresses the
//     active-wiring slog for the disabled branch).
func buildAuthBundle(cfg *config.AppConfig) (api.AuthBundle, *store.MemoryStore, error) {
	authCfg := resolveAuthConfig(cfg)

	if !authCfg.Enabled {
		slog.Info("auth boot-time wiring: disabled "+
			"(auth.enabled=false in config / FLOWSTATE_AUTH_ENABLED=false), "+
			"running in pass-through mode",
			"phase", "PR5/C10",
		)
		return api.AuthBundle{}, nil, nil
	}

	mode := resolveString("FLOWSTATE_AUTH_MODE", authCfg.Mode, identity.ModeDeploymentLogin)

	source, err := buildIdentitySource(mode, authCfg)
	if err != nil {
		return api.AuthBundle{}, nil, err
	}

	allowed := splitCSV(os.Getenv("FLOWSTATE_AUTH_ALLOWED_ORIGINS"))
	if len(allowed) == 0 {
		allowed = authCfg.AllowedOrigins
	}
	if len(allowed) == 0 {
		allowed = []string{"localhost:*"} // pre-PR1 lift default
	}

	secureCookies := envBoolDefault("FLOWSTATE_AUTH_SECURE_COOKIES", authCfg.SecureCookies)

	memStore := store.NewMemoryStore()
	sessionCfg := auth.DefaultSessionConfig()
	sessionCfg.SecureCookies = secureCookies
	sessionCfg.Mode = mode
	sessionMgr := auth.NewSessionManager(memStore, sessionCfg)

	csrfKey, err := resolveCSRFKey(authCfg.CSRFKey)
	if err != nil {
		return api.AuthBundle{}, nil, err
	}
	csrfCfg := auth.DefaultCSRFConfig()
	csrfCfg.AuthKey = csrfKey
	csrfCfg.SecureCookies = secureCookies
	// QA WARN-5 fix (May 2026): thread the resolved AllowedOrigins
	// allowlist through gorilla/csrf's TrustedOrigins. Without this,
	// every cross-origin POST was rejected at the CSRF gate regardless
	// of the auth allowlist — gorilla/csrf has its OWN Origin check
	// (exact-host match against TrustedOrigins) that runs in parallel
	// to RequireOrigin. Sharing the slice keeps the two layers in
	// lockstep so a cross-origin request the perimeter accepts isn't
	// silently dropped by the CSRF middleware. See
	// internal/auth/csrf.go:35 (CSRFConfig.TrustedOrigins doc).
	csrfCfg.TrustedOrigins = allowed

	bundle := api.AuthBundle{
		Origin:  auth.OriginConfig{AllowedOrigins: allowed},
		Session: sessionMgr,
		Auth: auth.AuthConfig{
			Enabled: true,
			Mode:    mode,
		},
		CSRF:           csrfCfg,
		IdentitySource: source,
	}
	return bundle, memStore, nil
}

// resolveAuthConfig returns the effective config.AuthConfig after
// folding cfg with FLOWSTATE_AUTH_ENABLED. Other fields are folded at
// their resolution sites (resolveString / splitCSV / envBoolDefault /
// resolveCSRFKey) because the env-vs-config rules differ per field.
//
// Expected:
//   - cfg may be nil; nil cfg yields DefaultAuthConfig().
//
// Returns:
//   - The resolved AuthConfig with Enabled folded.
//
// Side effects:
//   - Reads the FLOWSTATE_AUTH_ENABLED env var.
func resolveAuthConfig(cfg *config.AppConfig) config.AuthConfig {
	base := config.DefaultAuthConfig()
	if cfg != nil {
		base = cfg.Auth
	}
	// Env var override — only when explicitly set. Empty / unset
	// preserves cfg.Auth.Enabled (which DefaultAuthConfig sets to true).
	if raw := strings.TrimSpace(os.Getenv("FLOWSTATE_AUTH_ENABLED")); raw != "" {
		base.Enabled = envBool("FLOWSTATE_AUTH_ENABLED")
	}
	return base
}

// resolveString picks env var (highest precedence), then cfgVal, then
// fallback. Empty / whitespace env values are treated as unset so an
// operator can't accidentally clobber the config with an empty deploy
// var.
func resolveString(envVar, cfgVal, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(envVar)); v != "" {
		return v
	}
	if v := strings.TrimSpace(cfgVal); v != "" {
		return v
	}
	return fallback
}

// buildIdentitySource picks the right identity.Source for the configured
// mode and reads its supporting fields (env → cfg).
//
// Expected:
//   - mode is one of identity.Mode* constants.
//   - cfg carries config-layer fallbacks for Secret / PrincipalID /
//     DisplayName.
//
// Returns:
//   - The identity.Source.
//   - Error on unknown mode or, for multi-user, an unparseable users.json.
//
// Side effects:
//   - Reads users.json from disk in multi-user mode (logs a warn if
//     absent — plan §"Bootstrap UX" multi-user).
func buildIdentitySource(mode string, cfg config.AuthConfig) (identity.Source, error) {
	switch mode {
	case identity.ModeSharedSecret:
		secret := resolveString("FLOWSTATE_AUTH_SECRET", cfg.Secret, "")
		return identity.NewSharedSecretSource(secret), nil
	case identity.ModeDeploymentLogin:
		secret := resolveString("FLOWSTATE_AUTH_SECRET", cfg.Secret, "")
		principalID := resolveString("FLOWSTATE_AUTH_PRINCIPAL_ID", cfg.PrincipalID, "")
		displayName := resolveString("FLOWSTATE_AUTH_DISPLAY_NAME", cfg.DisplayName, "")
		return identity.NewDeploymentLoginSource(secret, principalID, displayName), nil
	case identity.ModeMultiUser:
		path := multiUserPath()
		if _, err := os.Stat(path); os.IsNotExist(err) {
			slog.Warn("auth.mode = multi-user but no users.json — "+
				"every login will fail. Run \"flowstate auth user add "+
				"<username>\" to provision a user.",
				"path", path,
			)
		}
		return identity.NewMultiUserSource(path)
	default:
		return nil, fmt.Errorf("unknown auth mode %q "+
			"(want one of: shared-secret, per-deployment-login, multi-user)", mode)
	}
}

// multiUserPath returns the canonical users.json path. Duplicates the
// helper in auth_user.go intentionally to keep the auth-cli and the
// auth-boot wiring independent; both read XDG_CONFIG_HOME the same way.
func multiUserPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "flowstate", "users.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".config", "flowstate", "users.json")
	}
	return filepath.Join(home, ".config", "flowstate", "users.json")
}

// resolveCSRFKey returns the 32-byte HMAC key used by gorilla/csrf.
//
// PR5/C10 hardens this surface: the ephemeral-random fallback in PR4/C9
// is REMOVED. When neither FLOWSTATE_AUTH_CSRF_KEY nor cfgKey supplies
// a value, the function returns an error pointing the operator at
// `flowstate auth csrf-key gen`. Fails CLOSED rather than the prior
// fail-OPEN-with-warn behaviour — a fresh restart with a fresh
// ephemeral key invalidates every active session silently, which is
// worse than refusing to start.
//
// Resolution order (highest precedence first):
//   1. FLOWSTATE_AUTH_CSRF_KEY env var
//   2. cfgKey (cfg.Auth.CSRFKey)
//   3. ERROR — operator must configure a key.
//
// Key padding/truncation: a configured key is materialised into a
// 32-byte slice. Keys shorter than 32 bytes are zero-padded (gorilla/csrf
// accepts arbitrary AuthKey length but practical security wants 32 +
// bytes); keys longer than 32 bytes are truncated. The
// `flowstate auth csrf-key gen` command emits exactly 32 base64-URL
// bytes (43 chars no padding), matching mintToken().
//
// Expected:
//   - cfgKey may be empty.
//
// Returns:
//   - A 32-byte slice on success.
//   - Error when both env and cfg are empty, with a message pointing at
//     `flowstate auth csrf-key gen`.
//
// Side effects:
//   - Reads FLOWSTATE_AUTH_CSRF_KEY from the environment.
func resolveCSRFKey(cfgKey string) ([]byte, error) {
	source := strings.TrimSpace(os.Getenv("FLOWSTATE_AUTH_CSRF_KEY"))
	if source == "" {
		source = strings.TrimSpace(cfgKey)
	}
	if source == "" {
		return nil, fmt.Errorf("auth.csrf_key is not configured. " +
			"Either set FLOWSTATE_AUTH_CSRF_KEY in the environment, " +
			"add `auth.csrf_key: <key>` to config.yaml, or disable auth " +
			"with `auth.enabled: false`. Generate a key with " +
			"`flowstate auth csrf-key gen`.")
	}
	// Take the first 32 bytes; pad with zero if shorter (defensive —
	// gorilla/csrf panics on empty key but accepts any length 32).
	key := make([]byte, 32)
	copy(key, []byte(source))
	return key, nil
}

// envBool returns true when the named env var is set to a truthy value.
// Accepts "true" / "1" / "yes" (case-insensitive). Empty / unset / any
// other value → false.
//
// Expected:
//   - name is non-empty.
//
// Returns:
//   - bool per the above mapping.
//
// Side effects:
//   - Reads the environment.
func envBool(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return v == "true" || v == "1" || v == "yes"
}

// envBoolDefault is envBool with a fallback when the var is unset.
//
// Expected:
//   - name is non-empty.
//
// Returns:
//   - When unset: def. When set to "false"/"0"/"no": false. Otherwise true.
//
// Side effects:
//   - Reads the environment.
func envBoolDefault(name string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if v == "" {
		return def
	}
	if v == "false" || v == "0" || v == "no" {
		return false
	}
	return true
}

// splitCSV splits a comma-separated string into trimmed non-empty
// entries. Empty input returns nil so the caller can fall back to a
// default allowlist.
//
// Expected:
//   - raw may be empty.
//
// Returns:
//   - A slice of trimmed entries; nil when raw is empty.
//
// Side effects:
//   - None.
func splitCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
