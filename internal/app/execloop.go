package app

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

// ErrExecLoopConfig rejects a loop built with missing collaborators or non-positive limits.
var ErrExecLoopConfig = errors.New("app: execution loop misconfigured")

// ErrExecLoopBudget reports pool exhaustion before the next turn.
var ErrExecLoopBudget = errors.New("app: execution loop budget exhausted")

// ErrExecLoopFenced reports a fenced or foreign-held lease.
var ErrExecLoopFenced = errors.New("app: execution loop lease fenced")

const execTurnBudgetFloor int64 = 1 // minimum sane reservation unit per turn

// RunExecLoop runs the bounded provider-driven loop to a budget/fence/provider stop.
func RunExecLoop(ctx context.Context, cfg ExecLoopConfig) (*ExecLoopResult, error) {
	if err := validateExecLoopConfig(&cfg); err != nil {
		return nil, err
	}
	token, err := acquireExecLease(ctx, &cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cfg.Store.ReleaseLease(context.Background(), cfg.LeaseItem, cfg.LeaseAgent, token) }()
	return runExecTurns(ctx, &cfg, token)
}
func acquireExecLease(ctx context.Context, cfg *ExecLoopConfig) (int64, error) {
	token, err := cfg.Store.AcquireLease(ctx, cfg.LeaseItem, cfg.LeaseAgent)
	if err != nil {
		if errors.Is(err, store.ErrLeaseHeld) {
			return 0, fmt.Errorf("%w: %v", ErrExecLoopFenced, err)
		}
		return 0, fmt.Errorf("exec loop lease: %w", err)
	}
	if err := cfg.Store.FenceLease(ctx, cfg.LeaseItem, cfg.LeaseAgent, token); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrExecLoopFenced, err)
	}
	return token, nil
}
func runExecTurns(ctx context.Context, cfg *ExecLoopConfig, token int64) (*ExecLoopResult, error) {
	definitions, err := SessionToolDefinitions(cfg.Registry, cfg.Allowlist)
	if err != nil {
		// A tool-surface mismatch is argument misuse, not a provider
		// failure: it must surface as usage before any provider
		// request or budget spend (CLI-AND-API-CONTRACT §§1–2).
		return nil, fmt.Errorf("%w: %v", ErrUsage, err)
	}
	res, history := &ExecLoopResult{}, []provider.Message{}
	if err := replayInto(ctx, cfg.Store, cfg.Session.ID, &history); err != nil {
		return nil, err
	}
	for res.Turns < cfg.MaxTurns {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		stop, serr := reserveExecBudget(ctx, cfg)
		if serr != nil {
			return res, serr
		}
		if stop {
			res.BudgetExhausted = true
			return res, ErrExecLoopBudget
		}
		if err := cfg.Store.FenceLease(ctx, cfg.LeaseItem, cfg.LeaseAgent, token); err != nil {
			return res, fmt.Errorf("%w: %v", ErrExecLoopFenced, err)
		}
		turn, terr := runExecTurn(ctx, *cfg, definitions, history)
		res.Turns++
		res.FinalContent = turn.Content
		res.FinishReason = turn.FinishReason
		if terr != nil {
			return res, terr
		}
		history = append(history, provider.Message{Role: "assistant", Content: turn.Content, ToolCalls: turn.Calls})
		if len(turn.Calls) == 0 {
			return res, nil
		}
		results, rerr := executeCalls(ctx, *cfg, turn.Calls)
		res.ToolCalls += int64(len(turn.Calls))
		if rerr != nil {
			return res, rerr
		}
		history = append(history, results...)
	}
	return res, nil
}
func reserveExecBudget(ctx context.Context, cfg *ExecLoopConfig) (stop bool, err error) {
	if cfg.Pool == nil || cfg.TurnBudget <= 0 || cfg.Pool.Reserve(cfg.TurnBudget) == cfg.TurnBudget {
		return false, nil
	}
	return true, stopBudgetExhausted(ctx, cfg.Store, cfg.Session.ID)
}

// failExecTurn records a terminal provider failure: the provider.error
// event is appended before the session is marked failed, as on every
// other terminal path, so a reader that observes the terminal status
// always finds its final event already committed. A failed append
// leaves the status untouched. The result always wraps ErrProvider
// and cause, plus the persistence failure when there is one.
func failExecTurn(ctx context.Context, w EventWriter, st SessionRepo, sessionID string, cause error) error {
	if _, aerr := w.Append(ctx, session.ActorEngine, "provider.error", map[string]any{"error": cause.Error(), "terminal": true}); aerr != nil {
		return fmt.Errorf("%w: %w: record provider error: %w", ErrProvider, cause, aerr)
	}
	if uerr := st.UpdateSessionStatus(ctx, sessionID, "failed"); uerr != nil {
		return fmt.Errorf("%w: %w: mark session failed: %w", ErrProvider, cause, uerr)
	}
	return fmt.Errorf("%w: %w", ErrProvider, cause)
}

func runExecTurn(ctx context.Context, cfg ExecLoopConfig, definitions []provider.ToolDefinition, history []provider.Message) (execTurn, error) {
	w, err := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
	if err != nil {
		return execTurn{}, err
	}
	defer func() { _ = w.Close() }()
	var turn execTurn
	req := &provider.ChatRequest{Tools: definitions, Messages: withSystemPrompt(cfg.SystemPrompt, history), Model: cfg.Session.ModelID, MaxTokens: cfg.MaxTokens}
	for ev := range cfg.Streamer.Stream(ctx, req) {
		switch {
		case ev.Err != nil:
			return execTurn{}, failExecTurn(ctx, w, cfg.Store, cfg.Session.ID, ev.Err)
		case ev.Kind == provider.StreamEventToolCall && ev.ToolCall != nil:
			turn.Calls = append(turn.Calls, *ev.ToolCall)
		case ev.Kind == provider.StreamEventFinish:
			turn.FinishReason = ev.FinishReason
		case ev.Kind == provider.StreamEventText || ev.Kind == "":
			if _, aerr := w.Append(ctx, session.ActorEngine, "assistant.delta", map[string]any{"delta": ev.Content}); aerr != nil {
				return execTurn{}, aerr
			}
			turn.Content += ev.Content
		}
	}
	if _, aerr := w.Append(ctx, session.ActorEngine, "assistant.message", map[string]any{"content": turn.Content}); aerr != nil {
		return execTurn{}, aerr
	}
	return turn, nil
}

// withSystemPrompt returns the request messages: a role-system message first
// when prompt is non-empty, then the history. The prompt is per-request only; it
// is never appended to history, persisted, or reconstructed by replay.
func withSystemPrompt(prompt string, history []provider.Message) []provider.Message {
	if prompt == "" {
		return history
	}
	return append([]provider.Message{{Role: "system", Content: prompt}}, history...)
}

func allowSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

func executeCalls(ctx context.Context, cfg ExecLoopConfig, calls []provider.ToolCall) ([]provider.Message, error) {
	results := make([]provider.Message, 0, len(calls))
	allowed := allowSet(cfg.Allowlist)
	for _, call := range calls {
		w, werr := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
		if werr != nil {
			return nil, werr
		}
		var serr error
		results, serr = runCall(ctx, cfg, w, call, allowed, results)
		_ = w.Close()
		if serr != nil {
			return nil, serr
		}
	}
	return results, nil
}

func appendResult(ctx context.Context, loop *tool.Loop, call provider.ToolCall, results []provider.Message) ([]provider.Message, error) { // appends one call outcome; persistence failures terminate the run
	failed := func(content string) []provider.Message {
		return append(results, provider.Message{Role: "tool", ToolCallID: call.ID, Content: content})
	}
	outcome, ierr := loop.Invoke(ctx, call.Name, call.Arguments)
	if ierr != nil {
		if errors.Is(ierr, tool.ErrEventAppend) || errors.Is(ierr, ErrCallEventSticky) {
			return results, ierr // infrastructure failure: terminate the run
		}
		return failed(fmt.Sprintf("error: %s", ierr)), nil
	}
	encoded, merr := json.Marshal(outcome)
	if merr != nil {
		return failed("error: unserialisable result"), nil
	}
	return failed(string(encoded)), nil
}

func answeredResultIDs(events []store.RecordedEvent) map[string]bool { // ids with a persisted correlated tool.result
	persisted := map[string]bool{} // tool_call_id of persisted correlated results
	for _, e := range events {
		if e.Type != tool.TypeToolResult {
			continue
		}
		if id := payloadFieldText(e.PayloadJSON, "tool_call_id"); id != "" {
			persisted[id] = true
		}
	}
	return persisted
}

func replayInto(ctx context.Context, st *store.Store, sessionID string, history *[]provider.Message) error {
	events, rerr := st.Replay(ctx, sessionID)
	if rerr != nil {
		return rerr
	}
	persisted := answeredResultIDs(events) // two-pass replay invariants: see below
	for _, e := range events {
		switch e.Type {
		case session.TypeTurnStart:
			*history = append(*history, provider.Message{Role: "user", Content: payloadFieldText(e.PayloadJSON, "task")})
		case tool.TypeToolCall:
			if id := payloadFieldText(e.PayloadJSON, "tool_call_id"); id == "" || !persisted[id] {
				continue // uncorrelated or dangling call: skip, never orphan
			}
			*history = append(*history, provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{
				ID:        payloadFieldText(e.PayloadJSON, "tool_call_id"),
				Name:      payloadFieldText(e.PayloadJSON, "tool"),
				Arguments: json.RawMessage(payloadFieldRaw(e.PayloadJSON, "args")),
			}}})
		case tool.TypeToolResult:
			if id := payloadFieldText(e.PayloadJSON, "tool_call_id"); id != "" {
				res := payloadFieldRaw(e.PayloadJSON, "result")
				if res == "null" {
					res = payloadFieldRaw(e.PayloadJSON, "error")
				}
				*history = append(*history, provider.Message{Role: "tool", ToolCallID: id, Content: res})
			} // empty id = result-only denial: skip, never orphan
		}
	}
	return nil
}

// stopBudgetExhausted appends a turn.end budget-stopped event.
func stopBudgetExhausted(ctx context.Context, st *store.Store, sessionID string) error {
	w, err := st.NewEventWriter(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("budget stop writer: %w", err)
	}
	defer func() { _ = w.Close() }()
	if _, err := w.Append(ctx, session.ActorEngine, "turn.end", map[string]any{"status": "budget_exhausted"}); err != nil {
		return fmt.Errorf("record budget stop: %w", err)
	}
	return nil
}
func validateExecLoopConfig(cfg *ExecLoopConfig) error {
	switch {
	case cfg.Store == nil, cfg.Session == nil, cfg.Streamer == nil, cfg.Registry == nil:
		return fmt.Errorf("%w: store, session, streamer and registry are required", ErrExecLoopConfig)
	case cfg.LeaseItem == "" || cfg.LeaseAgent == "":
		return fmt.Errorf("%w: lease item and agent are required", ErrExecLoopConfig)
	case cfg.MaxTurns <= 0:
		return fmt.Errorf("%w: max turns must be positive", ErrExecLoopConfig)
	case cfg.Pool != nil && cfg.TurnBudget < execTurnBudgetFloor:
		return fmt.Errorf("%w: turn budget must be at least %d with a pool", ErrExecLoopConfig, execTurnBudgetFloor)
	}
	return nil
}
func payloadMap(payloadJSON string) map[string]any {
	var p map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
		return nil
	}
	return p
}
func payloadFieldText(payloadJSON, field string) string {
	text, _ := payloadMap(payloadJSON)[field].(string) // missing fields read as ""
	return text
}
func payloadFieldRaw(payloadJSON, field string) string {
	encoded, err := json.Marshal(payloadMap(payloadJSON)[field])
	if err != nil {
		return "" // unreachable for map[string]any values
	}
	return string(encoded)
}

// runCall invokes one call via the plain writer (denied) or the correlated adapter (allowlisted).
func runCall(ctx context.Context, cfg ExecLoopConfig, w tool.EventSink, call provider.ToolCall, allowed map[string]bool, results []provider.Message) ([]provider.Message, error) {
	ctx = withParentEvents(ctx, w) // the one writer the loop holds, for tools that record their own events
	loop, lerr := func() (*tool.Loop, error) {
		if !allowed[call.Name] {
			return tool.NewLoop(cfg.Registry, w, cfg.Allowlist)
		}
		sink, serr := NewCallEventSink(w, call)
		if serr != nil {
			return nil, serr
		}
		return tool.NewLoop(cfg.Registry, sink, cfg.Allowlist)
	}()
	if lerr != nil {
		return nil, lerr
	}
	return appendResult(ctx, loop, call, results)
}
