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

// ErrExecLoopBudget reports that the budget pool was exhausted before the next turn.
var ErrExecLoopBudget = errors.New("app: execution loop budget exhausted")

// ErrExecLoopFenced reports that the loop's lease was fenced or held by another writer.
var ErrExecLoopFenced = errors.New("app: execution loop lease fenced")

const execTurnBudgetFloor int64 = 1

// RunExecLoop executes the bounded provider-driven loop: it acquires the
// fencing lease, advertises allowlisted definitions and runs at most MaxTurns
// provider turns. A turn ending with tool calls executes each through the
// real loop and continues with correlated results; a turn without calls ends
// the run. Budget exhaustion stops with ErrExecLoopBudget; a fenced or held
// lease stops with ErrExecLoopFenced.
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
		return nil, err
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

func reserveExecBudget(ctx context.Context, cfg *ExecLoopConfig) (stop bool, err error) {
	if cfg.Pool == nil || cfg.TurnBudget <= 0 || cfg.Pool.Reserve(cfg.TurnBudget) == cfg.TurnBudget {
		return false, nil
	}
	return true, stopBudgetExhausted(ctx, cfg.Store, cfg.Session.ID)
}

// runExecTurn streams one provider turn through a fresh single-use writer;
// provider errors mark the session failed and wrap ErrProvider.
func runExecTurn(ctx context.Context, cfg ExecLoopConfig, definitions []provider.ToolDefinition, history []provider.Message) (string, []provider.ToolCall, error) {
	w, err := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
	if err != nil {
		return "", nil, err
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
	if _, aerr := w.Append(ctx, session.ActorEngine, "assistant.message", map[string]any{"content": content}); aerr != nil {
		return "", nil, aerr
	}
	return content, calls, nil
}

// executeCalls runs each provider tool call through the real tool.Loop and
// returns the tool-role results for the next turn; denied or failed calls
// produce explicit error feedback.
func executeCalls(ctx context.Context, cfg ExecLoopConfig, calls []provider.ToolCall) ([]provider.Message, error) {
	results := make([]provider.Message, 0, len(calls))
	for _, call := range calls {
		w, err := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
		if err != nil {
			return nil, err
		}
		// Every call — allowlisted or denied — is annotated with its provider
		// call id so the history projection can correlate the outcome later.
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
		results = appendResult(ctx, loop, call, results)
		if err := w.Close(); err != nil {
			return nil, err
		}
	}
	return results, nil
}

// appendResult invokes one call and appends its outcome as a tool-role message.
func appendResult(ctx context.Context, loop *tool.Loop, call provider.ToolCall, results []provider.Message) []provider.Message {
	failed := func(content string) []provider.Message {
		return append(results, provider.Message{Role: "tool", ToolCallID: call.ID, Content: content})
	}
	outcome, ierr := loop.Invoke(ctx, call.Name, call.Arguments)
	if ierr != nil {
		return failed(fmt.Sprintf("error: %s", ierr))
	}
	encoded, merr := json.Marshal(outcome)
	if merr != nil {
		return failed("error: unserialisable result")
	}
	return failed(string(encoded))
}

// replayInto loads the verified session log into provider history, reconstructing
// correlated tool turns from persisted events. A denied call persists only a
// tool.result error event, so its assistant tool-call message is synthesised
// from the result payload to keep the provider history correlated.
func replayInto(ctx context.Context, st *store.Store, sessionID string, history *[]provider.Message) error {
	events, err := st.Replay(ctx, sessionID)
	if err != nil {
		return err
	}
	correlated := map[string]bool{}
	for _, e := range events {
		switch e.Type {
		case session.TypeTurnStart:
			*history = append(*history, provider.Message{Role: "user", Content: payloadFieldText(e.PayloadJSON, "task")})
		case tool.TypeToolCall:
			correlated[payloadFieldText(e.PayloadJSON, "tool_call_id")] = true
			*history = append(*history, provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{
				ID:        payloadFieldText(e.PayloadJSON, "tool_call_id"),
				Name:      payloadFieldText(e.PayloadJSON, "tool"),
				Arguments: json.RawMessage(payloadFieldRaw(e.PayloadJSON, "args")),
			}}})
		case tool.TypeToolResult:
			if id := payloadFieldText(e.PayloadJSON, "tool_call_id"); id != "" && !correlated[id] {
				correlated[id] = true
				*history = append(*history, provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{
					ID:        id,
					Name:      payloadFieldText(e.PayloadJSON, "tool"),
					Arguments: json.RawMessage("null"),
				}}})
			}
			// Denied feedback carries the error object, not a result field;
			// its serialised payload is the explicit denial feedback text.
			content := payloadFieldRaw(e.PayloadJSON, "result")
			if content == "null" {
				content = payloadFieldRaw(e.PayloadJSON, "error")
			}
			*history = append(*history, provider.Message{Role: "tool", ToolCallID: payloadFieldText(e.PayloadJSON, "tool_call_id"), Content: content})
		}
	}
	return nil
}

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

// payloadMap decodes a recorded event payload or returns nil.
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

// execCallSink wraps the writer in the per-call correlated adapter. Every
// call is correlated, including denials: the loop's denial feedback is a
// result-only outcome that the adapter accepts without a preceding start.
func execCallSink(w tool.EventSink, call provider.ToolCall) (tool.EventSink, error) {
	return NewCallEventSink(w, call)
}
