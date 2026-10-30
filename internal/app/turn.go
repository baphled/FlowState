package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/runtime"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// payloadTask extracts the task text from a stored turn.start
// payload without failing on unknown shapes.
func payloadTask(payloadJSON string) string {
	var p struct {
		Task string `json:"task"`
	}
	_ = json.Unmarshal([]byte(payloadJSON), &p)
	return p.Task
}

// Turn runs one provider turn for sess: it appends the user message
// event, streams the provider response appending one delta event per
// chunk, then the final assistant event and turn.end, and marks the
// session completed. A provider error appends a terminal typed
// provider.error event, marks the session failed and returns the
// error; there are no retries (M1-PROVIDER failure path).
func Turn(ctx context.Context, st *store.Store, sess *store.Session, p provider.Streamer) (*runtime.TurnResult, error) {
	return TurnWithOptions(ctx, st, sess, p, runtime.TurnOptions{})
}

// TurnWithOptions runs one provider turn like Turn, honouring opts.
// When opts.Budget > 0 and the accumulated streamed delta content
// length reaches the budget, the turn stops consuming the stream
// without appending assistant.message or turn.end, pauses the session
// via the store writer (session.paused with reason "budget_exceeded"
// and status paused) and returns a TurnResult with BudgetExceeded set
// and a nil error.
func TurnWithOptions(ctx context.Context, st *store.Store, sess *store.Session, p provider.Streamer, opts runtime.TurnOptions) (*runtime.TurnResult, error) {
	if sess == nil {
		return nil, fmt.Errorf("app: session must not be nil")
	}
	if p == nil {
		return nil, fmt.Errorf("app: provider must not be nil")
	}
	events, err := st.Replay(ctx, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("load session log: %w", err)
	}
	var task string
	for _, e := range events {
		if e.Type == session.TypeTurnStart {
			task = payloadTask(e.PayloadJSON)
			break
		}
	}
	w, err := st.NewEventWriter(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	if _, err := w.Append(ctx, session.ActorUser, runtime.TypeUserMessage, map[string]any{"content": task}); err != nil {
		return nil, err
	}
	var content string
	var consumed int64
	streamErr := false
	budgetExceeded := false
	for ev := range p.Stream(ctx, &provider.ChatRequest{
		Messages: []provider.Message{{Role: "user", Content: task}},
		Model:    sess.ModelID,
	}) {
		if ev.Err != nil {
			if _, aerr := w.Append(ctx, session.ActorEngine, runtime.TypeProviderError, map[string]any{
				"error":    ev.Err.Error(),
				"terminal": true,
			}); aerr != nil {
				return nil, aerr
			}
			if uerr := st.UpdateSessionStatus(ctx, sess.ID, "failed"); uerr != nil {
				return nil, fmt.Errorf("mark session failed: %w", uerr)
			}
			streamErr = true
			content = ""
			err = ev.Err
			break
		}
		if _, derr := w.Append(ctx, session.ActorEngine, runtime.TypeAssistantDelta, map[string]any{"delta": ev.Content}); derr != nil {
			return nil, derr
		}
		content += ev.Content
		consumed += int64(len(ev.Content))
		if opts.Budget > 0 && consumed >= opts.Budget {
			budgetExceeded = true
			break
		}
	}
	if budgetExceeded {
		if perr := w.Pause(ctx, store.PauseInfo{
			Reason:         "budget_exceeded",
			ConsumedBudget: consumed,
			NextSafeOp:     "session run " + sess.ID,
		}); perr != nil {
			return nil, perr
		}
		return &runtime.TurnResult{SessionID: sess.ID, FinalContent: content, BudgetExceeded: true, ConsumedBudget: consumed}, nil
	}
	if streamErr {
		return nil, fmt.Errorf("%w: %w", runtime.ErrProvider, err)
	}
	if _, err := w.Append(ctx, session.ActorEngine, runtime.TypeAssistantMessage, map[string]any{"content": content}); err != nil {
		return nil, err
	}
	if _, err := w.Append(ctx, session.ActorEngine, runtime.TypeTurnEnd, map[string]any{"status": "completed"}); err != nil {
		return nil, err
	}
	if err := st.UpdateSessionStatus(ctx, sess.ID, "completed"); err != nil {
		return nil, fmt.Errorf("mark session completed: %w", err)
	}
	return &runtime.TurnResult{SessionID: sess.ID, FinalContent: content}, nil
}
