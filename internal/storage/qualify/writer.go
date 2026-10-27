package qualify

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Event is one attributable session event pending serialization.
type Event struct {
	Actor   string // 'user' | 'engine' | 'tool:<name>' | 'provider:<id>'
	Type    string // e.g. turn.start, provider.delta, tool.call, tool.result, session.cancelled
	Payload any    // encoded with the Canonical encoder
}

// StoredEvent is the row form read back from the events table.
type StoredEvent struct {
	SessionID     string
	Seq           int64
	CreatedAtUTC  string
	Actor         string
	Type          string
	PayloadSHA256 string
	PayloadJSON   string
}

// Writer is the single serialized event-writer goroutine per session. All
// events inserts happen inside its goroutine, one transaction per batch; a
// status transition and its events commit in the same transaction. After a
// terminal commit (cancelled/completed/failed) or explicit Close, Append
// returns ErrWriterClosed.
//
// seq is 0-based contiguous per DATA-MODEL §3 wording "contiguity 0..max";
// this choice is documented here deliberately.
type Writer struct {
	db        *sql.DB
	sessionID string

	closed bool // set after a terminal commit
	submit chan writeReq
	done   chan struct{}
}

type writeReq struct {
	ctx       context.Context
	events    []Event
	newStatus *string // optional same-transaction status transition
	resp      chan writeResp
}

type writeResp struct {
	seqs []int64
	err  error
}

// NewWriter starts the serialized writer goroutine for sessionID. On a
// reopened database it resumes seq numbering after the persisted max.
func NewWriter(db *sql.DB, sessionID string) *Writer {
	w := &Writer{
		db:        db,
		sessionID: sessionID,
		submit:    make(chan writeReq),
		done:      make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *Writer) run() {
	defer close(w.done)
	nextSeq := int64(0)
	var maxSeq sql.NullInt64
	if err := w.db.QueryRow(
		"SELECT COALESCE(MAX(seq), -1) FROM events WHERE session_id = ?", w.sessionID,
	).Scan(&maxSeq); err != nil {
		nextSeq = -1 // poison: every subsequent write fails
	} else if maxSeq.Valid {
		nextSeq = maxSeq.Int64 + 1
	}
	for req := range w.submit {
		if w.closed || nextSeq < 0 {
			req.resp <- writeResp{err: ErrWriterClosed}
			continue
		}
		seqs, err := w.commitBatch(req, &nextSeq)
		if err != nil {
			req.resp <- writeResp{err: err}
			continue
		}
		req.resp <- writeResp{seqs: seqs}
	}
}

func (w *Writer) commitBatch(req writeReq, nextSeq *int64) ([]int64, error) {
	tx, err := w.db.BeginTx(req.ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if req.newStatus != nil && *req.newStatus == "paused" {
		var st string
		if err := tx.QueryRowContext(req.ctx,
			"SELECT status FROM sessions WHERE id = ?", w.sessionID).Scan(&st); err != nil {
			return nil, err
		}
		if st != "active" && st != "paused" {
			return nil, fmt.Errorf("cannot pause from status %q", st)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	seqs := make([]int64, 0, len(req.events))
	for _, e := range req.events {
		payload, err := Canonical(e.Payload)
		if err != nil {
			return nil, fmt.Errorf("canonical encode: %w", err)
		}
		seq := *nextSeq
		*nextSeq++
		if _, err := tx.ExecContext(req.ctx, `INSERT INTO events
			(session_id, seq, created_at_utc, actor, type, payload_sha256, payload_json)
			VALUES (?,?,?,?,?,?,?)`,
			w.sessionID, seq, now, e.Actor, e.Type, Digest(payload), string(payload)); err != nil {
			return nil, fmt.Errorf("insert seq %d: %w", seq, err)
		}
		seqs = append(seqs, seq)
	}
	if req.newStatus != nil {
		if err := SetSessionStatusTx(req.ctx, tx, w.sessionID, *req.newStatus); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if req.newStatus != nil && isTerminal(*req.newStatus) {
		w.closed = true // terminal commit closes the writer
	}
	return seqs, nil
}

func isTerminal(status string) bool {
	switch status {
	case "completed", "cancelled", "failed":
		return true
	}
	return false
}

// Append submits events to the serialized writer and returns the assigned
// contiguous seq values.
func (w *Writer) Append(ctx context.Context, events ...Event) ([]int64, error) {
	return w.submitReq(ctx, events, nil)
}

// AppendWithStatus submits events plus a same-transaction status transition.
func (w *Writer) AppendWithStatus(ctx context.Context, status string, events ...Event) ([]int64, error) {
	return w.submitReq(ctx, events, &status)
}

func (w *Writer) submitReq(ctx context.Context, events []Event, status *string) ([]int64, error) {
	select {
	case <-w.done:
		return nil, ErrWriterClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	resp := make(chan writeResp, 1)
	w.submit <- writeReq{ctx: ctx, events: events, newStatus: status, resp: resp}
	select {
	case r := <-resp:
		return r.seqs, r.err
	case <-w.done:
		return nil, ErrWriterClosed
	}
}

// Close stops the writer goroutine. Subsequent Appends return
// ErrWriterClosed.
func (w *Writer) Close() error {
	close(w.submit)
	<-w.done
	return nil
}

// ReadEvents reads all events of a session ordered by seq.
func ReadEvents(ctx context.Context, db *sql.DB, sessionID string) ([]StoredEvent, error) {
	rows, err := db.QueryContext(ctx, `SELECT session_id, seq, created_at_utc, actor, type,
		payload_sha256, payload_json FROM events WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredEvent
	for rows.Next() {
		var e StoredEvent
		if err := rows.Scan(&e.SessionID, &e.Seq, &e.CreatedAtUTC, &e.Actor, &e.Type,
			&e.PayloadSHA256, &e.PayloadJSON); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ReplayResult reports the replay oracle outcome for one session.
type ReplayResult struct {
	Count          int
	MinSeq, MaxSeq int64
}

// Replay is the replay oracle: per DATA-MODEL §3 and VA-E1, seq must be
// contiguous 0..max (0-based, no gaps, duplicates or reorder), each stored
// payload_sha256 must equal sha256 of the stored payload_json bytes
// (byte-exact, not re-derived), and every row must be attributed to
// sessionID. Any violation is an error.
func Replay(ctx context.Context, db *sql.DB, sessionID string) (ReplayResult, error) {
	events, err := ReadEvents(ctx, db, sessionID)
	if err != nil {
		return ReplayResult{}, err
	}
	for i, e := range events {
		if e.SessionID != sessionID {
			return ReplayResult{}, fmt.Errorf("event %d attributed to session %q, want %q", e.Seq, e.SessionID, sessionID)
		}
		if e.Seq != int64(i) {
			return ReplayResult{}, fmt.Errorf("seq not contiguous 0-based: position %d has seq %d", i, e.Seq)
		}
		if got := Digest([]byte(e.PayloadJSON)); got != e.PayloadSHA256 {
			return ReplayResult{}, fmt.Errorf("event %d payload digest mismatch: stored %s computed %s", e.Seq, e.PayloadSHA256, got)
		}
	}
	res := ReplayResult{Count: len(events)}
	if len(events) > 0 {
		res.MinSeq = events[0].Seq
		res.MaxSeq = events[len(events)-1].Seq
	}
	return res, nil
}
