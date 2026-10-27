package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Session mirrors the sessions table row.
type Session struct {
	ID        string
	Status    string
	Provider  string
	ModelID   string
	Harness   string
	ConfigSHA string
	CreatedAt string
	UpdatedAt string
}

// EventIn is an inbound event awaiting seq assignment by the writer.
type EventIn struct {
	Actor   string
	Type    string
	Payload map[string]any
}

// Event is a persisted event row.
type Event struct {
	SessionID     string
	Seq           int
	CreatedAtUTC  string
	Actor         string
	Type          string
	PayloadSHA256 string
	PayloadJSON   string
}

// ErrTerminal is returned by the writer when the session has reached a
// terminal state (cancelled/completed/failed) and effects are rejected.
var ErrTerminal = errors.New("storage: session is terminal; late effects rejected")

// PauseInfo describes why a session paused (budget exhaustion) and what
// uncertain tool effect, if any, is outstanding at the pause boundary.
type PauseInfo struct {
	Reason         string
	ConsumedBudget int64
	NextSafeOp     string
	UncertainTool  string
}

// EventWriter is the single serialized event-writer goroutine per session.
// All events inserts and session state transitions for the session happen
// through it, each inside one transaction (DATA-MODEL §5, §7).
type EventWriter struct {
	db      *sql.DB
	session Session

	mu       sync.Mutex // serializes writers; goroutine ownership is enforced by design
	seq      int
	terminal bool
	paused   bool
}

// NewEventWriter creates the session (status=active) and returns its writer.
func NewEventWriter(db *sql.DB, s Session) (*EventWriter, Session, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	s.ID = uuid.NewString()
	s.Status = "active"
	s.CreatedAt, s.UpdatedAt = now, now
	if s.ConfigSHA == "" {
		s.ConfigSHA = "unset:" + s.Provider + ":" + s.ModelID
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, s, err
	}
	_, err = tx.Exec(`INSERT INTO sessions (id, created_at_utc, updated_at_utc, status, provider, model_id, harness, config_sha256)
		VALUES (?,?,?,?,?,?,?,?)`,
		s.ID, s.CreatedAt, s.UpdatedAt, s.Status, s.Provider, s.ModelID, s.Harness, s.ConfigSHA)
	if err != nil {
		tx.Rollback()
		return nil, s, fmt.Errorf("insert session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, s, err
	}
	return &EventWriter{db: db, session: s, seq: -1}, s, nil
}

// AttachWriter re-attaches a writer to an existing (reopened) session,
// resuming its seq counter from the persisted high-water mark.
func AttachWriter(ctx context.Context, db *sql.DB, sessionID string) (*EventWriter, error) {
	var s Session
	var maxSeq sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT id, created_at_utc, updated_at_utc, status, provider, model_id, harness, config_sha256 FROM sessions WHERE id = ?`, sessionID).
		Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt, &s.Status, &s.Provider, &s.ModelID, &s.Harness, &s.ConfigSHA)
	if err != nil {
		return nil, fmt.Errorf("attach writer: %w", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), -1) FROM events WHERE session_id = ?`, sessionID).Scan(&maxSeq); err != nil {
		return nil, err
	}
	w := &EventWriter{db: db, session: s, seq: int(maxSeq.Int64)}
	switch s.Status {
	case "cancelled", "completed", "failed":
		w.terminal = true
	case "paused":
		w.paused = true
	}
	return w, nil
}

// Session returns the writer's session snapshot.
func (w *EventWriter) Session() Session { return w.session }

// Append assigns the next contiguous seq, canonicalizes the payload and
// commits the event (plus updated_at touch) in one transaction. Rejected
// when the session is terminal or paused.
func (w *EventWriter) Append(ctx context.Context, in EventIn) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.terminal {
		return fmt.Errorf("%w: %s", ErrTerminal, w.session.ID)
	}
	if w.paused {
		return errors.New("storage: session paused; explicit resume required before more effects")
	}
	payload, err := CanonicalJSON(in.Payload)
	if err != nil {
		return fmt.Errorf("canonicalize payload: %w", err)
	}
	digest, err := PayloadDigest(payload)
	if err != nil {
		return err
	}
	seq := w.seq + 1
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO events (session_id, seq, created_at_utc, actor, type, payload_sha256, payload_json)
		VALUES (?,?,?,?,?,?,?)`,
		w.session.ID, seq, now, in.Actor, in.Type, digest, payload)
	if err != nil {
		tx.Rollback()
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE sessions SET updated_at_utc = ? WHERE id = ?`, now, w.session.ID)
	if err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	w.seq = seq
	return nil
}

// Cancel commits the cancellation checkpoint event (with reason and last
// committed seq) and the terminal session.cancelled event plus status
// transition atomically. Late effects afterwards are rejected.
func (w *EventWriter) Cancel(ctx context.Context, reason string, lastCommittedSeq int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.terminal {
		return fmt.Errorf("%w: already terminal", ErrTerminal)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	cpSeq := w.seq + 1
	if err := insertEventTx(ctx, tx, w.session.ID, cpSeq, now, "engine", "cancel.checkpoint",
		map[string]any{"reason": reason, "last_committed_seq": lastCommittedSeq}); err != nil {
		return err
	}
	endSeq := cpSeq + 1
	if err := insertEventTx(ctx, tx, w.session.ID, endSeq, now, "user", "session.cancelled",
		map[string]any{"reason": reason, "last_committed_seq": lastCommittedSeq}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status='cancelled', updated_at_utc=? WHERE id=?`, now, w.session.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	w.seq = endSeq
	w.terminal = true
	w.session.Status = "cancelled"
	return nil
}

// Pause atomically commits outstanding complete events (already committed by
// Append), a session.paused event (payload records reason, consumed budget,
// next safe operation and any uncertain tool effect) and status='paused'.
// Pause is nonterminal; no turn.end is emitted.
func (w *EventWriter) Pause(ctx context.Context, pi PauseInfo) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.terminal {
		return fmt.Errorf("%w: cannot pause terminal session", ErrTerminal)
	}
	if pi.NextSafeOp == "" {
		pi.NextSafeOp = "session run <id>"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	payload := map[string]any{
		"reason":          pi.Reason,
		"consumed_budget": pi.ConsumedBudget,
		"next_safe_op":    pi.NextSafeOp,
	}
	if pi.UncertainTool != "" {
		payload["uncertain_tool"] = pi.UncertainTool
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seq := w.seq + 1
	if err := insertEventTx(ctx, tx, w.session.ID, seq, now, "engine", "session.paused", payload); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status='paused', updated_at_utc=? WHERE id=?`, now, w.session.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	w.seq = seq
	w.paused = true
	w.session.Status = "paused"
	return nil
}

// Resume transitions paused→active, appends session.resumed referencing the
// stored checkpoint and any uncertain tool effect. It reports (does not
// repeat) the uncertain effect: returns the number of effects re-executed,
// which is always 0 — repetition requires an explicit safe resolution.
func (w *EventWriter) Resume(ctx context.Context, uncertainTool string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.terminal {
		return 0, fmt.Errorf("%w: cannot resume terminal session", ErrTerminal)
	}
	if !w.paused {
		return 0, errors.New("storage: session not paused")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	payload := map[string]any{"from_status": "paused"}
	if uncertainTool != "" {
		payload["uncertain_tool"] = uncertainTool
		payload["uncertain_resolution"] = "reported; awaiting explicit safe resolution"
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	seq := w.seq + 1
	if err := insertEventTx(ctx, tx, w.session.ID, seq, now, "user", "session.resumed", payload); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status='active', updated_at_utc=? WHERE id=?`, now, w.session.ID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	w.seq = seq
	w.paused = false
	w.session.Status = "active"
	return 0, nil
}

func insertEventTx(ctx context.Context, tx *sql.Tx, sessionID string, seq int, now, actor, typ string, payload map[string]any) error {
	canon, err := CanonicalJSON(payload)
	if err != nil {
		return err
	}
	digest, err := PayloadDigest(canon)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO events (session_id, seq, created_at_utc, actor, type, payload_sha256, payload_json)
		VALUES (?,?,?,?,?,?,?)`, sessionID, seq, now, actor, typ, digest, canon)
	return err
}

// ReadEvents returns all events for a session ordered by seq (concurrent
// readers use this; WAL allows readers concurrent with the writer).
func ReadEvents(ctx context.Context, db *sql.DB, sessionID string) ([]Event, error) {
	rows, err := db.QueryContext(ctx, `SELECT session_id, seq, created_at_utc, actor, type, payload_sha256, payload_json
		FROM events WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evs []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.SessionID, &e.Seq, &e.CreatedAtUTC, &e.Actor, &e.Type, &e.PayloadSHA256, &e.PayloadJSON); err != nil {
			return nil, err
		}
		evs = append(evs, e)
	}
	return evs, rows.Err()
}

// VerifyContiguous asserts seq is 0..max with no gaps (DATA-MODEL §3).
func VerifyContiguous(ctx context.Context, db *sql.DB, sessionID string) error {
	var minSeq, maxSeq, count sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT MIN(seq), MAX(seq), COUNT(*) FROM events WHERE session_id = ?`, sessionID).
		Scan(&minSeq, &maxSeq, &count)
	if err != nil {
		return err
	}
	if !count.Valid || count.Int64 == 0 {
		return nil
	}
	if minSeq.Int64 != 0 {
		return fmt.Errorf("seq does not start at 0 (min=%d)", minSeq.Int64)
	}
	if count.Int64 != maxSeq.Int64+1 {
		return fmt.Errorf("seq not contiguous: count=%d max=%d", count.Int64, maxSeq.Int64)
	}
	return nil
}

// ReplayReport summarizes a verified replay.
type ReplayReport struct {
	SessionID  string
	EventCount int
	MinSeq     int
	MaxSeq     int
}

// Replay is the replay oracle: re-opens the given DB's persisted rows and
// verifies (a) seq contiguity 0..max, (b) sha256(payload_json) ==
// payload_sha256 for every row, (c) canonical payload bytes. Any corruption
// fails the replay.
func Replay(ctx context.Context, db *sql.DB, sessionID string) (*ReplayReport, error) {
	evs, err := ReadEvents(ctx, db, sessionID)
	if err != nil {
		return nil, err
	}
	for i, e := range evs {
		if e.Seq != i {
			return nil, fmt.Errorf("replay: seq %d out of order at position %d", e.Seq, i)
		}
		sum, err := PayloadDigest(e.PayloadJSON)
		if err != nil {
			return nil, err
		}
		if sum != e.PayloadSHA256 {
			return nil, fmt.Errorf("replay: digest mismatch at seq %d", e.Seq)
		}
		if !IsCanonical(e.PayloadJSON) {
			return nil, fmt.Errorf("replay: payload not canonical at seq %d", e.Seq)
		}
	}
	rep := &ReplayReport{SessionID: sessionID, EventCount: len(evs)}
	if len(evs) > 0 {
		rep.MinSeq, rep.MaxSeq = evs[0].Seq, evs[len(evs)-1].Seq
	}
	return rep, nil
}

// RestoreFixtureDigests recomputes payload_sha256 from payload_json for every
// row of the session. Used by the T019 negative-test fixture to reset
// deliberate digest corruption between sub-cases; never used in production
// paths.
func RestoreFixtureDigests(ctx context.Context, db *sql.DB, sessionID string) (int, error) {
	evs, err := ReadEvents(ctx, db, sessionID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range evs {
		sum, err := PayloadDigest(e.PayloadJSON)
		if err != nil {
			return n, err
		}
		if sum == e.PayloadSHA256 {
			continue
		}
		if _, err := db.ExecContext(ctx, `UPDATE events SET payload_sha256=? WHERE session_id=? AND seq=?`, sum, sessionID, e.Seq); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
