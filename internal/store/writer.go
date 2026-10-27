package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Session is the persisted session record per DATA-MODEL §2.
type Session struct {
	ID        string
	CreatedAt time.Time
	UpdatedAt time.Time
	Status    string
	Provider  string
	ModelID   string
	Harness   string
	ConfigSHA string
}

// CreateSession inserts a session row with status active and returns it.
// The ID must be a caller-generated UUIDv7; attribution fields must be
// runtime-observed, never prompt-claimed.
func (s *Store) CreateSession(ctx context.Context, id, provider, modelID, harness, configSHA string) (*Session, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	sess := &Session{
		ID: id, CreatedAt: now, UpdatedAt: now, Status: "active",
		Provider: provider, ModelID: modelID, Harness: harness, ConfigSHA: configSHA,
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, created_at_utc, updated_at_utc, status, provider, model_id, harness, config_sha256)
		 VALUES (?,?,?,?,?,?,?,?)`,
		sess.ID, iso(sess.CreatedAt), iso(sess.UpdatedAt), sess.Status, sess.Provider, sess.ModelID, sess.Harness, sess.ConfigSHA)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return sess, nil
}

// GetSession loads a session by id.
func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	var sess Session
	var created, updated string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, created_at_utc, updated_at_utc, status, provider, model_id, harness, config_sha256 FROM sessions WHERE id=?`, id).
		Scan(&sess.ID, &created, &updated, &sess.Status, &sess.Provider, &sess.ModelID, &sess.Harness, &sess.ConfigSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSession, id)
	}
	if err != nil {
		return nil, err
	}
	sess.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &sess, nil
}

func (s *Store) checkOpen() error {
	if s == nil || s.db == nil {
		return ErrClosed
	}
	return nil
}

func iso(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// isUniqueConstraint reports whether err is a SQLite UNIQUE constraint
// failure; the modernc driver reports the extended code (1555/2067) in
// the message text rather than via Error.Code, so match the text.
func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// canonicalJSON serialises v with sorted keys and no insignificant
// whitespace, so its bytes can be hashed as the canonical payload.
func canonicalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// writerRegistry enforces at most one live EventWriter per session id
// across the whole process for this Store.
var writerRegistry sync.Map // session id -> *EventWriter

// EventWriter is the single serialized event writer for one session
// (DATA-MODEL §5). It owns the next-seq allocation and every events
// insert inside a transaction. Release it with Close so a later writer
// may be registered.
type EventWriter struct {
	store   *Store
	session string
	mu      sync.Mutex
	next    int64
	closed  bool
}

// NewEventWriter registers the single event writer for session id.
// If a live writer already exists for the session it returns
// ErrWriterExists.
func (s *Store) NewEventWriter(ctx context.Context, sessionID string) (*EventWriter, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	if _, err := s.GetSession(ctx, sessionID); err != nil {
		return nil, err
	}
	w := &EventWriter{store: s, session: sessionID}
	if _, loaded := writerRegistry.LoadOrStore(sessionID, w); loaded {
		return nil, fmt.Errorf("%w: %s", ErrWriterExists, sessionID)
	}
	// Recover next seq from the committed log.
	var maxSeq sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(seq) FROM events WHERE session_id=?`, sessionID).Scan(&maxSeq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writerRegistry.Delete(sessionID)
		return nil, fmt.Errorf("load max seq: %w", err)
	}
	w.next = 0
	if maxSeq.Valid {
		w.next = maxSeq.Int64 + 1
	}
	return w, nil
}

// Event is one attributable ordered event as appended by the writer.
// Payload is any JSON-serialisable value; it is canonicalised on write.
type Event struct {
	SessionID string
	Seq       int64
	CreatedAt time.Time
	Actor     string
	Type      string
	Payload   any
}

// Append validates e, assigns the next 0-based contiguous seq for the
// session, canonicalises the payload, computes payload_sha256 and
// commits the row in one transaction. It returns the committed seq.
// Actor and type must be non-empty; an existing (session, seq) row
// yields ErrSeqCollision. Writes after writer Close fail with
// ErrClosed.
func (w *EventWriter) Append(ctx context.Context, actor, typ string, payload any) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.store == nil || w.store.db == nil {
		return -1, ErrClosed
	}
	if actor == "" {
		return -1, ErrEmptyActor
	}
	if typ == "" {
		return -1, ErrEmptyType
	}
	payloadJSON, err := canonicalJSON(payload)
	if err != nil {
		return -1, fmt.Errorf("canonicalize payload: %w", err)
	}
	digest := sha256Hex(payloadJSON)
	seq := w.next
	_, err = w.store.db.ExecContext(ctx,
		`INSERT INTO events (session_id, seq, created_at_utc, actor, type, payload_sha256, payload_json)
		 VALUES (?,?,?,?,?,?,?)`,
		w.session, seq, iso(time.Now().UTC()), actor, typ, digest, string(payloadJSON))
	if err != nil {
		if isUniqueConstraint(err) {
			return -1, fmt.Errorf("%w: session %s seq %d", ErrSeqCollision, w.session, seq)
		}
		return -1, fmt.Errorf("append event seq %d: %w", seq, err)
	}
	w.next++
	return seq, nil
}

// NextSeq reports the next seq this writer will assign.
func (w *EventWriter) NextSeq() int64 { return w.next }

// SessionID returns the session this writer owns.
func (w *EventWriter) SessionID() string { return w.session }

// Close releases the writer registration. The committed event log stays
// intact; a new writer for the session resumes from the committed max
// seq. Double Close is a no-op.
func (w *EventWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	writerRegistry.CompareAndDelete(w.session, w)
	return nil
}
