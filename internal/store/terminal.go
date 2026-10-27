package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrSessionTerminal is returned by terminal transitions when the
// session is already in a terminal state (completed, cancelled or
// failed). Terminal states are immutable: DATA-MODEL §7 rejects
// further run/state transitions on them.
var ErrSessionTerminal = errors.New("store: session is terminal")

// TerminalEvent is the event payload recorded alongside a terminal
// status transition.
type TerminalEvent struct {
	Actor    string
	Type     string
	Payload  any
	NewState string
}

// terminalStatuses mirrors the session package's terminal set; the
// store owns enforcement so the guarantee holds regardless of caller.
var storeTerminalStatuses = map[string]bool{
	"completed": true, "cancelled": true, "failed": true,
}

// transitionTerminal moves a non-terminal session into the terminal
// state newState and appends exactly one terminal event, in a single
// serialized transaction (DATA-MODEL §7: state change and event share
// the same single-writer transaction). The status guard makes the
// transition idempotent-unsafe by design: a session already in any
// terminal state yields ErrSessionTerminal without appending, so
// concurrent cancels can commit at most one terminal event. The
// event seq is MAX(seq)+1 computed inside the same transaction,
// keeping the log contiguous.
func (s *Store) transitionTerminal(ctx context.Context, sessionID string, ev TerminalEvent) (*Session, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin terminal transition: %w", err)
	}
	defer func() {
		if rb := tx.Rollback(); rb != nil && !errors.Is(rb, sql.ErrTxDone) {
			// Nothing to surface: the primary error governs.
			_ = rb
		}
	}()
	var sess Session
	var created, updated, status string
	err = tx.QueryRowContext(ctx,
		`SELECT id, created_at_utc, updated_at_utc, status, provider, model_id, harness, config_sha256 FROM sessions WHERE id=?`,
		sessionID).Scan(&sess.ID, &created, &updated, &status, &sess.Provider, &sess.ModelID, &sess.Harness, &sess.ConfigSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSession, sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	if storeTerminalStatuses[status] {
		return nil, fmt.Errorf("%w: session %s is %s", ErrSessionTerminal, sessionID, status)
	}
	payloadJSON, err := json.Marshal(ev.Payload)
	if err != nil {
		return nil, fmt.Errorf("canonicalize terminal payload: %w", err)
	}
	digest := sha256.Sum256(payloadJSON)
	var maxSeq sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(seq) FROM events WHERE session_id=?`, sessionID).Scan(&maxSeq); err != nil {
		return nil, fmt.Errorf("load max seq: %w", err)
	}
	next := int64(0)
	if maxSeq.Valid {
		next = maxSeq.Int64 + 1
	}
	nowISO := iso(time.Now().UTC())
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO events (session_id, seq, created_at_utc, actor, type, payload_sha256, payload_json)
		 VALUES (?,?,?,?,?,?,?)`,
		sessionID, next, nowISO, ev.Actor, ev.Type, hex.EncodeToString(digest[:]), string(payloadJSON)); err != nil {
		return nil, fmt.Errorf("append terminal event seq %d: %w", next, err)
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE sessions SET status=?, updated_at_utc=? WHERE id=? AND status=?`,
		ev.NewState, nowISO, sessionID, status)
	if err != nil {
		return nil, fmt.Errorf("set status %s: %w", ev.NewState, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if n != 1 {
		// The guarded status changed under us (or the row vanished);
		// no terminal event is committed because the tx rolls back.
		return nil, fmt.Errorf("%w: session %s", ErrSessionTerminal, sessionID)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit terminal transition: %w", err)
	}
	sess.Status = ev.NewState
	sess.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339Nano, nowISO)
	return &sess, nil
}

// CancelSession transitions an active or paused session to cancelled
// and records exactly one `session.cancelled` event in the same
// transaction (M1-TERMINAL). Cancelling a terminal session fails with
// ErrSessionTerminal; an unknown id fails with ErrUnknownSession.
// If ctx is already canceled the transition fails without writing.
func (s *Store) CancelSession(ctx context.Context, sessionID string, reason string) (*Session, error) {
	payload := map[string]any{"status": "cancelled"}
	if reason != "" {
		payload["reason"] = reason
	}
	return s.transitionTerminal(ctx, sessionID, TerminalEvent{
		Actor: "engine", Type: "session.cancelled", Payload: payload, NewState: "cancelled",
	})
}

// CompleteSession transitions an active or paused session to
// completed and records exactly one terminal `turn.end` event in the
// same transaction (M1-TERMINAL: terminal exactly once, no zombie
// turns). Completing a terminal session fails with ErrSessionTerminal.
func (s *Store) CompleteSession(ctx context.Context, sessionID string, result any) (*Session, error) {
	payload := map[string]any{"status": "completed", "terminal": true}
	if result != nil {
		payload["result"] = result
	}
	return s.transitionTerminal(ctx, sessionID, TerminalEvent{
		Actor: "engine", Type: "turn.end", Payload: payload, NewState: "completed",
	})
}
