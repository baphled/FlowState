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

// Typed errors for the M2 provider-driven execution loop. The loop
// fails closed: configuration, lease and budget failures stop the
// run before unbounded work.
var (
	// ErrExecLoopConfig rejects a loop built with missing
	// collaborators or non-positive limits.
	ErrExecLoopConfig = errors.New("app: execution loop misconfigured")

	// ErrExecLoopBudget reports that the budget pool was exhausted
	// before the next provider turn could be reserved.
	ErrExecLoopBudget = errors.New("app: execution loop budget exhausted")

	// ErrExecLoopFenced reports that the loop's lease was fenced or
	// held by another writer; the loop stops without further appends.
	ErrExecLoopFenced = errors.New("app: execution loop lease fenced")
)

// execTurnBudgetFloor is the minimum sane reservation unit for one turn.
const execTurnBudgetFloor int64 = 1

// RunExecLoop executes the bounded provider-driven loop: it acquires
// the fencing lease, advertises the allowlisted definitions and runs
// at most MaxTurns provider turns. A turn ending with tool
// calls executes each through the real loop and continues with the
// correlated results; a turn without calls ends the run. Budget
// exhaustion stops with ErrExecLoopBudget; a fenced or held lease
// stops with ErrExecLoopFenced.
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

// acquireExecLease acquires and fences the run lease.
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

// runExecTurns drives the bounded turn loop to a safe stop.
func runExecTurns(ctx context.Context, cfg *ExecLoopConfig, token int64) (*ExecLoopResult, error) {
	definitions, err := SessionToolDefinitions(cfg.Registry, cfg.Allowlist)
	if err != nil {
		return nil, err
	}
	res := &ExecLoopResult{}
	var history []provider.Message
	if err := replayInto(ctx, cfg.Store, cfg.Session.ID, &history); err != nil {
		return nil, err
	}
	for res.Turns < cfg.MaxTurns {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if stop, serr := reserveExecBudget(ctx, cfg); stop || serr != nil {
			if serr != nil {
				return res, serr
			}
			res.BudgetExhausted = true
			return res, ErrExecLoopBudget
		}
		if err := cfg.Store.FenceLease(ctx, cfg.LeaseItem, cfg.LeaseAgent, token); err != nil {
			return res, fmt.Errorf("%w: %v", ErrExecLoopFenced, err)
		}
		out, calls, terr := runExecTurn(ctx, *cfg, definitions, history)
		res.Turns++
		res.FinalContent = out
		if terr != nil {
			return res, terr
		}
		history = append(history, provider.Message{Role: "assistant", Content: out, ToolCalls: calls})
		if len(calls) == 0 {
			return res, nil
		}
		results, rerr := executeCalls(ctx, *cfg, calls)
		res.ToolCalls += int64(len(calls))
		if rerr != nil {
			return res, rerr
		}
		history = append(history, results...)
	}
	return res, nil
}

// reserveExecBudget reserves one turn; stop reports exhaustion.
func reserveExecBudget(ctx context.Context, cfg *ExecLoopConfig) (stop bool, err error) {
	if cfg.Pool == nil || cfg.TurnBudget <= 0 {
		return false, nil
	}
	if cfg.Pool.Reserve(cfg.TurnBudget) == cfg.TurnBudget {
		return false, nil
	}
	if serr := stopBudgetExhausted(ctx, cfg.Store, cfg.Session.ID); serr != nil {
		return true, serr
	}
	return true, nil
}

// runExecTurn streams one provider turn through a fresh single-use
// writer; provider errors mark the session failed and wrap
// ErrProvider.
func runExecTurn(ctx context.Context, cfg ExecLoopConfig, definitions []provider.ToolDefinition, history []provider.Message) (string, []provider.ToolCall, error) {
	w, err := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = w.Close() }()
	var content string
	var calls []provider.ToolCall
	for ev := range cfg.Streamer.Stream(ctx, &provider.ChatRequest{
		Tools:    definitions,
		Messages: history,
		Model:    cfg.Session.ModelID,
	}) {
		switch {
		case ev.Err != nil:
			_, _ = w.Append(ctx, session.ActorEngine, "provider.error", map[string]any{"error": ev.Err.Error(), "terminal": true})
			_ = cfg.Store.UpdateSessionStatus(ctx, cfg.Session.ID, "failed")
			return "", nil, fmt.Errorf("%w: %w", ErrProvider, ev.Err)
		case ev.Kind == provider.StreamEventToolCall && ev.ToolCall != nil:
			calls = append(calls, *ev.ToolCall)
		case ev.Kind == provider.StreamEventText || ev.Kind == "":
			if _, aerr := w.Append(ctx, session.ActorEngine, "assistant.delta", map[string]any{"delta": ev.Content}); aerr != nil {
				return "", nil, aerr
			}
			content += ev.Content
		}
	}
	if _, aerr := w.Append(ctx, session.ActorEngine, "assistant.message", map[string]any{"content": content}); aerr != nil {
		return "", nil, aerr
	}
	return content, calls, nil
}

func allowSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

// executeCalls runs each provider tool call through the real
// tool.Loop and returns the tool-role results for the next turn;
// denied or failed calls produce explicit error feedback.
func executeCalls(ctx context.Context, cfg ExecLoopConfig, calls []provider.ToolCall) ([]provider.Message, error) {
	results := make([]provider.Message, 0, len(calls))
	allowed := allowSet(cfg.Allowlist)
	for _, call := range calls {
		w, err := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
		if err != nil {
			return nil, err
		}
		// A denied call never emits tool.call, so its result-only
		// feedback goes through the plain writer; an allowlisted call
		// is correlated through the per-call adapter.
		var sink tool.EventSink = w
		if allowed[call.Name] {
			correlated, serr := NewCallEventSink(w, call)
			if serr != nil {
				_ = w.Close()
				return nil, serr
			}
			sink = correlated
		}
		loop, lerr := tool.NewLoop(cfg.Registry, sink, cfg.Allowlist)
		if lerr != nil {
			_ = w.Close()
			return nil, lerr
		}
		results = appendResult(ctx, loop, call, results)
		if err := w.Close(); err != nil {
			return nil, err
		}
	}
	return results, nil
}

// appendResult invokes one call and appends its outcome as a
// tool-role message.
func appendResult(ctx context.Context, loop *tool.Loop, call provider.ToolCall, results []provider.Message) []provider.Message {
	outcome, ierr := loop.Invoke(ctx, call.Name, call.Arguments)
	if ierr != nil {
		return append(results, provider.Message{
			Role: "tool", ToolCallID: call.ID,
			Content: fmt.Sprintf("error: %s", ierr),
		})
	}
	encoded, merr := json.Marshal(outcome)
	if merr != nil {
		return append(results, provider.Message{
			Role: "tool", ToolCallID: call.ID, Content: "error: unserialisable result",
		})
	}
	return append(results, provider.Message{Role: "tool", ToolCallID: call.ID, Content: string(encoded)})
}

// replayInto loads the verified session log into provider history,
// reconstructing correlated tool turns from persisted events.
func replayInto(ctx context.Context, st *store.Store, sessionID string, history *[]provider.Message) error {
	events, err := st.Replay(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, e := range events {
		switch e.Type {
		case session.TypeTurnStart:
			*history = append(*history, provider.Message{Role: "user", Content: payloadTaskText(e.PayloadJSON)})
		case tool.TypeToolCall:
			*history = append(*history, provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{
				ID:        payloadFieldText(e.PayloadJSON, "tool_call_id"),
				Name:      payloadFieldText(e.PayloadJSON, "tool"),
				Arguments: json.RawMessage(payloadFieldRaw(e.PayloadJSON, "args")),
			}}})
		case tool.TypeToolResult:
			*history = append(*history, provider.Message{Role: "tool", ToolCallID: payloadFieldText(e.PayloadJSON, "tool_call_id"), Content: payloadFieldRaw(e.PayloadJSON, "result")})
		}
	}
	return nil
}

// stopBudgetExhausted appends a turn.end budget-stopped event. The
// sessions table has no budget_exhausted status yet; adding one is
// store-schema debt logged in the packet admission.
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

// validateExecLoopConfig fails closed on invalid configuration.
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

func payloadTaskText(payloadJSON string) string {
	return payloadFieldText(payloadJSON, "task")
}

func payloadFieldText(payloadJSON, field string) string {
	var p map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
		return ""
	}
	text, _ := p[field].(string)
	return text
}

func payloadFieldRaw(payloadJSON, field string) string {
	var p map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
		return ""
	}
	encoded, err := json.Marshal(p[field])
	if err != nil {
		return ""
	}
	return string(encoded)
}
