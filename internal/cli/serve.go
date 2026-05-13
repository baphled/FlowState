package cli

import (
	"context"
	"crypto/rand"
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

	// PR4/C9 — boot-time auth wiring (deferred from PR3 ship-state).
	// Reads FLOWSTATE_AUTH_* env vars; when FLOWSTATE_AUTH_ENABLED=true
	// installs the AuthBundle on the api.Server and registers the
	// /api/auth/login + /api/auth/logout routes. When the flag is off
	// (default through PR4) the server keeps its pre-flag behaviour.
	if err := installAuthFromEnv(application.API); err != nil {
		return fmt.Errorf("auth boot-time wiring: %w", err)
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
		var eng engineShutdowner
		if application.Engine != nil {
			eng = application.Engine
		}
		return performServeShutdown(server, eng, cmd.OutOrStdout(), cmd.ErrOrStderr())
	case err := <-errChan:
		return err
	}
}

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

// installAuthFromEnv reads FLOWSTATE_AUTH_* env vars and, when
// FLOWSTATE_AUTH_ENABLED=true, constructs an api.AuthBundle and installs
// it on the server. Also wires the SessionStore used by `flowstate auth
// reset`'s store-cleanup step (memory feedback_close_latent_surfaces_too —
// every place that uses the store should see the same instance).
//
// Env vars (plan §"Bootstrap UX" read order: env → CLI flag → config →
// default. C9 covers the env-var slice; CLI flag + config layer wiring
// land in PR5 when the flag flips default-on).
//
//   - FLOWSTATE_AUTH_ENABLED          → "true" arms the bundle
//   - FLOWSTATE_AUTH_MODE             → "shared-secret" | "per-deployment-login" | "multi-user"
//   - FLOWSTATE_AUTH_SECRET           → shared-secret / per-deployment-login modes
//   - FLOWSTATE_AUTH_PRINCIPAL_ID     → per-deployment-login mode
//   - FLOWSTATE_AUTH_DISPLAY_NAME     → per-deployment-login mode (optional)
//   - FLOWSTATE_AUTH_ALLOWED_ORIGINS  → comma-separated glob list
//   - FLOWSTATE_AUTH_SECURE_COOKIES   → "false" disables Secure (dev only)
//   - FLOWSTATE_AUTH_CSRF_KEY         → 32B HMAC key (hex / base64 / raw);
//                                       boot-time fallback generates one
//                                       and logs the fact (operator-visible
//                                       so the next restart sees the same
//                                       sessions).
//
// Expected:
//   - apiServer is non-nil.
//
// Returns:
//   - nil when FLOWSTATE_AUTH_ENABLED is unset or "false" (no-op).
//   - Error on misconfig (unknown mode, malformed allowlist, store
//     mode-mismatch).
//
// Side effects:
//   - Calls apiServer.InstallAuth(...) which replaces the route map.
//   - Calls SetResetStore(store) so `flowstate auth reset` operates on
//     the same store the live server uses.
//   - May log slog.Warn for first-boot misconfigs (multi-user with no
//     users.json — plan §"Bootstrap UX" multi-user lines 707-709).
func installAuthFromEnv(apiServer *api.Server) error {
	if !envBool("FLOWSTATE_AUTH_ENABLED") {
		// Flag off — keep PR3 pass-through behaviour. Plan §"Rollout
		// Plan" PR5/C10 flips this default.
		slog.Info("auth boot-time wiring: FLOWSTATE_AUTH_ENABLED unset, "+
			"running in pass-through mode (PR3 default)",
			"phase", "PR4/C9",
		)
		return nil
	}

	mode := strings.TrimSpace(os.Getenv("FLOWSTATE_AUTH_MODE"))
	if mode == "" {
		mode = identity.ModeDeploymentLogin // plan §OD-E default
	}

	source, err := buildIdentitySource(mode)
	if err != nil {
		return err
	}

	allowed := splitCSV(os.Getenv("FLOWSTATE_AUTH_ALLOWED_ORIGINS"))
	if len(allowed) == 0 {
		allowed = []string{"localhost:*"} // pre-PR1 lift default
	}

	// SecureCookies defaults to true (HTTPS prod); operator opts out
	// for HTTP-only dev by setting FLOWSTATE_AUTH_SECURE_COOKIES=false.
	secureCookies := envBoolDefault("FLOWSTATE_AUTH_SECURE_COOKIES", true)

	memStore := store.NewMemoryStore()
	sessionCfg := auth.DefaultSessionConfig()
	sessionCfg.SecureCookies = secureCookies
	sessionCfg.Mode = mode
	sessionMgr := auth.NewSessionManager(memStore, sessionCfg)

	csrfKey, err := resolveCSRFKey()
	if err != nil {
		return err
	}
	csrfCfg := auth.DefaultCSRFConfig()
	csrfCfg.AuthKey = csrfKey
	csrfCfg.SecureCookies = secureCookies

	bundle := api.AuthBundle{
		Origin: auth.OriginConfig{AllowedOrigins: allowed},
		Session: sessionMgr,
		Auth: auth.AuthConfig{
			Enabled: true,
			Mode:    mode,
		},
		CSRF:           csrfCfg,
		IdentitySource: source,
	}
	apiServer.InstallAuth(bundle)
	SetResetStore(memStore)

	slog.Info("auth boot-time wiring active",
		"mode", mode,
		"allowed_origins", allowed,
		"secure_cookies", secureCookies,
	)
	return nil
}

// buildIdentitySource picks the right identity.Source for the configured
// mode and reads its supporting env vars.
//
// Expected:
//   - mode is one of identity.Mode* constants.
//
// Returns:
//   - The identity.Source.
//   - Error on unknown mode or, for multi-user, an unparseable users.json.
//
// Side effects:
//   - Reads users.json from disk in multi-user mode (logs a warn if
//     absent — plan §"Bootstrap UX" multi-user).
func buildIdentitySource(mode string) (identity.Source, error) {
	switch mode {
	case identity.ModeSharedSecret:
		return identity.NewSharedSecretSource(os.Getenv("FLOWSTATE_AUTH_SECRET")), nil
	case identity.ModeDeploymentLogin:
		return identity.NewDeploymentLoginSource(
			os.Getenv("FLOWSTATE_AUTH_SECRET"),
			os.Getenv("FLOWSTATE_AUTH_PRINCIPAL_ID"),
			os.Getenv("FLOWSTATE_AUTH_DISPLAY_NAME"),
		), nil
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
		return nil, fmt.Errorf("unknown FLOWSTATE_AUTH_MODE %q", mode)
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

// resolveCSRFKey returns the 32-byte HMAC key used by gorilla/csrf. v1
// reads FLOWSTATE_AUTH_CSRF_KEY (raw bytes). When unset, generates a
// random 32-byte key and logs a warn — the next process restart will
// produce a different key and invalidate sessions, so the operator MUST
// set the env var for any non-ephemeral deployment.
//
// Expected:
//   - None.
//
// Returns:
//   - A 32-byte slice.
//   - Error from rand.Read (effectively never).
//
// Side effects:
//   - On fallback, logs an slog.Warn so the operator notices.
func resolveCSRFKey() ([]byte, error) {
	if raw := os.Getenv("FLOWSTATE_AUTH_CSRF_KEY"); raw != "" {
		// Take the first 32 bytes; pad with zero if shorter (defensive —
		// gorilla/csrf panics on empty key, but accepts any length 32).
		key := make([]byte, 32)
		copy(key, []byte(raw))
		return key, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating random CSRF key: %w", err)
	}
	slog.Warn("FLOWSTATE_AUTH_CSRF_KEY unset; using ephemeral random " +
		"key — sessions WILL NOT survive a server restart. Set " +
		"FLOWSTATE_AUTH_CSRF_KEY=<32+ char string> to fix.")
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
