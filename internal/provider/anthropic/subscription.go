// Package anthropic implements the Anthropic provider: two independent routes
// chosen by providers.anthropic.auth_type. This file is the OAuth (subscription)
// route: it reaches a user's Claude subscription ONLY by running the user's own,
// unmodified `claude` binary in headless print mode and reading its stream-json
// output. The API-key route (apikey.go) sends the user's API key to the Messages
// API and shares none of the rules below, which describe the OAuth route only.
//
// Policy basis (https://code.claude.com/docs/en/legal-and-compliance,
// "Authentication and credential use"): third-party developers may not offer
// Claude.ai login nor "collect, store, or intermediate Claude.ai credentials
// or session tokens"; an end user may still sign in to the unmodified Claude
// Code binary with their own subscription. Hard rules that follow: this
// package never reads, stores, refreshes or forwards any Claude credential or
// token; never reads ~/.claude; never sends a request to Anthropic itself
// (the API-key route does, but with an API key, never a Claude credential);
// never imitates Claude Code identity headers. Sign-in happens in Claude Code
// (`claude`, then /login) and the child inherits the environment minus the variables that redirect it
// away from that sign-in (see childEnv).
//
// Known limits (OAuth route): structured tool calls are refused before any
// process starts; the prompt is a "User:" / "Assistant:" transcript, so stdin never starts
// with "/" and cannot run as a local slash command; MaxTokens is ignored.
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

var (
	// ErrUnsupportedTools marks a request that carries FlowState structured tool calls.
	ErrUnsupportedTools = errors.New(
		"anthropic: provider does not support FlowState structured tool calls; the run must not use a tool surface")
	// ErrNotSignedIn marks a Claude Code authentication failure.
	ErrNotSignedIn = errors.New("anthropic: Claude Code is not signed in; run `claude` and use /login")
	// ErrInvalidModel marks a model name that could be read as a command-line flag.
	ErrInvalidModel = errors.New("anthropic: invalid model name")
	// ErrInvalidRequest marks a request that cannot be mapped to a prompt.
	ErrInvalidRequest = errors.New("anthropic: invalid request")
	// ErrSystemPromptTooLarge marks a system prompt over MaxSystemPromptBytes.
	ErrSystemPromptTooLarge = errors.New("anthropic: system prompt too large")
	// ErrNoLauncher marks a Subscription built without a Launcher.
	ErrNoLauncher = errors.New("anthropic: no launcher configured")
	// ErrInvalidWorkDir marks a working-directory factory result that is empty, relative or not clean.
	ErrInvalidWorkDir = errors.New("anthropic: invalid working directory")
	// ErrNoProcess marks a Launcher that returned neither a process nor an error.
	ErrNoProcess = errors.New("anthropic: launcher returned no process")
	// ErrLineTooLong marks a stdout line over the line cap.
	ErrLineTooLong = errors.New("anthropic: stdout line too long")
)

// NewSubscription returns the OAuth-route provider that runs bin (DefaultBin
// when empty) through launch, which is required. It does not run the binary
// or check sign-in.
func NewSubscription(bin string, launch Launcher, opts ...Option) *Subscription {
	if bin == "" {
		bin = DefaultBin
	}
	p := &Subscription{bin: bin, launch: launch,
		grace: DefaultResultGrace, maxLine: MaxLineBytes}
	for _, o := range opts {
		o(p)
	}
	return p
}

// WithWorkDir replaces the working-directory factory; nil keeps the default.
// The provider never removes a directory this factory returns.
func WithWorkDir(f WorkDirFactory) Option {
	return func(p *Subscription) {
		if f != nil {
			p.workDir = f
		}
	}
}

// WithResultGrace sets how long the child may linger after its result line
// before it is killed. A non-positive d keeps DefaultResultGrace.
func WithResultGrace(d time.Duration) Option {
	return func(p *Subscription) {
		if d > 0 {
			p.grace = d
		}
	}
}

// WithMaxLine sets the cap in bytes on one stdout line. A non-positive n
// keeps MaxLineBytes.
func WithMaxLine(n int) Option {
	return func(p *Subscription) {
		if n > 0 {
			p.maxLine = n
		}
	}
}

// Name implements provider.Provider.
func (p *Subscription) Name() string { return "anthropic" }

// Models lists the Claude Code model aliases; a full model id is also accepted.
func (p *Subscription) Models() []string { return []string{"sonnet", "opus", "haiku"} }

// Chat performs one turn by collecting Stream.
func (p *Subscription) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	var sb strings.Builder
	for ev := range p.Stream(ctx, req) {
		if ev.Err != nil {
			return nil, ev.Err
		}
		sb.WriteString(ev.Content)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &provider.ChatResponse{Content: sb.String()}, nil
}

// Stream runs one headless Claude Code process and emits its text deltas, then
// a finish event or one terminal error. The channel closes when the stream
// ends; cancelling ctx kills the process.
func (p *Subscription) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	out := make(chan provider.StreamEvent)
	cmd, cleanup, err := p.command(req)
	go func() {
		defer close(out)
		if err != nil {
			send(ctx, out, provider.StreamEvent{Err: err})
			return
		}
		p.run(ctx, out, cmd, cleanup)
	}()
	return out
}

// run calls cleanup once the child has been reaped. After a result line the
// turn is over, so the child gets the grace period to exit and is then killed;
// the wait is therefore bounded. Reaping inline keeps the cleanup from being lost.
func (p *Subscription) run(ctx context.Context, out chan<- provider.StreamEvent, cmd Command, cleanup func()) {
	if ctx.Err() != nil { // nothing to run for a turn nobody waits for
		cleanup()
		return
	}
	pctx, cancel := context.WithCancel(ctx)
	proc, err := p.launch(pctx, cmd)
	if err == nil && proc == nil {
		err = ErrNoProcess
	}
	if err != nil {
		cancel()
		cleanup()
		send(ctx, out, provider.StreamEvent{Err: err})
		return
	}
	stdout := proc.Stdout()
	if stdout == nil {
		stdout = io.NopCloser(strings.NewReader(""))
	}
	// Killing the child cannot end a read that a descendant keeps open, so
	// cancellation closes the pipe itself.
	context.AfterFunc(pctx, func() { _ = stdout.Close() })
	ps := &parser{}
	final, readErr := pump(ctx, stdout, ps, p.maxLine, out)
	if readErr != nil {
		cancel() // the child may be blocked writing to a pipe nobody reads
	}
	waitErr := p.reap(proc, cancel, cleanup)
	if ctx.Err() != nil {
		return
	}
	if ps.done {
		// Sent only after the reap: a consumer may exit on the terminal event,
		// and the directory must already be gone by then.
		for _, ev := range final {
			if !send(ctx, out, ev) {
				return
			}
		}
		return
	}
	if readErr != nil {
		waitErr = readErr
	}
	send(ctx, out, provider.StreamEvent{Err: ps.endError(waitErr, proc.Stderr())})
}

// reap waits for the child, killing it (by cancelling its context) once the
// grace period passes, then runs cleanup.
func (p *Subscription) reap(proc Process, cancel context.CancelFunc, cleanup func()) error {
	t := time.AfterFunc(p.grace, cancel)
	err := proc.Wait()
	t.Stop()
	cancel()
	cleanup()
	return err
}

// privateWorkDir is the default WorkDirFactory: a fresh directory directly
// under the system temp directory, named with WorkDirPrefix.
func privateWorkDir() (string, error) { return os.MkdirTemp("", WorkDirPrefix) }

// removeDefaultDir deletes a directory that privateWorkDir created in this
// same call. As a second condition it still requires a direct child of the
// current system temp directory named with WorkDirPrefix; if TMPDIR changed
// since creation the directory is left behind rather than risk another path.
// RemoveAll errors are deliberately ignored: nothing can act on them here.
func removeDefaultDir(dir string) {
	clean := filepath.Clean(dir)
	if filepath.Dir(clean) != filepath.Clean(os.TempDir()) ||
		!strings.HasPrefix(filepath.Base(clean), WorkDirPrefix) {
		return
	}
	_ = os.RemoveAll(clean)
}

// pump forwards parsed events until a result line, EOF, a read error or
// cancellation. The result line's events are returned, not sent.
func pump(ctx context.Context, r io.Reader, ps *parser, maxLine int,
	out chan<- provider.StreamEvent) (final []provider.StreamEvent, err error) {
	br := newLineReader(r, maxLine)
	for ctx.Err() == nil {
		line, rerr := br.next()
		evs := ps.feed(line)
		if ps.done {
			return evs, nil
		}
		for _, ev := range evs {
			if !send(ctx, out, ev) {
				return nil, nil
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return nil, nil
			}
			return nil, fmt.Errorf("read stdout: %w", rerr)
		}
	}
	return nil, nil
}

// send delivers ev unless ctx is cancelled first; it reports delivery.
func send(ctx context.Context, out chan<- provider.StreamEvent, ev provider.StreamEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}
