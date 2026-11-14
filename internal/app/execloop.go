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

var (
	// ErrExecLoopConfig rejects a loop built with missing collaborators or non-positive limits.
	ErrExecLoopConfig = errors.New("app: execution loop misconfigured")
	// ErrExecLoopBudget reports that the budget pool was exhausted before the next turn.
	ErrExecLoopBudget = errors.New("app: execution loop budget exhausted")
	// ErrExecLoopFenced reports that the loop's lease was fenced or held by another writer.
	ErrExecLoopFenced = errors.New("app: execution loop lease fenced")
)

const execTurnBudgetFloor int64 = 1

// RunExecLoop executes the bounded provider-driven loop: a fencing lease, allowlisted
// definitions and at most MaxTurns streaming turns through the real tool loop.
func RunExecLoop(ctx context.Context, cfg ExecLoopConfig) (*ExecLoopResult, error) {
	if verr := validateExecLoopConfig(&cfg); verr != nil {
		return nil, verr
	}
	token, lerr := acquireExecLease(ctx, &cfg)
	if lerr != nil {
		return nil, lerr
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
	if ferr := cfg.Store.FenceLease(ctx, cfg.LeaseItem, cfg.LeaseAgent, token); ferr != nil {
		return 0, fmt.Errorf("%w: %v", ErrExecLoopFenced, ferr)
	}
	return token, nil
}

func runExecTurns(ctx context.Context, cfg *ExecLoopConfig, token int64) (*ExecLoopResult, error) {
	definitions, derr := SessionToolDefinitions(cfg.Registry, cfg.Allowlist)
	if derr != nil {
		return nil, derr
	}
	res, history := &ExecLoopResult{}, []provider.Message{}
	if rerr := replayInto(ctx, cfg.Store, cfg.Session.ID, &history); rerr != nil {
		return nil, rerr
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
		out, calls, terr := runExecTurn(ctx, *cfg, token, definitions, history)
		res.Turns++
		res.FinalContent = out
		if terr != nil {
			return res, terr
		}
		history = append(history, provider.Message{Role: "assistant", Content: out, ToolCalls: calls})
		if len(calls) == 0 {
			return res, nil
		}
		results, rerr := executeCalls(ctx, *cfg, token, calls)
		res.ToolCalls += int64(len(calls))
		if rerr != nil {
			return res, rerr
		}
		history = append(history, results...)
	}
	return res, nil
}

func reserveExecBudget(ctx context.Context, cfg *ExecLoopConfig) (bool, error) {
	if cfg.Pool == nil || cfg.TurnBudget <= 0 || cfg.Pool.Reserve(cfg.TurnBudget) == cfg.TurnBudget {
		return false, nil
	}
	return true, stopBudgetExhausted(ctx, cfg.Store, cfg.Session.ID)
}

// runExecTurn streams one provider turn through a fresh single-use writer bound to the
// fencing lease (cancelled contexts abort so partial output is never persisted).
func runExecTurn(ctx context.Context, cfg ExecLoopConfig, token int64, definitions []provider.ToolDefinition, history []provider.Message) (string, []provider.ToolCall, error) {
	w, werr := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
	if werr != nil {
		return "", nil, werr
	}
	if err := cfg.Store.FenceLease(ctx, cfg.LeaseItem, cfg.LeaseAgent, token); err != nil {
		_ = w.Close()
		return "", nil, fmt.Errorf("%w: %v", ErrExecLoopFenced, err)
	}
	defer func() { _ = w.Close() }()
	var content string
	var calls []provider.ToolCall
	req := &provider.ChatRequest{Tools: definitions, Messages: history, Model: cfg.Session.ModelID}
	for ev := range cfg.Streamer.Stream(ctx, req) {
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
	if cerr := ctx.Err(); cerr != nil {
		return "", nil, cerr
	}
	if _, aerr := w.Append(ctx, session.ActorEngine, "assistant.message", map[string]any{"content": content}); aerr != nil {
		return "", nil, aerr
	}
	return content, calls, nil
}

func executeCalls(ctx context.Context, cfg ExecLoopConfig, token int64, calls []provider.ToolCall) ([]provider.Message, error) {
	results := make([]provider.Message, 0, len(calls))
	for _, call := range calls {
		w, werr := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
		if werr != nil {
			return nil, werr
		}
		if err := cfg.Store.FenceLease(ctx, cfg.LeaseItem, cfg.LeaseAgent, token); err != nil {
			_ = w.Close()
			return nil, fmt.Errorf("%w: %v", ErrExecLoopFenced, err)
		}
		sink, serr := execCallSink(w, call)
		if serr != nil {
			_ = w.Close()
			return nil, serr
		}
		loop, lerr := tool.NewLoop(cfg.Registry, sink, cfg.Allowlist)
		if lerr != nil {
			_ = w.Close()
			return nil, lerr
		}
		var fatal error
		results, fatal = appendResult(ctx, loop, call, results)
		if fatal != nil {
			_ = w.Close()
			return nil, fatal
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
	}
	return results, nil
}

// appendResult appends one correlated tool-role message: tool-execution failures become
// feedback text, loop-level terminal failures are returned as fatal turn-terminal errors.
func appendResult(ctx context.Context, loop *tool.Loop, call provider.ToolCall, results []provider.Message) ([]provider.Message, error) {
	failed := func(content string) []provider.Message {
		return append(results, provider.Message{Role: "tool", ToolCallID: call.ID, Content: content})
	}
	outcome, ierr := loop.Invoke(ctx, call.Name, call.Arguments)
	switch {
	case ierr == nil:
	case errors.Is(ierr, tool.ErrUnknownTool), errors.Is(ierr, tool.ErrEmptyName):
		// Loop-level misconfiguration: terminal, not tool feedback.
		return results, fmt.Errorf("%w: %s: %v", ErrExecLoopConfig, call.Name, ierr)
	default:
		if !errors.Is(ierr, tool.ErrDenied) && !errors.Is(ierr, tool.ErrToolExecuted) {
			return results, ierr
		}
	}
	if outcome == nil && ierr != nil {
		return failed(fmt.Sprintf("error: %s", ierr)), nil
	}
	encoded, merr := json.Marshal(outcome)
	if merr != nil {
		return failed("error: unserialisable result"), nil
	}
	return failed(string(encoded)), nil
}

// replayInto rebuilds provider history from persisted events; uncorrelated tool.results
// (legacy denials) are skipped so no orphan tool-role message reaches the provider.
func replayInto(ctx context.Context, st *store.Store, sessionID string, history *[]provider.Message) error {
	events, rerr := st.Replay(ctx, sessionID)
	if rerr != nil {
		return rerr
	}
	correlated := map[string]bool{}
	// pending carries the last replayed assistant.message for the next tool-call message.
	pending := ""
	flush := func(calls []provider.ToolCall) {
		*history = append(*history, provider.Message{Role: "assistant", Content: pending, ToolCalls: calls})
		pending = ""
	}
	for _, e := range events {
		switch e.Type {
		case session.TypeTurnStart:
			*history = append(*history, provider.Message{Role: "user", Content: payloadFieldText(e.PayloadJSON, "task")})
		case "assistant.message":
			pending = payloadFieldText(e.PayloadJSON, "content")
		case tool.TypeToolCall:
			correlated[payloadFieldText(e.PayloadJSON, "tool_call_id")] = true
			flush([]provider.ToolCall{{
				ID:        payloadFieldText(e.PayloadJSON, "tool_call_id"),
				Name:      payloadFieldText(e.PayloadJSON, "tool"),
				Arguments: json.RawMessage(payloadFieldRaw(e.PayloadJSON, "args")),
			}})
		case tool.TypeToolResult:
			if payloadFieldText(e.PayloadJSON, "tool_call_id") == "" {
				continue
			}
			if id := payloadFieldText(e.PayloadJSON, "tool_call_id"); !correlated[id] {
				correlated[id] = true
				*history = append(*history, provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{
					ID: id, Name: payloadFieldText(e.PayloadJSON, "tool"), Arguments: json.RawMessage("null"),
				}}})
			}
			content := payloadFieldRaw(e.PayloadJSON, "result")
			if content == "null" {
				content = payloadFieldRaw(e.PayloadJSON, "error")
			}
			*history = append(*history, provider.Message{Role: "tool", ToolCallID: payloadFieldText(e.PayloadJSON, "tool_call_id"), Content: content})
		}
	}
	if pending != "" {
		*history = append(*history, provider.Message{Role: "assistant", Content: pending})
	}
	return nil
}

func stopBudgetExhausted(ctx context.Context, st *store.Store, sessionID string) error {
	w, werr := st.NewEventWriter(ctx, sessionID)
	if werr != nil {
		return werr
	}
	defer func() { _ = w.Close() }()
	if _, aerr := w.Append(ctx, session.ActorEngine, "turn.end", map[string]any{"status": "budget_exhausted"}); aerr != nil {
		return fmt.Errorf("record budget stop: %w", aerr)
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
	text, _ := payloadMap(payloadJSON)[field].(string)
	return text
}

func payloadFieldRaw(payloadJSON, field string) string {
	encoded, err := json.Marshal(payloadMap(payloadJSON)[field])
	if err != nil {
		return ""
	}
	return string(encoded)
}

func execCallSink(w tool.EventSink, call provider.ToolCall) (tool.EventSink, error) {
	return NewCallEventSink(w, call)
}
