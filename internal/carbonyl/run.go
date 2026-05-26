package carbonyl

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// RunOptions configures the terminal rendering session.
//
// Stdin/Stdout/Stderr are forwarded to the Carbonyl subprocess. They default
// to the parent's controlling terminal so Carbonyl can ioctl on the tty; tests
// override with nil/Discard to keep fake-binary subprocesses from holding the
// test runner's stdio past Cmd.WaitDelay.
type RunOptions struct {
	BinaryPath string
	FPS        int
	Zoom       int
	StaticDir  string
	APIMux     http.Handler
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

// Run starts an ephemeral API server serving the Vue SPA and launches
// Carbonyl to render it in the terminal. It is a drop-in replacement
// for tui.Run with the same signature.
//
// Expected:
//   - application is a non-nil AppInterface with a configured API server.
//   - agentID identifies the agent; when empty the Vue SPA shows the picker.
//   - sessionID identifies the chat session context.
//
// Returns:
//   - nil when the Carbonyl process exits cleanly.
//   - An error if the ephemeral server or Carbonyl bridge fails to start.
//
// Side effects:
//   - Sets the agent manifest on the engine when agentID is non-empty.
//   - Publishes a session-resumed event on the event bus.
//   - Registers the session with the session manager.
//   - Persists session metadata to disk.
//   - Starts an HTTP server on 127.0.0.1:0.
//   - Launches the Carbonyl subprocess; blocks until it exits.
func Run(application AppInterface, agentID string, sessionID string) error {
	return RunWithOptions(application, agentID, sessionID, DefaultRunOptions())
}

// DefaultRunOptions returns RunOptions with sensible defaults derived
// from the environment (CARBONYL_BINARY, FLOWSTATE_WEB_DIR, PATH lookup).
func DefaultRunOptions() RunOptions {
	return RunOptions{
		BinaryPath: resolveCarbonylBinary(),
		FPS:        defaultFPS,
		Zoom:       defaultZoom,
		StaticDir:  resolveStaticDir(),
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	}
}

// RunWithOptions is the same as Run but accepts explicit options,
// bypassing environment-based defaults. Useful for testing.
func RunWithOptions(application AppInterface, agentID string, sessionID string, opts RunOptions) error {
	if agentID != "" {
		application.SetAgentManifest(agentID)
	}

	publishResumedEvent(application.EventBus(), sessionID)

	if mgr := application.SessionMgr(); mgr != nil {
		mgr.RegisterSession(sessionID, agentID)
	}

	persistSessionMetadata(application.SessionsDir(), sessionID, agentID)

	var apiHandler http.Handler
	if opts.APIMux != nil {
		apiHandler = opts.APIMux
	} else if api := application.APIServer(); api != nil {
		apiHandler = api.Handler()
	} else {
		return errNoAPIHandler
	}

	ephemeral, err := NewEphemeralServer(apiHandler, opts.StaticDir)
	if err != nil {
		return fmt.Errorf("carbonyl: failed to create ephemeral server: %w", err)
	}

	if err := ephemeral.Start(); err != nil {
		return fmt.Errorf("carbonyl: failed to start ephemeral server: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ephemeral.Shutdown(shutdownCtx)
	}()

	targetURL := buildTargetURL(ephemeral.URL(), agentID, sessionID)

	cfg := DefaultConfig().
		WithBinary(opts.BinaryPath).
		WithURL(targetURL).
		WithFPS(opts.FPS).
		WithZoom(opts.Zoom)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge := New(cfg)
	if opts.Stdin != nil || opts.Stdout != nil || opts.Stderr != nil {
		bridge.Stdin = opts.Stdin
		bridge.Stdout = opts.Stdout
		bridge.Stderr = opts.Stderr
	}
	if err := bridge.Start(ctx); err != nil {
		return fmt.Errorf("carbonyl: failed to start bridge: %w", err)
	}
	defer bridge.Stop()

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sigChan)

		select {
		case <-ctx.Done():
			return
		case <-sigChan:
			if bridge.IsRunning() {
				bridge.Stop()
			}
		}
	}()

	return bridge.Wait()
}

// OpenInBrowser starts an ephemeral API server and prints the URL for
// the user to open in their browser. Blocks until SIGINT or SIGTERM.
func OpenInBrowser(application AppInterface, agentID string, sessionID string) error {
	opts := DefaultRunOptions()

	var apiHandler http.Handler
	if opts.APIMux != nil {
		apiHandler = opts.APIMux
	} else if api := application.APIServer(); api != nil {
		apiHandler = api.Handler()
	} else {
		return errNoAPIHandler
	}

	ephemeral, err := NewEphemeralServer(apiHandler, opts.StaticDir)
	if err != nil {
		return fmt.Errorf("carbonyl: failed to create ephemeral server: %w", err)
	}

	if err := ephemeral.Start(); err != nil {
		return fmt.Errorf("carbonyl: failed to start ephemeral server: %w", err)
	}

	targetURL := buildTargetURL(ephemeral.URL(), agentID, sessionID)
	fmt.Fprintf(os.Stdout, "Opening browser: %s\n", targetURL)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	<-sigChan

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ephemeral.Shutdown(shutdownCtx)
}

// PrintURL starts an ephemeral API server and prints the URL to stdout.
// Useful for debugging or SSH sessions where Carbonyl is unavailable.
// Blocks until SIGINT or SIGTERM.
func PrintURL(application AppInterface, agentID string, sessionID string) error {
	opts := DefaultRunOptions()

	var apiHandler http.Handler
	if opts.APIMux != nil {
		apiHandler = opts.APIMux
	} else if api := application.APIServer(); api != nil {
		apiHandler = api.Handler()
	} else {
		return errNoAPIHandler
	}

	ephemeral, err := NewEphemeralServer(apiHandler, opts.StaticDir)
	if err != nil {
		return fmt.Errorf("carbonyl: failed to create ephemeral server: %w", err)
	}

	if err := ephemeral.Start(); err != nil {
		return fmt.Errorf("carbonyl: failed to start ephemeral server: %w", err)
	}

	targetURL := buildTargetURL(ephemeral.URL(), agentID, sessionID)
	fmt.Fprintf(os.Stdout, "FlowState running at: %s\n", targetURL)
	fmt.Fprintf(os.Stdout, "Press Ctrl+C to stop.\n")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	<-sigChan

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ephemeral.Shutdown(shutdownCtx)
}

// AppInterface abstracts the application dependencies needed by the
// Carbonyl runner. The production implementation is *app.App; test
// doubles implement this interface instead.
type AppInterface interface {
	SetAgentManifest(agentID string)
	EventBus() EventBus
	SessionMgr() SessionRegistrar
	SessionsDir() string
	APIServer() APIServer
}

// EventBus is a minimal event bus interface. The production
// implementation is *eventbus.EventBus; for the Carbonyl runner the
// interface is intentionally empty because publishResumedEvent is a
// no-op placeholder until the event bus is threaded through.
type EventBus interface{}

// SessionRegistrar abstracts session registration. The production
// implementation is *session.Manager.
type SessionRegistrar interface {
	RegisterSession(sessionID string, agentID string)
}

// APIServer abstracts the HTTP API server. The production implementation
// is *api.Server.
type APIServer interface {
	Handler() http.Handler
}

func buildTargetURL(baseURL, agentID, sessionID string) string {
	params := []string{}
	if sessionID != "" {
		params = append(params, "session="+sessionID)
	}
	if agentID != "" {
		params = append(params, "agent="+agentID)
	}
	if len(params) == 0 {
		return baseURL
	}
	return baseURL + "?" + strings.Join(params, "&")
}

func publishResumedEvent(_ EventBus, _ string) {
}

func persistSessionMetadata(sessionsDir, sessionID, agentID string) {
	if sessionsDir == "" || sessionID == "" {
		return
	}
	metaPath := filepath.Join(sessionsDir, sessionID+".meta.json")
	content := fmt.Sprintf(`{"id":"%s","agent_id":"%s","status":"active","created_at":"%s"}`,
		sessionID, agentID, time.Now().Format(time.RFC3339))
	os.WriteFile(metaPath, []byte(content), 0600)
}

func resolveCarbonylBinary() string {
	if path := os.Getenv("CARBONYL_BINARY"); path != "" {
		return path
	}
	if path, err := lookPath("carbonyl"); err == nil {
		return path
	}
	return "carbonyl"
}

func resolveStaticDir() string {
	if dir := os.Getenv("FLOWSTATE_WEB_DIR"); dir != "" {
		return dir
	}
	candidates := []string{
		"web/dist",
		"../web/dist",
		filepath.Join(os.Getenv("HOME"), ".local", "share", "flowstate", "web", "dist"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "index.html")); err == nil {
			return c
		}
	}
	return ""
}

// lookPath is aliased for testing so tests can inject a custom binary
// resolver without polluting the real PATH lookup.
var lookPath = defaultLookPath

func defaultLookPath(name string) (string, error) {
	return exec.LookPath(name)
}
