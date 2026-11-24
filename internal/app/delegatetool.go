package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// Limits on the child's final message returned to the parent model (Pi's).
const (
	delegateTextMaxBytes = 50 * 1024
	delegateTextMaxLines = 2000
	// delegateTaskMaxBytes bounds the task, which becomes the child's first event.
	delegateTaskMaxBytes = 64 * 1024
	// delegateNameMaxChars bounds an agent name echoed in an error.
	delegateNameMaxChars = 64
	// delegateDescriptionMaxChars bounds a profile description in the tool definition.
	delegateDescriptionMaxChars = 200
	// delegateListMax is how many profiles the tool description lists.
	delegateListMax = 32
)

// delegateSchema is the JSON schema of the delegate arguments. `tasks` and
// `resume` are deliberately absent: they are not available yet.
const delegateSchema = `{"type":"object","properties":{"agent":{"type":"string","minLength":1},` +
	`"task":{"type":"string","minLength":1},"cwd":{"type":"string"}},"required":["agent","task"],` +
	`"additionalProperties":false}`

// NewDelegateTool builds the delegate tool for one parent run. The profiles are
// copied and kept in name order, so the description is stable.
func NewDelegateTool(cfg DelegateToolConfig) *DelegateTool {
	cfg.Profiles = slices.SortedFunc(slices.Values(cfg.Profiles), func(a, b agent.Profile) int {
		return strings.Compare(a.Name, b.Name)
	})
	return &DelegateTool{cfg: cfg}
}

// withParentEvents returns ctx carrying the parent's per-call event writer. The
// loop calls it in runCall, where it already holds that writer: the store allows
// one writer per session, so the tool must reuse it for the delegation receipt
// instead of opening another. A context without one makes RunDelegatedChild fall
// back to its own, retried, writer (the direct-call path).
func withParentEvents(ctx context.Context, sink tool.EventSink) context.Context {
	return context.WithValue(ctx, delegateParentEventsKey{}, sink)
}

// Append forwards to the wrapped sink and marks a failure as tool.ErrEventAppend.
func (s appendFailureSink) Append(ctx context.Context, actor, typ string, payload any) (int64, error) {
	seq, err := s.inner.Append(ctx, actor, typ, payload)
	if err != nil {
		return seq, fmt.Errorf("%w: %s: %w", tool.ErrEventAppend, typ, err)
	}
	return seq, nil
}

// Name is the tool's allowlist name.
func (*DelegateTool) Name() string { return agent.DelegateTool }

// Definition is the provider-facing tool definition. Its description lists the
// loaded profiles, in name order, so the model can choose; with none it says no
// agent is available.
func (t *DelegateTool) Definition() provider.ToolDefinition {
	var b strings.Builder
	b.WriteString("Delegate one task to a child agent that runs in its own session with its own tools and turn cap, " +
		"and return its final message. A failed or cancelled child comes back as an error you can act on.")
	if len(t.cfg.Profiles) == 0 {
		b.WriteString("\nNo agents are available: no profile was loaded.")
	} else {
		b.WriteString("\nAvailable agents:")
	}
	for i, p := range t.cfg.Profiles {
		if i == delegateListMax {
			fmt.Fprintf(&b, "\n(and %d more)", len(t.cfg.Profiles)-delegateListMax)
			break
		}
		b.WriteString("\n- " + p.Name + ": " + clipRunes(p.Description, delegateDescriptionMaxChars))
	}
	return provider.ToolDefinition{Name: agent.DelegateTool, Description: b.String(), Parameters: json.RawMessage(delegateSchema)}
}

// Invoke runs one child and formats its outcome. A completed child is a
// DelegateToolResult. A failed, cancelled or partial child, a refusal and an
// infrastructure failure are errors, which the loop shows the model as
// "error: ..." without failing the parent (ADR-009 D7). A cancelled parent still
// ends cancelled: the loop checks its context before the next turn.
func (t *DelegateTool) Invoke(ctx context.Context, raw json.RawMessage) (any, error) {
	args, err := parseDelegateArgs(raw)
	if err != nil {
		return nil, err
	}
	profile, err := t.profile(args.Agent)
	if err != nil {
		return nil, err
	}
	req := DelegateRequest{
		Store: t.cfg.Store, Parent: t.cfg.Parent, Streamer: t.cfg.Streamer, Pool: t.cfg.Pool,
		ParentWorkdir: t.cfg.Workdir, Profile: profile, Task: args.Task, Workdir: args.Cwd,
	}
	if sink, ok := ctx.Value(delegateParentEventsKey{}).(tool.EventSink); ok && sink != nil {
		req.ParentEvents = appendFailureSink{inner: sink}
	}
	res, err := RunDelegatedChild(ctx, req)
	if err != nil {
		return nil, err
	}
	if res.Status != DelegateStatusCompleted {
		return nil, errors.New(delegateFailureSummary(res))
	}
	return DelegateToolResult{Child: res.ChildID, Status: res.Status, Turns: res.Turns, Text: capDelegateText(res.FinalContent)}, nil
}

// profile finds a loaded profile; an unknown name lists the available ones.
func (t *DelegateTool) profile(name string) (agent.Profile, error) {
	if len(t.cfg.Profiles) == 0 {
		return agent.Profile{}, errors.New("no agents are available: no profile was loaded")
	}
	names := make([]string, 0, len(t.cfg.Profiles))
	for _, p := range t.cfg.Profiles {
		if p.Name == name {
			return p, nil
		}
		names = append(names, p.Name)
	}
	return agent.Profile{}, fmt.Errorf("unknown agent %q; available agents: %s", clipRunes(name, delegateNameMaxChars), strings.Join(names, ", "))
}

// parseDelegateArgs decodes the argument object strictly: unknown fields, extra
// JSON, a missing agent or a blank task are errors, and so are `tasks` and
// `resume`, which this build does not offer.
func parseDelegateArgs(raw json.RawMessage) (delegateArgs, error) {
	var args delegateArgs
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return args, fmt.Errorf("invalid arguments: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return args, errors.New("invalid arguments: extra data after the JSON object")
	}
	switch {
	case args.Tasks != nil, args.Resume != nil:
		return args, errors.New("tasks and resume are not available yet: " +
			"this build runs one child per call with agent, task and optional cwd")
	case args.Agent == "":
		return args, errors.New("agent is required")
	case strings.TrimSpace(args.Task) == "":
		return args, errors.New("task is required and must not be empty")
	case len(args.Task) > delegateTaskMaxBytes:
		return args, fmt.Errorf("task is too long: %d bytes, the limit is %d", len(args.Task), delegateTaskMaxBytes)
	case strings.IndexFunc(args.Task, isForbiddenTaskRune) >= 0:
		return args, errors.New("task must not contain NUL, ESC or C1 control characters")
	}
	return args, nil
}

// isForbiddenTaskRune reports NUL, ESC and the C1 controls, which a terminal
// obeys. A newline and a tab are ordinary text and stay allowed.
func isForbiddenTaskRune(r rune) bool {
	return r == 0 || r == 0x1b || (r >= 0x80 && r <= 0x9f)
}

// delegateFailureSummary describes a child that did not complete: its id,
// status, turns and reason, and its partial text when it produced any.
func delegateFailureSummary(res DelegateResult) string {
	id := res.ChildID
	if id == "" {
		id = "none"
	}
	msg := fmt.Sprintf("child %s %s after %d turns: %s", id, res.Status, res.Turns, capDelegateText(res.Reason))
	if res.Partial {
		msg += "\npartial output:\n" + capDelegateText(res.FinalContent)
	}
	return msg
}

// capDelegateText cuts text to the first delegateTextMaxLines lines and
// delegateTextMaxBytes bytes, at a character boundary, and says so.
func capDelegateText(text string) string {
	cut := text
	if lines := strings.SplitAfter(cut, "\n"); len(lines) > delegateTextMaxLines {
		cut = strings.Join(lines[:delegateTextMaxLines], "")
	}
	if len(cut) > delegateTextMaxBytes {
		end := delegateTextMaxBytes
		for end > 0 && !utf8.RuneStart(cut[end]) {
			end--
		}
		cut = cut[:end]
	}
	if len(cut) == len(text) {
		return text
	}
	return cut + fmt.Sprintf("\n[truncated: the child's message was cut at %d KB / %d lines]",
		delegateTextMaxBytes/1024, delegateTextMaxLines)
}

// clipRunes cuts s to at most n characters, marking a cut with an ellipsis.
func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "..."
}
