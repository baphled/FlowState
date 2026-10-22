// session_lifetime.go provides the session lifetime bound: per-session
// turn, message, and age ceilings whose exhaustion refuses further
// turns with an honest terminal (persisted assistant message plus an
// error Done chunk), so no session can stream forever.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// ErrSessionLifetimeExceeded is the terminal error surfaced on the wire
// when a session's lifetime budget is spent. The persisted terminal
// assistant message names the specific exhausted budget in its content;
// no new StopReason sentinel is introduced.
var ErrSessionLifetimeExceeded = errors.New("session lifetime budget exceeded")

// engineMaxSessionTurns is the default ceiling on streamed turns per
// session. Forty turns is well beyond an interactive session's useful
// working span while still bounding runaway auto-continuation loops.
const engineMaxSessionTurns = 40

// engineMaxSessionMessages is the default ceiling on persisted messages
// per session. Auto-compaction normally keeps live windows far below
// this; the bound catches unbounded transcript growth for sessions the
// compactor cannot shrink.
const engineMaxSessionMessages = 1000

// engineMaxSessionAge is the default wall-clock ceiling on a session,
// measured from its first streamed turn. A session older than a working
// day has accumulated context no operator reasonably continues by hand.
const engineMaxSessionAge = 24 * time.Hour

// sessionLifetimeEntry tracks one session's lifetime consumption: the
// number of accepted streamed turns and the wall clock of the first.
// Guarded by the engine mutex.
type sessionLifetimeEntry struct {
	turns       int
	firstTurnAt time.Time
}

// resolveMaxSessionTurns returns the configured session turn bound or
// the compiled-in default when zero. Negative disables the bound.
//
// Expected: cfg is a valid Config struct.
// Returns: the resolved bound; zero means disabled.
// Side effects: None.
func resolveMaxSessionTurns(cfg Config) int {
	if cfg.MaxSessionTurns != 0 {
		return cfg.MaxSessionTurns
	}
	return engineMaxSessionTurns
}

// resolveMaxSessionMessages returns the configured session message
// bound or the compiled-in default when zero. Negative disables the
// bound.
//
// Expected: cfg is a valid Config struct.
// Returns: the resolved bound; zero means disabled.
// Side effects: None.
func resolveMaxSessionMessages(cfg Config) int {
	if cfg.MaxSessionMessages != 0 {
		return cfg.MaxSessionMessages
	}
	return engineMaxSessionMessages
}

// resolveMaxSessionAge returns the configured session age bound or the
// compiled-in default when zero. Negative disables the bound.
//
// Expected: cfg is a valid Config struct.
// Returns: the resolved bound; zero means disabled.
// Side effects: None.
func resolveMaxSessionAge(cfg Config) time.Duration {
	if cfg.MaxSessionAge != 0 {
		return cfg.MaxSessionAge
	}
	return engineMaxSessionAge
}

// sessionLifetimeReason reports which lifetime bound the session has
// spent, if any. Whichever bound trips first wins; the returned string
// names it (e.g. "max_session_turns=40") for the honest terminal.
//
// Expected: sessionID identifies the session under test.
// Returns: the exhausted bound description, or empty when the session
// is within every bound (or has never streamed).
// Side effects: None.
func (e *Engine) sessionLifetimeReason(sessionID string) string {
	e.mu.Lock()
	entry, ok := e.sessionLifetime[sessionID]
	e.mu.Unlock()
	if !ok {
		return ""
	}
	if e.maxSessionTurns > 0 && entry.turns >= e.maxSessionTurns {
		return fmt.Sprintf("max_session_turns=%d", e.maxSessionTurns)
	}
	if e.maxSessionAge > 0 && time.Since(entry.firstTurnAt) >= e.maxSessionAge {
		return fmt.Sprintf("max_session_age=%s", e.maxSessionAge)
	}
	if e.maxSessionMessages > 0 && e.store != nil && len(e.store.AllMessages()) >= e.maxSessionMessages {
		return fmt.Sprintf("max_session_messages=%d", e.maxSessionMessages)
	}
	return ""
}

// SessionLifetimeExceeded reports whether the session has spent any of
// its configured lifetime bounds. Consumers that auto-continue sessions
// (the completion orchestrator's re-prompt chain) consult this so a
// spent session is not re-prompted.
//
// Expected: sessionID identifies the session under test.
// Returns: true when a lifetime bound is exhausted.
// Side effects: None.
func (e *Engine) SessionLifetimeExceeded(sessionID string) bool {
	return e.sessionLifetimeReason(sessionID) != ""
}

// recordSessionTurn records one accepted streamed turn against the
// session's lifetime budget, creating the tracker on first use.
//
// Expected: sessionID identifies the session that accepted a turn.
// Returns: None.
// Side effects: increments the turn counter and stamps firstTurnAt once.
func (e *Engine) recordSessionTurn(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	entry, ok := e.sessionLifetime[sessionID]
	if !ok {
		e.sessionLifetime[sessionID] = &sessionLifetimeEntry{turns: 1, firstTurnAt: time.Now()}
		return
	}
	entry.turns++
}

// streamSessionLimitTerminal refuses a turn on a spent session
// honestly: the user message and a terminal assistant message naming
// the exhausted budget are persisted so the transcript explains the
// refusal, and the stream closes with an error Done chunk (the
// ErrCompactionInsufficient precedent). No provider call is made and no
// continuation machinery runs.
//
// Expected: ctx is the caller's context; sessionID identifies the spent
// session; message is the user's turn text; limitReason names the
// exhausted bound.
// Returns: a channel carrying the error terminal.
// Side effects: persists the user message and the terminal assistant
// message when a store is wired.
func (e *Engine) streamSessionLimitTerminal(ctx context.Context, sessionID, message, limitReason string) <-chan provider.StreamChunk {
	slog.Warn("session lifetime budget exhausted, refusing turn",
		"session", sessionID,
		"limit", limitReason,
	)
	if e.store != nil {
		userMsg := provider.Message{Role: "user", Content: message}
		msgID := e.store.AppendReturningID(userMsg)
		e.embedMessage(ctx, message, msgID)
		content := fmt.Sprintf("Session stopped by the engine: session lifetime budget exhausted (%s). Start a new session to continue.", limitReason)
		terminal := provider.Message{Role: "assistant", Content: content, ModelID: e.LastModel()}
		terminalID := e.store.AppendReturningID(terminal)
		e.dualWriteToChainStore(ctx, terminal)
		e.embedMessage(ctx, content, terminalID)
	}
	outChan := make(chan provider.StreamChunk, streamBufferSize)
	emitTerminalStreamChunk(ctx, outChan, provider.StreamChunk{
		Error:      fmt.Errorf("%w: %s", ErrSessionLifetimeExceeded, limitReason),
		Done:       true,
		ModelID:    e.LastModel(),
		ProviderID: e.LastProvider(),
	})
	close(outChan)
	return outChan
}
