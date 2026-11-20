// Package claudecode implements the claude-code provider: it reaches a
// user's Claude subscription ONLY by running the user's own, unmodified
// `claude` binary in headless print mode and reading its stream-json output.
//
// Policy basis (https://code.claude.com/docs/en/legal-and-compliance,
// "Authentication and credential use"): third-party developers may not offer
// Claude.ai login in their own applications nor "collect, store, or
// intermediate Claude.ai credentials or session tokens — sign-in to a Claude
// account must complete through Anthropic's own flow". This does not prevent
// "an end user from signing in to the unmodified Claude Code binary with their
// own Claude subscription". Hard rules that follow: this package never reads,
// stores, refreshes or forwards any Claude credential or token; never reads
// ~/.claude; never sends a request to Anthropic itself; never sets or imitates
// Claude Code identity headers. Sign-in happens in Claude Code (`claude`,
// then /login) and the child inherits the environment unchanged.
//
// Known limits of this first cut: FlowState structured tool calls are
// refused (fail closed, no process is started); multi-turn history is mapped
// to a lossy plain-text transcript; MaxTokens cannot be expressed to the CLI
// and is ignored here, so FlowState's own budget enforcement on the stream
// still applies.
package claudecode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
)

// ErrUnsupportedTools marks a request that carries FlowState structured tool calls.
var ErrUnsupportedTools = errors.New(
	"claude-code: provider does not support FlowState structured tool calls; the run must not use a tool surface")

// ErrNotSignedIn marks a Claude Code authentication failure.
var ErrNotSignedIn = errors.New("claude-code: Claude Code is not signed in; run `claude` and use /login")

// ErrBinaryNotFound marks a missing Claude Code executable.
var ErrBinaryNotFound = errors.New("claude-code: Claude Code executable not found")

// New returns a provider that runs bin (DefaultBin when empty). It does not
// run the binary or check sign-in.
func New(bin string, opts ...Option) *Provider {
	if bin == "" {
		bin = DefaultBin
	}
	p := &Provider{bin: bin, launch: execLauncher}
	for _, o := range opts {
		o(p)
	}
	return p
}

// WithLauncher replaces the process launcher; it exists so tests need no real process.
func WithLauncher(l Launcher) Option {
	return func(p *Provider) { p.launch = l }
}

// Name implements provider.Provider.
func (p *Provider) Name() string { return "claude-code" }

// Models lists the Claude Code model aliases; a full model id is also accepted.
func (p *Provider) Models() []string { return []string{"sonnet", "opus", "haiku"} }

// Chat performs one turn by collecting Stream.
func (p *Provider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
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
func (p *Provider) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	out := make(chan provider.StreamEvent)
	cmd, err := p.command(req)
	go func() {
		defer close(out)
		if err != nil {
			send(ctx, out, provider.StreamEvent{Err: err})
			return
		}
		p.run(ctx, out, cmd)
	}()
	return out
}

// command validates req and builds the invocation; no process is started.
func (p *Provider) command(req *provider.ChatRequest) (Command, error) {
	if err := rejectTools(req); err != nil {
		return Command{}, err
	}
	prompt, system, err := mapMessages(req.Messages)
	if err != nil {
		return Command{}, err
	}
	model := req.Model
	if model == "" {
		model = DefaultModel
	}
	args := []string{
		"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--no-session-persistence", "--tools", "", "--strict-mcp-config", "--setting-sources", "",
		"--model", model,
	}
	if system != "" {
		args = append(args, "--system-prompt", system)
	}
	return Command{Bin: p.bin, Args: args, Dir: os.TempDir(), Stdin: prompt}, nil
}

func (p *Provider) run(ctx context.Context, out chan<- provider.StreamEvent, cmd Command) {
	proc, err := p.launch(ctx, cmd)
	if err != nil {
		send(ctx, out, provider.StreamEvent{Err: err})
		return
	}
	ps := &parser{}
	readErr := pump(ctx, proc.Stdout(), ps, out)
	waitErr := proc.Wait()
	if ctx.Err() != nil || ps.done {
		return
	}
	if readErr != nil && waitErr == nil {
		waitErr = readErr
	}
	send(ctx, out, provider.StreamEvent{Err: ps.endError(waitErr, proc.Stderr())})
}

// pump forwards parsed events until a result line, EOF, a read error or cancellation.
func pump(ctx context.Context, r io.Reader, ps *parser, out chan<- provider.StreamEvent) error {
	br := newLineReader(r)
	for ctx.Err() == nil {
		line, err := br.next()
		for _, ev := range ps.feed(line) {
			if !send(ctx, out, ev) {
				return nil
			}
		}
		if ps.done {
			return nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read stdout: %w", err)
		}
	}
	return nil
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

// rejectTools fails closed on any structured tool surface.
func rejectTools(req *provider.ChatRequest) error {
	if len(req.Tools) > 0 {
		return ErrUnsupportedTools
	}
	for i := range req.Messages {
		m := &req.Messages[i]
		if len(m.ToolCalls) > 0 || m.ToolCallID != "" || m.Role == "tool" {
			return ErrUnsupportedTools
		}
	}
	return nil
}

// mapMessages returns the stdin prompt and the joined system prompt. A single
// user message is passed verbatim; any longer history becomes a lossy plain
// transcript of "User:" / "Assistant:" blocks ending with the latest message.
func mapMessages(msgs []provider.Message) (prompt, system string, err error) {
	var sys, rest []string
	var turns []provider.Message
	for _, m := range msgs {
		switch m.Role {
		case "system":
			sys = append(sys, m.Content)
		case "user", "assistant":
			turns = append(turns, m)
		default:
			return "", "", fmt.Errorf("claude-code: unsupported message role %q", m.Role)
		}
	}
	if len(turns) == 0 {
		return "", "", errors.New("claude-code: request has no user message")
	}
	system = strings.Join(sys, "\n\n")
	if len(turns) == 1 && turns[0].Role == "user" {
		return turns[0].Content, system, nil
	}
	for _, m := range turns {
		label := "User"
		if m.Role == "assistant" {
			label = "Assistant"
		}
		rest = append(rest, label+":\n"+m.Content)
	}
	return strings.Join(rest, "\n\n"), system, nil
}
