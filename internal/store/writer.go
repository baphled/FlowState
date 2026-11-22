package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// CreateSession inserts a session row with status active and returns it.
// The ID must be a caller-generated UUIDv7; attribution fields must be
// runtime-observed, never prompt-claimed.
func (s *Store) CreateSession(ctx context.Context, id, provider, modelID, harness, configSHA string) (*Session, error) {
	return s.insertSession(ctx, id, "", provider, modelID, harness, configSHA)
}

// insertSession persists one session row in a single ORM operation. An
// empty parentID stores a root session with a SQL NULL parent_id.
func (s *Store) insertSession(ctx context.Context, id, parentID, provider, modelID, harness, configSHA string) (*Session, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	sess := &Session{
		ID: id, CreatedAt: now, UpdatedAt: now, Status: "active",
		Provider: provider, ModelID: modelID, Harness: harness, ConfigSHA: configSHA,
		ParentID: parentID,
	}
	row := sessionRow{
		ID: sess.ID, CreatedAtUTC: iso(sess.CreatedAt), UpdatedAtUTC: iso(sess.UpdatedAt),
		Status: sess.Status, Provider: sess.Provider, ModelID: sess.ModelID,
		Harness: sess.Harness, ConfigSHA: sess.ConfigSHA, ParentID: nullableParent(parentID),
	}
	if err := s.gdb.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return sess, nil
}

// GetSession loads a session by id.
func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	var row sessionRow
	err := s.gdb.WithContext(ctx).Where(map[string]any{"id": id}).First(&row).Error
	if isRecordNotFound(err) {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSession, id)
	}
	if err != nil {
		return nil, err
	}
	return row.session(), nil
}

func (s *Store) checkOpen() error {
	if s == nil || s.db == nil {
		return ErrClosed
	}
	return nil
}

// nullableParent maps the empty root marker to a SQL NULL parent_id.
func nullableParent(parentID string) *string {
	if parentID == "" {
		return nil
	}
	return &parentID
}

// session projects the row onto the public Session record; stored
// timestamps are UTC text.
func (r sessionRow) session() *Session {
	sess := &Session{
		ID: r.ID, Status: r.Status, Provider: r.Provider, ModelID: r.ModelID,
		Harness: r.Harness, ConfigSHA: r.ConfigSHA,
	}
	sess.CreatedAt, _ = time.Parse(time.RFC3339Nano, r.CreatedAtUTC)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339Nano, r.UpdatedAtUTC)
	if r.ParentID != nil {
		sess.ParentID = *r.ParentID
	}
	return sess
}

// iso renders t in the fixed-width stored layout; parsing with
// time.RFC3339Nano accepts both it and older trimmed strings.
func iso(t time.Time) string { return t.UTC().Format(timestampLayout) }

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

// NewEventWriter registers the single event writer for session id.
// If a live writer already exists for the session it returns
// ErrWriterExists.
func (s *Store) NewEventWriter(ctx context.Context, sessionID string) (*EventWriter, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	sess, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	w := &EventWriter{store: s, session: sessionID}
	// Restore paused writers as paused: a fresh writer for a session
	// whose persisted status is paused must keep the pause invariant
	// (Resume stays available; Append stays guarded) across processes.
	w.paused = sess.Status == "paused"
	if _, loaded := writerRegistry.LoadOrStore(sessionID, w); loaded {
		return nil, fmt.Errorf("%w: %s", ErrWriterExists, sessionID)
	}
	// Recover next seq from the committed log.
	var top eventRow
	err = s.gdb.WithContext(ctx).Where(map[string]any{"session_id": sessionID}).Order("seq desc").First(&top).Error
	if err != nil && !isRecordNotFound(err) {
		writerRegistry.Delete(sessionID)
		return nil, fmt.Errorf("load max seq: %w", err)
	}
	w.next = 0
	if err == nil {
		w.next = top.Seq + 1
	}
	return w, nil
}

// Append validates e, assigns the next 0-based contiguous seq for the
// session, canonicalises the payload, computes payload_sha256 and
// commits the row in one transaction. It returns the committed seq.
// Actor and type must be non-empty; an existing (session, seq) row
// yields ErrSeqCollision. Writes after writer Close fail with
// ErrClosed.
//
// Cancellation has a definite outcome. A context that is already done
// refuses the append before anything is written. Once the write has
// started it is not interrupted: the insert commits on its own, so a
// cancellation arriving during it could report failure for a row that
// was in fact committed, leaving the writer one seq behind the log and
// every later append (including the one recording the cancellation)
// colliding with it.
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
	seq := w.next
	if err := ctx.Err(); err != nil {
		return -1, fmt.Errorf("append event seq %d: %w", seq, err)
	}
	if err := w.insertEvent(context.WithoutCancel(ctx), w.store.gdb, seq, actor, typ, payload); err != nil {
		return -1, err
	}
	w.next++
	return seq, nil
}

// insertEvent canonicalises the payload and inserts the mapped event
// row inside the given ORM scope (plain or transactional).
func (w *EventWriter) insertEvent(ctx context.Context, scope *gorm.DB, seq int64, actor, typ string, payload any) error {
	payloadJSON, err := canonicalJSON(payload)
	if err != nil {
		return fmt.Errorf("canonicalize payload: %w", err)
	}
	row := eventRow{
		SessionID: w.session, Seq: seq, CreatedAtUTC: iso(time.Now().UTC()),
		Actor: actor, Type: typ,
		PayloadSHA: sha256Hex(payloadJSON), PayloadJSON: string(payloadJSON),
	}
	if err := scope.WithContext(ctx).Create(&row).Error; err != nil {
		if isUniqueConstraint(err) {
			return fmt.Errorf("%w: session %s seq %d", ErrSeqCollision, w.session, seq)
		}
		return fmt.Errorf("append event seq %d: %w", seq, err)
	}
	return nil
}

// setSessionStatus updates the session status inside the ORM scope.
func (w *EventWriter) setSessionStatus(ctx context.Context, scope *gorm.DB, status string) error {
	q := scope.WithContext(ctx).Model(&sessionRow{}).
		Where(map[string]any{"id": w.session}).
		Updates(map[string]any{"status": status, "updated_at_utc": iso(time.Now().UTC())})
	if q.Error != nil {
		return fmt.Errorf("set status %s: %w", status, q.Error)
	}
	return nil
}

// Pause atomically appends a `session.paused` event (actor "engine",
// payload carries reason, consumed_budget and next_safe_op) and sets
// sessions.status='paused' in one transaction. Empty NextSafeOp
// defaults to "session run <id>".
func (w *EventWriter) Pause(ctx context.Context, pi PauseInfo) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.store == nil || w.store.db == nil {
		return ErrClosed
	}
	if pi.NextSafeOp == "" {
		pi.NextSafeOp = "session run <id>"
	}
	payload := map[string]any{
		"reason":          pi.Reason,
		"consumed_budget": pi.ConsumedBudget,
		"next_safe_op":    pi.NextSafeOp,
	}
	seq := w.next
	txErr := w.store.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := w.insertEvent(ctx, tx, seq, "engine", "session.paused", payload); err != nil {
			return err
		}
		if err := w.setSessionStatus(ctx, tx, "paused"); err != nil {
			return err
		}
		return chargePausedReservation(ctx, tx, w.session, pi.ConsumedBudget)
	})
	if txErr != nil {
		return txErr
	}
	w.next++
	w.paused = true
	return nil
}

// chargePausedReservation records the pause's consumed figure on the
// session's live reservation inside the pause transaction: consumed
// grows monotonically and caps at the granted amount, so a pause
// carrying no consumption figure (the exec loop) never resurrects
// spent budget. Sessions without a live reservation are untouched.
func chargePausedReservation(ctx context.Context, scope *gorm.DB, sessionID string, consumed int64) error {
	var row budgetReservationRow
	err := scope.WithContext(ctx).
		Where(map[string]any{"child_id": sessionID, "status": ReservationStatusReserved}).
		First(&row).Error
	if isRecordNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load reservation for pause: %w", err)
	}
	next := consumed
	if next < row.Consumed {
		next = row.Consumed
	}
	if next > row.Granted {
		next = row.Granted
	}
	if next == row.Consumed {
		return nil
	}
	q := scope.WithContext(ctx).Model(&budgetReservationRow{}).
		Where(map[string]any{"id": row.ID}).
		Updates(map[string]any{"consumed": next})
	if q.Error != nil {
		return fmt.Errorf("charge reservation %s on pause: %w", row.ID, q.Error)
	}
	return nil
}

// Resume atomically appends a `session.resumed` event (actor "user",
// payload from_status=paused) and sets sessions.status='active' in one
// transaction. Resuming a session that is not paused returns
// ErrNotPaused and appends no event.
func (w *EventWriter) Resume(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.store == nil || w.store.db == nil {
		return ErrClosed
	}
	if !w.paused {
		return ErrNotPaused
	}
	seq := w.next
	txErr := w.store.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := w.insertEvent(ctx, tx, seq, "user", "session.resumed", map[string]any{"from_status": "paused"}); err != nil {
			return err
		}
		return w.setSessionStatus(ctx, tx, "active")
	})
	if txErr != nil {
		return txErr
	}
	w.next++
	w.paused = false
	return nil
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
