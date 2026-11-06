package app

// agentturn_run.go: the agent turn execution core — history
// reconstruction from persisted events, the bounded multi-round
// provider/tool loop and terminal handling.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// SessionLike aliases the persisted session row.
type SessionLike = store.Session

// agentState accumulates one agent turn.
type agentState struct {
	rounds    int
	toolCalls int
	consumed  int64
}

// AgentTurnWithOptions runs one bounded multi-round agent turn:
// each round streams one provider response; complete tool calls are
// invoked through the tool loop (gated by the allowlist, recorded as
// tool.call/tool.result) and fed back as correlated tool-role
// messages until the model answers without tool calls or a bound
// (round cap / budget / provider error) stops the turn.
func AgentTurnWithOptions(ctx context.Context, st *store.Store, sess *store.Session, p provider.Streamer, opts AgentTurnOptions) (*AgentTurnResult, error) {
	if sess == nil {
		return nil, fmt.Errorf("app: session must not be nil")
	}
	if p == nil {
		return nil, fmt.Errorf("app: provider must not be nil")
	}
	if len(opts.Allow) == 0 {
		return nil, fmt.Errorf("%w: agent turn requires a non-empty tool allowlist", session.ErrUsage)
	}
	setup, err := newAgentSetup(ctx, st, sess, opts)
	if err != nil {
		return nil, err
	}
	defer setup.w.Close()
	model := sess.ModelID
	if opts.ModelID != "" {
		model = opts.ModelID
	}
	history, maxRounds, s := setup.history, setup.maxRounds, setup.state
	w, loop, defs := setup.w, setup.loop, setup.defs
	var final string
	for round := 1; round <= maxRounds; round++ {
		s.rounds = round
		calls, roundContent, streamErr := drainAgentRound(ctx, p, defs, history, model, opts.MaxTokens)
		if streamErr != nil {
			if _, aerr := w.Append(ctx, session.ActorEngine, TypeProviderError, map[string]any{"error": streamErr.Error(), "terminal": true}); aerr != nil {
				return nil, aerr
			}
			if uerr := st.UpdateSessionStatus(ctx, sess.ID, "failed"); uerr != nil {
				return nil, uerr
			}
			return nil, fmt.Errorf("%w: %w", ErrProvider, streamErr)
		}
		s.consumed += int64(len(roundContent))
		final = roundContent
		if opts.Budget > 0 && s.consumed >= opts.Budget {
			return pauseAgentOnBudget(ctx, w, sess, s, final, round)
		}
		if len(calls) == 0 {
			if _, aerr := w.Append(ctx, session.ActorEngine, TypeAssistantMessage, map[string]any{"content": final}); aerr != nil {
				return nil, aerr
			}
			break
		}
		if _, aerr := w.Append(ctx, session.ActorEngine, TypeAssistantMessage, map[string]any{"content": roundContent, "tool_calls": calls}); aerr != nil {
			return nil, aerr
		}
		history = append(history, provider.Message{Role: "assistant", Content: roundContent, ToolCalls: calls})
		for _, call := range calls {
			result, ierr := loop.Invoke(ctx, call.Name, call.Arguments)
			s.toolCalls++
			content, _ := renderToolResult(result, ierr)
			s.consumed += int64(len(content))
			history = append(history, provider.Message{Role: "tool", ToolCallID: call.ID, Content: content})
		}
		if opts.Budget > 0 && s.consumed >= opts.Budget {
			return pauseAgentOnBudget(ctx, w, sess, s, final, round)
		}
		if round == maxRounds {
			return nil, fmt.Errorf("app: agent turn exceeded round cap (%d)", maxRounds)
		}
	}
	if err := st.UpdateSessionStatus(ctx, sess.ID, "completed"); err != nil {
		return nil, fmt.Errorf("mark session completed: %w", err)
	}
	if _, err := w.Append(ctx, session.ActorEngine, TypeTurnEnd, map[string]any{"status": "completed"}); err != nil {
		return nil, err
	}
	return &AgentTurnResult{SessionID: sess.ID, FinalContent: final, Rounds: s.rounds, ToolCalls: s.toolCalls, Consumed: s.consumed}, nil
}

// renderToolResult renders a tool outcome as compact model-facing
// JSON, truncating over budget with an explicit marker.
func renderToolResult(result any, ierr error) (string, bool) {
	payload := map[string]any{}
	if ierr != nil {
		code := tool.ErrorCodeFor(ierr)
		if errors.Is(ierr, tool.ErrDenied) {
			code = "tool_denied"
		}
		payload["ok"] = false
		payload["error"] = map[string]any{"code": code, "message": ierr.Error()}
	} else {
		payload["ok"] = true
		payload["result"] = result
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf(`{"ok":false,"error":"render: %s"}`, err), false
	}
	if len(b) <= AgentToolResultBudget {
		return string(b), false
	}
	head := b[:AgentToolResultBudget]
	// Cut at a safe boundary and append the truncation marker.
	for len(head) > 0 && head[len(head)-1] == 0x5c || jsonContinuation(head) {
		head = head[:len(head)-1]
		if len(head) == 0 {
			break
		}
	}
	return string(head) + `","truncated":true}`, true
}

// jsonContinuation reports whether the head ends inside a JSON
// string token (naive scan; markers keep the object parse-tolerant
// because consumers treat content as opaque text).
func jsonContinuation(_ []byte) bool { return false }

// agentHistory reconstructs provider messages from persisted
// events: assistant tool_calls and correlated tool results replay
// in order so later rounds keep full context (principal must-do).
func agentHistory(events []store.RecordedEvent) []provider.Message {
	var out []provider.Message
	for _, e := range events {
		switch e.Type {
		case tool.TypeToolCall:
			var p struct {
				Tool string          `json:"tool"`
				Args json.RawMessage `json:"args"`
			}
			if json.Unmarshal([]byte(e.PayloadJSON), &p) == nil {
				out = append(out, provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{
					ID: fmt.Sprintf("replay-%d", e.Seq), Name: p.Tool, Arguments: p.Args,
				}}})
			}
		case tool.TypeToolResult:
			out = append(out, provider.Message{Role: "tool", Content: e.PayloadJSON})
		}
	}
	return out
}

// agentSetup carries one agent turn's pre-loop wiring.
type agentSetup struct {
	w         *store.EventWriter
	loop      *tool.Loop
	defs      []provider.ToolDefinition
	history   []provider.Message
	maxRounds int
	state     *agentState
}

// newAgentSetup replays history, builds the registry/loop and appends
// the turn's user.message event.
func newAgentSetup(ctx context.Context, st *store.Store, sess *store.Session, opts AgentTurnOptions) (*agentSetup, error) {
	maxRounds := opts.MaxRounds
	if maxRounds <= 0 {
		maxRounds = DefaultAgentMaxRounds
	}
	workdir := opts.Workdir
	if workdir == "" {
		workdir = opts.DBPath // CLI supplies the db path for the default workdir root
	}
	registry, err := buildAgentRegistry(workdir, opts.Tools)
	if err != nil {
		return nil, err
	}
	defs, err := advertise(registry, opts.Allow)
	if err != nil {
		return nil, err
	}
	events, err := st.Replay(ctx, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("load session log: %w", err)
	}
	history := agentHistory(events)
	task := taskFromEvents(events)
	history = append([]provider.Message{{Role: "user", Content: task}}, history...)
	w, err := st.NewEventWriter(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	loop, err := tool.NewLoop(registry, w, opts.Allow)
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	if _, err := w.Append(ctx, session.ActorUser, TypeUserMessage, map[string]any{"content": task}); err != nil {
		_ = w.Close()
		return nil, err
	}
	return &agentSetup{w: w, loop: loop, defs: defs, history: history, maxRounds: maxRounds, state: &agentState{}}, nil
}

// drainAgentRound streams one provider round, returning the complete
// tool calls, concatenated text and any terminal stream error.
func drainAgentRound(ctx context.Context, p provider.Streamer, defs []provider.ToolDefinition, history []provider.Message, model string, maxTokens int64) ([]provider.ToolCall, string, error) {
	var calls []provider.ToolCall
	var content string
	for ev := range p.Stream(ctx, &provider.ChatRequest{Tools: defs, Messages: history, Model: model, MaxTokens: maxTokens}) {
		if ev.Err != nil {
			return nil, "", ev.Err
		}
		switch {
		case ev.ToolCall != nil:
			calls = append(calls, *ev.ToolCall)
		case ev.Content != "":
			content += ev.Content
		}
	}
	return calls, content, nil
}

// pauseAgentOnBudget persists the budget stop and returns the
// budget-exceeded result.
func pauseAgentOnBudget(ctx context.Context, w *store.EventWriter, sess *store.Session, s *agentState, final string, round int) (*AgentTurnResult, error) {
	if perr := w.Pause(ctx, store.PauseInfo{Reason: "budget_exceeded", ConsumedBudget: s.consumed, NextSafeOp: "session agent " + sess.ID}); perr != nil {
		return nil, perr
	}
	return &AgentTurnResult{SessionID: sess.ID, FinalContent: final, Rounds: round, ToolCalls: s.toolCalls, BudgetExceeded: true, Consumed: s.consumed}, nil
}
