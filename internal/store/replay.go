package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// RecordedEvent is an event row read back from the store. PayloadJSON
// is the stored canonical bytes; the digest is re-verified on read.
type RecordedEvent struct {
	SessionID   string
	Seq         int64
	CreatedAt   time.Time
	Actor       string
	Type        string
	PayloadSHA  string
	PayloadJSON string
}

// Replay reads the full event log for a session in seq order, asserts
// seq contiguity from 0 (DATA-MODEL §3) and verifies
// sha256(payload_json) == payload_sha256 for every row (VA-E1). A gap
// yields ErrSeqGap; a digest mismatch yields ErrHashMismatch.
func (s *Store) Replay(ctx context.Context, sessionID string) ([]RecordedEvent, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT session_id, seq, created_at_utc, actor, type, payload_sha256, payload_json
		 FROM events WHERE session_id=? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("replay query: %w", err)
	}
	defer rows.Close()
	var out []RecordedEvent
	for rows.Next() {
		var e RecordedEvent
		var created string
		if err := rows.Scan(&e.SessionID, &e.Seq, &created, &e.Actor, &e.Type, &e.PayloadSHA, &e.PayloadJSON); err != nil {
			return nil, fmt.Errorf("replay scan: %w", err)
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if e.Seq != int64(len(out)) {
			return nil, fmt.Errorf("%w: session %s expected seq %d got %d", ErrSeqGap, sessionID, len(out), e.Seq)
		}
		got := sha256.Sum256([]byte(e.PayloadJSON))
		if hex.EncodeToString(got[:]) != e.PayloadSHA {
			return nil, fmt.Errorf("%w: session %s seq %d", ErrHashMismatch, sessionID, e.Seq)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ExportJSONL renders the verified event log of a session as JSON Lines
// — one canonical object per line containing session_id, seq,
// created_at_utc, actor, type, payload_sha256 and payload (the parsed
// canonical payload). JSONL is a derived export only, never the source
// of truth (DATA-MODEL §5); the returned bytes are re-verified via
// Replay before export. Line separator is '\n' with a trailing newline
// after the last line.
func (s *Store) ExportJSONL(ctx context.Context, sessionID string) ([]byte, error) {
	events, err := s.Replay(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	for _, e := range events {
		fmt.Fprintf(&buf, "{\"actor\":%q,\"created_at_utc\":%q,\"payload\":%s,\"payload_sha256\":%q,\"seq\":%d,\"session_id\":%q,\"type\":%q}\n",
			e.Actor, iso(e.CreatedAt), e.PayloadJSON, e.PayloadSHA, e.Seq, e.SessionID, e.Type)
	}
	return buf.Bytes(), nil
}

// updateSessionStatus is a minimal status transition helper for event
// semantics; full terminal semantics is a later packet.
func (s *Store) updateSessionStatus(ctx context.Context, sessionID, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET status=?, updated_at_utc=? WHERE id=?`,
		status, iso(time.Now().UTC()), sessionID)
	if err != nil {
		return fmt.Errorf("set status %s: %w", status, err)
	}
	return nil
}

// PauseBudget records a budget pause: it atomically appends a
// `session.paused` event via the session's writer and sets
// sessions.status='paused'. The payload must describe reason, consumed
// budget, next safe operation and any uncertain tool effect
// (DATA-MODEL §7). No terminal turn.end is emitted.
func (w *EventWriter) PauseBudget(ctx context.Context, payload any) error {
	seq, err := w.Append(ctx, "engine", "session.paused", payload)
	if err != nil {
		return err
	}
	if err := w.store.updateSessionStatus(ctx, w.session, "paused"); err != nil {
		return fmt.Errorf("pause after event seq %d: %w", seq, err)
	}
	return nil
}

// Resume records a `session.resumed` event via the session's writer and
// sets sessions.status='active'. Callers must ensure an explicit fresh
// bounded run owns the session; this helper only performs the
// event-level transition.
func (w *EventWriter) Resume(ctx context.Context, payload any) error {
	seq, err := w.Append(ctx, "engine", "session.resumed", payload)
	if err != nil {
		return err
	}
	if err := w.store.updateSessionStatus(ctx, w.session, "active"); err != nil {
		return fmt.Errorf("resume after event seq %d: %w", seq, err)
	}
	return nil
}

// SessionExists reports whether a session row with the given id exists.
func (s *Store) SessionExists(ctx context.Context, sessionID string) (bool, error) {
	if err := s.checkOpen(); err != nil {
		return false, err
	}
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE id=?`, sessionID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
