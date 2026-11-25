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

// delegateSchema is the JSON schema of the delegate arguments: either agent and
// task (one child), tasks (up to 8, exclusive with the others) or resume (a
// child id, with an optional follow-up task).
const delegateSchema = `{"type":"object","properties":{"agent":{"type":"string","minLength":1},` +
	`"task":{"type":"string","minLength":1},"cwd":{"type":"string"},"resume":{"type":"string","minLength":1},` +
	`"tasks":{"type":"array","minItems":1,"maxItems":8,"items":{"type":"object","properties":{` +
	`"agent":{"type":"string","minLength":1},"task":{"type":"string","minLength":1},"cwd":{"type":"string"}},` +
	`"required":["agent","task"],"additionalProperties":false}}},"additionalProperties":false}`

// NewDelegateTool builds the delegate tool for one parent run. The profiles are
// copied and kept in name order, so the description is stable.
func NewDelegateTool(cfg DelegateToolConfig) *DelegateTool {
	cfg.Profiles = slices.SortedFunc(slices.Values(cfg.Profiles), func(a, b agent.Profile) int {
		return strings.Compare(a.Name, b.Name)
	})
	if cfg.limiter == nil {
		cfg.limiter = newDescendantLimiter(cfg.DescendantLimit)
	}
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
	b.WriteString("Delegate one task (agent and task) to a child agent that runs in its own session with its own tools " +
		"and turn cap, and return its final message. A child may delegate in turn only if its profile lists delegate " +
		"and it is not already two levels down. A failed or cancelled child comes back as an error you can act on. " +
		"Or pass tasks, up to 8 entries of agent, task and optional cwd (not with agent or task): they run 4 at a time " +
		"and return {results: [...]} in input order; a child that did not complete is an entry with its status and " +
		"reason, not an error. Or pass resume, the id of an interrupted child of yours, with an optional follow-up task " +
		"(not with agent, cwd or tasks): it continues with the turns it has left.")
	profiles := t.offered()
	if len(profiles) == 0 {
		b.WriteString("\nNo agents are available: no profile was loaded or this agent's delegates list names none.")
	} else {
		b.WriteString("\nAvailable agents:")
	}
	for i, p := range profiles {
		if i == delegateListMax {
			fmt.Fprintf(&b, "\n(and %d more)", len(profiles)-delegateListMax)
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
	t.cfg.slot.yield() // a run waiting for its own children holds no descendant slot
	defer t.cfg.slot.reclaim(ctx)
	if args.batch != nil {
		return t.invokeParallel(ctx, args.batch)
	}
	req := t.baseRequest()
	req.Task, req.Workdir = args.Task, args.Cwd
	if sink, ok := ctx.Value(delegateParentEventsKey{}).(tool.EventSink); ok && sink != nil {
		req.ParentEvents = appendFailureSink{inner: sink}
	}
	var res DelegateResult
	if args.Resume != "" {
		res, err = ResumeDelegatedChild(ctx, DelegateResumeRequest{DelegateRequest: req, ChildID: args.Resume, Profiles: t.cfg.Profiles})
	} else if req.Profile, err = t.profile(args.Agent); err == nil {
		res, err = RunDelegatedChild(ctx, req)
	}
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
	if err := t.allowance().check(name); err != nil {
		return agent.Profile{}, err
	}
	if len(t.offered()) == 0 {
		return agent.Profile{}, errors.New("no agents are available: no profile was loaded")
	}
	names := make([]string, 0, len(t.cfg.Profiles))
	for _, p := range t.offered() {
		if p.Name == name {
			return p, nil
		}
		names = append(names, p.Name)
	}
	return agent.Profile{}, fmt.Errorf("unknown agent %q; available agents: %s", clipRunes(name, delegateNameMaxChars), strings.Join(names, ", "))
}

// parseDelegateArgs decodes the argument object strictly: unknown fields, extra
// JSON, a missing agent or a blank task are errors; `resume` stands alone with an
// optional task and is refused beside agent, cwd or tasks. `tasks` is decoded and validated whole, so a bad
// entry refuses the call before any child starts.
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
	case args.Resume != "":
		return args, parseDelegateResume(args)
	case args.Tasks != nil:
		return parseDelegateBatch(args)
	}
	return args, validateDelegateTask(args.Agent, args.Task)
}

// parseDelegateResume validates a resume call: the child id alone, with an
// optional follow-up task that passes the single form's task checks.
func parseDelegateResume(args delegateArgs) error {
	switch {
	case args.Tasks != nil:
		return errors.New("resume cannot be combined with tasks")
	case args.Agent != "" || args.Cwd != "":
		return errors.New("resume cannot be combined with agent or cwd")
	case args.Task == "":
		return nil
	}
	return validateDelegateTask(args.Resume, args.Task)
}

// parseDelegateBatch decodes and validates the `tasks` array: it is exclusive
// with agent, task and cwd, holds 1 to delegateTasksMax entries, and every entry
// passes the single form's checks; an error names the entry's index.
func parseDelegateBatch(args delegateArgs) (delegateArgs, error) {
	if args.Agent != "" || args.Task != "" || args.Cwd != "" {
		return args, errors.New("tasks cannot be combined with agent, task or cwd")
	}
	dec := json.NewDecoder(bytes.NewReader(args.Tasks))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args.batch); err != nil {
		return args, fmt.Errorf("invalid tasks: %w", err)
	}
	switch {
	case len(args.batch) == 0:
		return args, errors.New("tasks must not be empty")
	case len(args.batch) > delegateTasksMax:
		return args, fmt.Errorf("too many tasks: %d, the limit is %d; no child was started", len(args.batch), delegateTasksMax)
	}
	for i, e := range args.batch {
		if err := validateDelegateTask(e.Agent, e.Task); err != nil {
			return args, fmt.Errorf("tasks[%d]: %w", i, err)
		}
	}
	return args, nil
}

// validateDelegateTask checks the agent name and the task text of one child.
func validateDelegateTask(agentName, task string) error {
	switch {
	case agentName == "":
		return errors.New("agent is required")
	case strings.TrimSpace(task) == "":
		return errors.New("task is required and must not be empty")
	case len(task) > delegateTaskMaxBytes:
		return fmt.Errorf("task is too long: %d bytes, the limit is %d", len(task), delegateTaskMaxBytes)
	case strings.IndexFunc(task, isForbiddenTaskRune) >= 0:
		return errors.New("task must not contain NUL, ESC or C1 control characters")
	}
	return nil
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
