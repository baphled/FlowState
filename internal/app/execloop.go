package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

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

// ErrMaxTurns reports a run that used all its turns while the model still asked
// for tools (code max_turns, exit 5, HTTP 409). The run ends failed; the last
// turn's tool results are recorded and the partial text stays in the result.
var ErrMaxTurns = errors.New("max_turns")

const execTurnBudgetFloor int64 = 1 // minimum sane reservation unit per turn

// emptyHookSet turns a typed-nil pointer held in a HookSet back into the untyped nil, the empty
// set; any other value is returned unchanged.
func emptyHookSet(h HookSet) HookSet {
	if h == nil {
		return nil
	}
	if v := reflect.ValueOf(h); v.Kind() == reflect.Ptr && v.IsNil() {
		return nil
	}
	return h
}

// observeExecLoop is a test seam: nil in production, and when non-nil RunExecLoop calls it once,
// first, with the config it was given (its Hooks already normalised), so a spec can see what a loop in a delegation tree was
// run with (its session and its Hooks). It observes only on entry, so it cannot see a set dropped
// inside the turns: the packet that first fires a hook must add a spec at the firing site. It is read without synchronisation, so a spec sets it
// before the run starts, restores it with DeferCleanup, and no specs that set it run in
// parallel; the observer itself is called from every loop goroutine and must be race-safe.
var observeExecLoop func(ExecLoopConfig)

// RunExecLoop runs the bounded provider-driven loop to a budget/fence/provider stop under the lease
// handle cfg.Lease, which the caller holds: the loop only fences it and never acquires or releases a
// lease; the caller releases it once the post-loop state writers have returned. A config with no handle
// is refused with ErrExecLoopConfig before anything is read, written or leased.
func RunExecLoop(ctx context.Context, cfg ExecLoopConfig) (*ExecLoopResult, error) {
	cfg.Hooks = emptyHookSet(cfg.Hooks)
	if observe := observeExecLoop; observe != nil {
		observe(cfg)
	}
	if err := validateExecLoopConfig(&cfg); err != nil {
		return nil, err
	}
	if err := cfg.Lease.Fence(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrExecLoopFenced, err)
	}
	return runExecTurns(ctx, &cfg)
}

// fenceExecLease checks the loop's lease is still held through the caller's handle.
func fenceExecLease(ctx context.Context, cfg *ExecLoopConfig) error {
	return cfg.Lease.Fence(ctx)
}

// execCompactLease is the lease the loop hands to Compact: the caller's handle, with its item, owner
// and token. Token is informational: when Held is set, Compact fences through Held, which is
// authoritative.
func execCompactLease(cfg *ExecLoopConfig) CompactLease {
	h := cfg.Lease
	return CompactLease{Item: h.Item(), Agent: h.Owner(), Token: h.Token(), Held: h}
}

func runExecTurns(ctx context.Context, cfg *ExecLoopConfig) (*ExecLoopResult, error) {
	definitions, err := SessionToolDefinitions(cfg.Registry, cfg.Allowlist)
	if err != nil {
		// A tool-surface mismatch is argument misuse, not a provider
		// failure: it must surface as usage before any provider
		// request or budget spend (CLI-AND-API-CONTRACT §§1–2).
		return nil, fmt.Errorf("%w: %v", ErrUsage, err)
	}
	res, history, meter := &ExecLoopResult{}, []provider.Message{}, &compactRun{}
	if err := replayInto(ctx, cfg.Store, cfg.Session.ID, &history); err != nil {
		return nil, err
	}
	for res.Turns < cfg.MaxTurns {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if err := meter.before(ctx, cfg, definitions, &history); err != nil {
			return res, err
		}
		if err := beginExecRequest(ctx, cfg, res); err != nil {
			return res, err
		}
		turn, terr := meter.request(ctx, cfg, definitions, &history, res)
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
		meter.afterResults(results)
		if res.Turns >= cfg.MaxTurns {
			// The model still wants to continue and the limit is reached. A
			// cancel during this turn's calls is a cancellation, not a
			// success; otherwise the run fails, after the last turn's
			// results are recorded.
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			return res, failExecLimit(ctx, cfg, res.Turns)
		}
	}
	return res, nil
}

// recordTurn folds a finished provider turn into the run's result: the turn count, the final
// text and finish reason, and the usage.
func (r *ExecLoopResult) recordTurn(turn execTurn) {
	r.Turns++
	r.FinalContent = turn.Content
	r.FinishReason = turn.FinishReason
	r.recordUsage(turn.Usage)
}

// failExecMaxTurns records the terminal max_turns failure: the provider.error
// event, carrying reason max_turns and terminal true, is appended before the
// session is marked failed, as failExecTurn does, so a failed append leaves the
// status untouched. The result wraps ErrMaxTurns and any persistence failure.
func failExecMaxTurns(ctx context.Context, w EventWriter, st SessionRepo, sessionID string, turns int64) error {
	cause := fmt.Errorf("%w: the model still requested tools after %d turns", ErrMaxTurns, turns)
	payload := map[string]any{"error": cause.Error(), "reason": CodeMaxTurns, "terminal": true}
	if _, aerr := w.Append(ctx, session.ActorEngine, TypeProviderError, payload); aerr != nil {
		return fmt.Errorf("%w: record max turns: %w", cause, aerr)
	}
	if uerr := st.UpdateSessionStatus(ctx, sessionID, "failed"); uerr != nil {
		return fmt.Errorf("%w: mark session failed: %w", cause, uerr)
	}
	return cause
}

// failExecLimit opens a writer and fails the run at its turn limit.
func failExecLimit(ctx context.Context, cfg *ExecLoopConfig, turns int64) error {
	w, err := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
	if err != nil {
		return fmt.Errorf("%w: record max turns: %w", ErrMaxTurns, err)
	}
	defer func() { _ = w.Close() }()
	return failExecMaxTurns(ctx, w, cfg.Store, cfg.Session.ID, turns)
}

// beginExecRequest reserves the pool unit of one provider request and checks the lease is still held. A
// pool that cannot cover it ends the run budget_exhausted.
func beginExecRequest(ctx context.Context, cfg *ExecLoopConfig, res *ExecLoopResult) error {
	stop, serr := reserveExecBudget(ctx, cfg)
	if serr != nil {
		return serr
	}
	if stop {
		res.BudgetExhausted = true
		return ErrExecLoopBudget
	}
	if err := fenceExecLease(ctx, cfg); err != nil {
		return fmt.Errorf("%w: %v", ErrExecLoopFenced, err)
	}
	return nil
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

// runExecTurn runs one provider request and records its events. With recoverable set, a context
// overflow is not a failure yet: it writes the non-terminal provider.error marker (ADR-010 D10)
// and returns an *execOverflow for the loop to compact and retry; otherwise it is a terminal
// provider failure like any other.
func runExecTurn(ctx context.Context, cfg ExecLoopConfig, definitions []provider.ToolDefinition, history []provider.Message,
	recoverable bool) (execTurn, error) {
	w, err := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
	if err != nil {
		return execTurn{}, err
	}
	defer func() { _ = w.Close() }()
	var turn execTurn
	req := &provider.ChatRequest{Tools: definitions, Messages: withSystemPrompt(cfg.SystemPrompt, history), Model: cfg.Session.ModelID,
		MaxTokens: ResolveMaxOutput(cfg.Session.Provider, cfg.Session.ModelID, cfg.MaxTokens)}
	for ev := range cfg.Streamer.Stream(ctx, req) {
		switch {
		case ev.Err != nil && recoverable && errors.Is(ev.Err, provider.ErrContextOverflow):
			return execTurn{}, markExecOverflow(ctx, w, ev.Err)
		case ev.Err != nil:
			return execTurn{}, failExecTurn(ctx, w, cfg.Store, cfg.Session.ID, ev.Err)
		case ev.Kind == provider.StreamEventToolCall && ev.ToolCall != nil:
			turn.Calls = append(turn.Calls, *ev.ToolCall)
		case ev.Kind == provider.StreamEventFinish:
			turn.FinishReason = ev.FinishReason
			turn.Usage = ev.Usage
		case ev.Kind == provider.StreamEventText || ev.Kind == "":
			if _, aerr := w.Append(ctx, session.ActorEngine, "assistant.delta", map[string]any{"delta": ev.Content}); aerr != nil {
				return execTurn{}, aerr
			}
			turn.Content += ev.Content
		}
	}
	if _, aerr := w.Append(ctx, session.ActorEngine, "assistant.message", assistantMessagePayload(turn.Content, turn.Usage)); aerr != nil {
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
		var err error
		if results, err = executeCall(ctx, cfg, call, allowed, results); err != nil {
			return nil, err
		}
	}
	return results, nil
}

// executeCall runs one call under its own per-call writer, closed by defer so a
// panic through a delegating tool releases it before the panicking child's
// settlement, which needs a writer on the same session.
func executeCall(ctx context.Context, cfg ExecLoopConfig, call provider.ToolCall, allowed map[string]bool,
	results []provider.Message) ([]provider.Message, error) {
	w, err := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = w.Close() }()
	return runCall(ctx, cfg, w, call, allowed, results)
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
		var denied *tool.HookDeniedError
		if errors.As(ierr, &denied) {
			return failed(liveDenialText(denied)), nil
		}
		return failed(fmt.Sprintf("error: %s", ierr)), nil
	}
	encoded, merr := json.Marshal(outcome)
	if merr != nil {
		return failed("error: unserialisable result"), nil
	}
	return failed(string(encoded)), nil
}

// liveDenialText is the tool message of a hook denial: the recorded tool.result payload rendered by the one
// function the rebuild uses, so the live and the rebuilt message cannot differ (ADR-012 D10).
func liveDenialText(denied *tool.HookDeniedError) string {
	payload, err := json.Marshal(map[string]any{"ok": false,
		"error": map[string]any{"code": tool.HookDeniedCode, "message": denied.Error()}})
	if err != nil { // unreachable: strings only
		panic(err)
	}
	text, _ := hookDenialText(string(payload))
	return text
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
	rebuilt, berr := rebuildCompacted(events)
	if berr != nil {
		return berr
	}
	*history = append(*history, rebuilt...)
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
	case cfg.Lease == nil:
		return fmt.Errorf("%w: a held lease handle is required", ErrExecLoopConfig)
	case cfg.Lease.Item() == "":
		return fmt.Errorf("%w: lease handle has no item", ErrExecLoopConfig)
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
		gated, gerr := tool.NewLoop(cfg.Registry, sink, cfg.Allowlist)
		if gerr != nil {
			return nil, gerr
		}
		return gated.WithGate(newHookGate(cfg, call, w)), nil
	}()
	if lerr != nil {
		return nil, lerr
	}
	return appendResult(ctx, loop, call, results)
}
