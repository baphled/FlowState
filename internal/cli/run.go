package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/baphled/flowstate/internal/app"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/sessionid"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/spf13/cobra"
)

// RunOptions configures non-interactive prompt execution.
type RunOptions struct {
	Prompt  string
	Agent   string
	JSON    bool
	Session string
	// Stats toggles a one-line compression summary printed to stderr
	// before exit. Item 2's workaround for the fact that ephemeral
	// `flowstate run` processes do not feed the /metrics endpoint
	// served by `flowstate serve` (each CLI invocation is its own
	// process with its own Prometheus registry).
	Stats bool
}

// runResponse represents the JSON response from a non-interactive prompt execution.
//
// Expected:
//   - None.
//
// Returns:
//   - N/A (type definition).
//
// Side effects:
//   - None.
type runResponse struct {
	Agent    string `json:"agent"`
	Prompt   string `json:"prompt"`
	Response string `json:"response"`
	Session  string `json:"session,omitempty"`
}

// newRunCmd creates the run command for non-interactive prompt execution.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with run options.
//
// Side effects:
//   - Registers run command flags.
func newRunCmd(getApp func() *app.App) *cobra.Command {
	opts := &RunOptions{
		Agent: "worker",
	}

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run a prompt non-interactively",
		Long:  "Run a prompt to completion for scripting and pipeline use.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPrompt(cmd, getApp(), opts)
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&opts.Prompt, "prompt", "p", "", "The prompt to send to the agent (required)")
	flags.StringVar(&opts.Agent, "agent", opts.Agent, "Agent to use (default: worker)")
	flags.BoolVar(&opts.JSON, "json", false, "Output result as JSON")
	flags.StringVar(&opts.Session, "session", "",
		"Session ID to use or resume. Generated automatically if "+
			"omitted. Must not contain path separators or a leading "+
			"dot.")
	flags.BoolVar(&opts.Stats, "stats", false,
		"Print a one-line compression summary to stderr before exit "+
			"(micro/auto counts, tokens saved, overhead tokens). "+
			"Use this for ad-hoc visibility; the /metrics endpoint "+
			"served by `flowstate serve` does not see ephemeral runs.")

	return cmd
}

// runPrompt executes a prompt non-interactively and outputs the response.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance with a configured engine.
//   - opts is a non-nil RunOptions with a non-empty prompt.
//
// Returns:
//   - nil on success, or an error if validation or execution fails.
//
// Side effects:
//   - Streams response to stdout, saves session if available.
func runPrompt(cmd *cobra.Command, application *app.App, opts *RunOptions) error {
	// Install a signal-linked context so SIGTERM / SIGINT (outer
	// `timeout` expiry, Ctrl-C, kill -TERM) cancels the stream rather
	// than terminating the process with an unflushed parent session.
	// Without this, the planner's accumulated turns evaporate at
	// process exit because saveSession only ran on the graceful return
	// path: see "Parent Session Lost on Non-Graceful Exit (April 2026)"
	// for the forensic reconstruction. Matches the pattern already in
	// runServe.
	//
	// signal.NotifyContext delivers a cancellation on the first signal
	// and installs the default handler for subsequent signals, so a
	// second Ctrl-C still kills the process — the operator retains the
	// usual escape hatch.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runPromptCtx(ctx, cmd, application, opts)
}

// runPromptCtx is the context-aware core of the run command. Extracted
// from runPrompt so the signal-driven persist-on-cancel behaviour can
// be exercised in-process by spec code that cancels ctx directly,
// without needing to send real signals to the test runner. The outer
// runPrompt is a thin shim that provides a signal-linked context.
//
// Expected:
//   - ctx is non-nil; when cancelled, streaming unwinds and the
//     function returns the ctx.Err wrapped in the usual "stream error"
//     prefix.
//   - cmd, application, and opts match runPrompt's expectations.
//
// Returns:
//   - nil on success, or the streaming / validation / save error.
//
// Side effects:
//   - Streams response to cmd.OutOrStdout.
//   - Attempts session persistence on every return path via defer, so
//     a signal-driven cancel still flushes the parent session JSON
//     with whatever messages accumulated up to that point. This is
//     the fix's load-bearing line.
func runPromptCtx(ctx context.Context, cmd *cobra.Command, application *app.App, opts *RunOptions) error {
	if err := validateRunOptions(opts); err != nil {
		return err
	}

	if application.Streamer == nil {
		return errors.New("engine not configured")
	}

	agentName := resolveAgentName(opts.Agent, configDefaultAgent(application))
	resolvedAgent, swarmCtx, err := resolveAgentOrSwarm(application, agentName)
	if err != nil {
		return err
	}
	agentName = resolvedAgent
	sessionID := resolveSessionID(opts.Session)
	loadExistingSession(application, opts.Session)
	persistRootSessionMetadata(application.SessionsDir(), sessionID, agentName)

	// Persist-on-return: fires for every exit path, including a
	// context cancellation propagated from SIGTERM / SIGINT. The
	// previous saveSession call lived on the graceful path only; that
	// was the root cause of the 136-byte `.meta.json` only footprint
	// observed for long-running planner sessions that got killed by
	// outer timeouts.
	defer saveSession(cmd, application, sessionID)

	wrappedStreamer := streaming.NewSessionContextStreamer(
		application.Streamer,
		func() string { return sessionID },
		session.IDKey{},
	)

	response, err := streamResponse(ctx, cmd, wrappedStreamer, application.Engine, swarmCtx, agentName, opts)
	if err != nil {
		return err
	}

	// Wait for the L3 knowledge-extraction goroutine dispatched by the
	// stream to finish before the process exits. Without this, each
	// short-lived run orphans its extraction at os.Exit and the
	// session-memory store is never saved to disk.
	if application.Engine != nil {
		waitForBackgroundExtractions(application.Engine, resolveBackgroundExtractionWait(application))
	}
	if opts.Stats && application.Engine != nil {
		// Per-session snapshot, not the cumulative aggregate — a long-
		// running flowstate serve (or interactive chat) process would
		// otherwise show carried-forward totals from every previous
		// session that shared the engine, which is the exact bug the
		// user reported: "the token counter doesn't reset when I start
		// a new session".
		writeCompressionStats(cmd.ErrOrStderr(), application.Engine.SessionCompressionMetrics(sessionID))
	}
	return writeRunOutput(cmd, opts, agentName, sessionID, response)
}

// writeCompressionStats emits the Item 2 ad-hoc per-turn compression
// summary. Goes to stderr so it does not corrupt JSON output on stdout
// when --json is also set. The format is intentionally compact —
// key=value on a single line — so operators can grep it easily in
// pipelines without needing to parse structured output.
//
// Expected:
//   - out is a non-nil writer.
//   - metrics may be the zero value when the engine was not wired
//     with a CompressionMetrics struct; the function still emits a
//     single line of zeros so the flag behaves consistently.
//
// Side effects:
//   - Writes one line to out.
func writeCompressionStats(out io.Writer, metrics ctxstore.CompressionMetrics) {
	_, _ = fmt.Fprintf(out,
		"compression: micro=%d auto=%d tokens_saved=%d overhead=%d\n",
		metrics.MicroCompactionCount,
		metrics.AutoCompactionCount,
		metrics.TokensSaved,
		metrics.OverheadTokens,
	)
}

// defaultBackgroundExtractionWait is the fallback bound applied when
// application config is unavailable. The extractor itself runs under a
// 30-second LLM timeout; the CLI gives it matching headroom plus a
// small margin for the final disk write (atomic temp-then-rename).
// Callers with access to the loaded CompressionConfig should prefer
// compression.session_memory.wait_timeout instead.
const defaultBackgroundExtractionWait = 35 * time.Second

// resolveBackgroundExtractionWait picks the effective pre-exit wait
// timeout from the loaded CompressionConfig when available, falling
// back to defaultBackgroundExtractionWait when the config has not been
// plumbed through (e.g. embedded tests using a minimal App).
//
// Expected:
//   - application is non-nil. Callers that short-circuited on a nil
//     engine have already returned above.
//
// Returns:
//   - The configured compression.session_memory.wait_timeout when it is
//     > 0, or defaultBackgroundExtractionWait otherwise.
//
// Side effects:
//   - None.
func resolveBackgroundExtractionWait(application *app.App) time.Duration {
	if application == nil || application.Config == nil {
		return defaultBackgroundExtractionWait
	}
	if w := application.Config.Compression.SessionMemory.WaitTimeout; w > 0 {
		return w
	}
	return defaultBackgroundExtractionWait
}

// backgroundExtractionWaiter is the narrow capability the CLI exit path
// needs from the engine. Expressed as an interface so tests can supply
// a test double that deterministically returns a scripted error — the
// real engine's WaitForBackgroundExtractions would require spinning an
// actual goroutine past the deadline, which is slow and flaky.
type backgroundExtractionWaiter interface {
	// WaitForBackgroundExtractions blocks until every dispatched
	// extraction finishes or timeout elapses. Returns nil on clean
	// finish or when timeout <= 0 (caller opted out of waiting).
	// Returns engine.ErrExtractionTimeout when the wait expired with
	// work still in flight. Callers on timeout must assume session-
	// memory state is incomplete. See M7.
	WaitForBackgroundExtractions(timeout time.Duration) error
}

// waitForBackgroundExtractions drives the pre-exit wait and surfaces a
// structured warning on timeout. The prior call site threw the return
// value away, leaving operators with no signal when the wait expired:
// partial `memory.json.tmp` files could be left on disk without any
// log entry to point at the run where it happened.
//
// Expected:
//   - waiter is non-nil. Callers that have no engine (embedded tests)
//     must short-circuit before invoking this helper.
//   - timeout is the maximum duration to block.
//
// Returns:
//   - None. Timeout is not an error for the run command — the prompt
//     response has already been written; surfacing the timeout as an
//     error would mask that success.
//
// Side effects:
//   - Blocks the caller for up to timeout.
//   - Emits slog.Warn on timeout with the configured timeout in
//     seconds so operators can correlate partial session-memory state
//     with the specific run.
func waitForBackgroundExtractions(waiter backgroundExtractionWaiter, timeout time.Duration) {
	err := waiter.WaitForBackgroundExtractions(timeout)
	if err == nil {
		// Clean finish OR caller passed a non-positive timeout and
		// opted out of waiting. Neither is worth a warning; the
		// opted-out path is an operator choice, and the clean-finish
		// path is the happy case.
		return
	}
	if !errors.Is(err, engine.ErrExtractionTimeout) {
		// Unknown error shape — surface it so operators can diagnose
		// future waiter implementations, but do not downgrade the
		// level. The warn template stays consistent so log-processors
		// do not need to learn new patterns.
		slog.Warn(
			"knowledge extraction wait returned unexpected error",
			"timeout_seconds", int(timeout/time.Second),
			"err", err,
		)
		return
	}
	slog.Warn(
		"knowledge extraction timed out before exit; session memory may be incomplete",
		"timeout_seconds", int(timeout/time.Second),
	)
}

// validateRunOptions checks that required options are set.
//
// Expected:
//   - opts is a non-nil RunOptions.
//
// Returns:
//   - nil if valid, or an error if the prompt is empty.
//
// Side effects:
//   - None.
func validateRunOptions(opts *RunOptions) error {
	if strings.TrimSpace(opts.Prompt) == "" {
		return errors.New("prompt is required")
	}
	// H4 — reject path-unsafe --session values at the CLI gate so no
	// downstream code (L1 spillover, L3 session-memory) ever builds a
	// filepath.Join on a traversal sequence. Empty is permitted here
	// because resolveSessionID generates a UUID in that case; only the
	// non-empty branch needs checking.
	if opts.Session != "" {
		if err := sessionid.Validate(opts.Session); err != nil {
			return fmt.Errorf("invalid --session value: %w", err)
		}
	}
	return nil
}

// resolveAgentOrSwarm is the T-swarm-2 CLI-side resolver shared by
// both `flowstate run --agent <id>` and `flowstate chat --agent <id>`.
// It applies the spec §2 precedence — agent registry first, swarm
// registry second — and returns:
//
//   - resolved is the agent id the streamer should drive. For agent
//     hits and historical pass-through this is the input verbatim;
//     for swarm hits it is the swarm's lead, so the engine's
//     existing per-engine setup engages the lead's manifest.
//   - swarmCtx is the swarm.Context the runner should install on the
//     engine when the id resolved to a swarm; nil otherwise (the
//     engine reverts to single-agent shape).
//   - err is *swarm.NotFoundError when both registries are non-nil
//     and neither knows the id. When neither registry is configured
//     the resolver passes through silently — that preserves the
//     historical CLI test contract where a bare engine accepts any
//     `--agent` value because the runtime engine does not validate
//     against a registry.
//
// Expected:
//   - application is non-nil. Tests with no engine still flow through
//     here because the swarm-context install is gated on Engine != nil.
//   - id is the user-provided `--agent` value, post-trim.
//
// Side effects:
//   - None. The caller is responsible for installing swarmCtx on the
//     engine — keeping this helper pure makes it trivial to unit-test.
func resolveAgentOrSwarm(application *app.App, id string) (string, *swarm.Context, error) {
	if application == nil {
		return id, nil, nil
	}
	agentReg := application.Registry
	hasAgent := func(name string) bool {
		if agentReg == nil {
			return false
		}
		if _, ok := agentReg.Get(name); ok {
			return true
		}
		_, ok := agentReg.GetByNameOrAlias(name)
		return ok
	}
	return swarm.ResolveTarget(hasAgent, application.SwarmRegistry, id)
}

// configDefaultAgent reads application.Config.DefaultAgent defensively
// — the App is fully wired in production but tests sometimes drive
// runPrompt against a partial App where Config is nil. Returning ""
// in those cases lets resolveAgentName fall through to the historical
// "worker" sentinel without crashing.
func configDefaultAgent(application *app.App) string {
	if application == nil || application.Config == nil {
		return ""
	}
	return application.Config.DefaultAgent
}

// resolveAgentName returns the trimmed agent name. When agent is empty
// or whitespace-only, it falls back to defaultAgent. When BOTH are
// empty, it falls back to the historical "worker" sentinel — kept so
// pre-existing callers that pass only the user-supplied flag (with no
// config fallback) keep their old behaviour.
//
// The two-argument shape mirrors resolveChatAgentName so the CLI run
// command can honour the operator's config.default_agent the same
// way `flowstate chat` already does.
func resolveAgentName(agent, defaultAgent string) string {
	name := strings.TrimSpace(agent)
	if name != "" {
		return name
	}
	fallback := strings.TrimSpace(defaultAgent)
	if fallback != "" {
		return fallback
	}
	return "worker"
}

// resolveSessionID returns the session ID, generating a new one if empty.
//
// Expected:
//   - session is a string (may be empty).
//
// Returns:
//   - The session ID, or a newly generated one if session is empty.
//
// Side effects:
//   - None.
func resolveSessionID(sessionParam string) string {
	if sessionParam == "" {
		return generateSessionID()
	}
	return sessionParam
}

// loadExistingSession loads a session into the engine if a session ID is provided.
//
// Expected:
//   - application is a non-nil App instance.
//   - session is a string (may be empty).
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Loads session into the engine if session is non-empty and sessions store is available.
func loadExistingSession(application *app.App, sessionParam string) {
	if sessionParam == "" || application.Sessions == nil {
		return
	}
	store, err := application.Sessions.Load(sessionParam)
	if err == nil {
		application.Engine.SetContextStore(store, sessionParam)
	}
}

// streamResponse streams a response from the streamer and returns the complete message.
//
// Expected:
//   - ctx is non-nil; cancellation propagates to the streaming runner
//     so a SIGTERM / SIGINT received by runPrompt unwinds the stream
//     instead of leaving the goroutine orphaned at process exit.
//   - cmd is a non-nil cobra.Command.
//   - streamer is a non-nil streaming.Streamer for response generation.
//   - agentName is a non-empty string.
//   - opts is a non-nil RunOptions with a non-empty prompt.
//
// Returns:
//   - The complete response string and nil on success, or empty string and error on failure.
//
// Side effects:
//   - Streams response chunks to stdout if JSON output is not requested.
func streamResponse(
	ctx context.Context,
	cmd *cobra.Command,
	streamer streaming.Streamer,
	eng swarm.DispatchEngine,
	swarmCtx *swarm.Context,
	agentName string,
	opts *RunOptions,
) (string, error) {
	consumer := NewWriterConsumer(cmd.OutOrStdout(), opts.JSON)
	if err := swarm.DispatchSwarm(ctx, eng, swarmCtx, streamer, consumer, agentName, opts.Prompt); err != nil {
		return "", err
	}
	if consumer.Err() != nil {
		return "", fmt.Errorf("stream error: %w", consumer.Err())
	}
	return consumer.Response(), nil
}

// saveSession saves the current session if the session store is available.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//   - sessionID is a non-empty string.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Saves session to the store if available, writes warning to stderr on failure.
func saveSession(cmd *cobra.Command, application *app.App, sessionID string) {
	if application.Sessions == nil {
		return
	}
	store := application.Engine.ContextStore()
	if store == nil {
		return
	}
	loadedSkills := application.Engine.LoadedSkills()
	skillNames := make([]string, 0, len(loadedSkills))
	for i := range loadedSkills {
		skillNames = append(skillNames, loadedSkills[i].Name)
	}
	metadata := ctxstore.SessionMetadata{
		AgentID:      application.Engine.Manifest().ID,
		SystemPrompt: application.Engine.BuildSystemPrompt(),
		LoadedSkills: skillNames,
	}
	if err := application.Sessions.Save(sessionID, store, metadata); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to save session: %v\n", err)
	}
}

// writeRunOutput writes the response in the requested format (JSON or plain text).
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - opts is a non-nil RunOptions.
//   - agentName is a non-empty string.
//   - sessionID is a non-empty string.
//   - response is a string (may be empty).
//
// Returns:
//   - nil on success, or an error if output fails.
//
// Side effects:
//   - Writes response to stdout in JSON or plain text format.
func writeRunOutput(cmd *cobra.Command, opts *RunOptions, agentName, sessionID, response string) error {
	if opts.JSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(runResponse{
			Agent:    agentName,
			Prompt:   opts.Prompt,
			Response: response,
			Session:  sessionID,
		})
	}

	if !strings.HasSuffix(response, "\n") {
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
	}
	return nil
}
