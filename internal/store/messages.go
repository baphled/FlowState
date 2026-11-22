package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Steering message sentinels: fail-closed delivery semantics.
var (
	// ErrMessageEmpty is returned when sender or body is empty.
	ErrMessageEmpty = errors.New("store: steering sender and body must not be empty")
	// ErrMessageScope is returned when the sender is not in the
	// recipient's delegation chain (steering follows delegation scope).
	ErrMessageScope = errors.New("store: steering sender is outside the delegation chain")
)

// SendMessage records one durable steering directive addressed to
// sessionID from sender. Unknown recipient or sender sessions are
// ErrUnknownSession; empty sender or body is ErrMessageEmpty. The
// sender must appear in the recipient's delegation chain (root or
// ancestor), so steering follows delegation scope.
func (s *Store) SendMessage(ctx context.Context, sessionID, sender, body string) (SessionSteeringMessage, error) {
	if sender == "" || body == "" {
		return SessionSteeringMessage{}, ErrMessageEmpty
	}
	msg := SessionSteeringMessage{
		ID:        newMessageID(),
		SessionID: sessionID,
		Sender:    sender,
		Body:      body,
		CreatedAt: time.Now().UTC(),
	}
	// Delegation-scope gate BEFORE the insert: the single-connection
	// pool serializes all access, so the read must not run inside the
	// write transaction (it would self-deadlock).
	chain, cerr := s.DelegationChain(ctx, sessionID)
	if cerr != nil {
		return SessionSteeringMessage{}, fmt.Errorf("send steering message: %w", cerr)
	}
	inChain := false
	for _, ancestor := range chain {
		if ancestor.ID == sender {
			inChain = true
			break
		}
	}
	if !inChain {
		return SessionSteeringMessage{}, ErrMessageScope
	}
	err := s.gdb.WithContext(ctx).Create(&sessionMessageRow{
		ID:           msg.ID,
		SessionID:    sessionID,
		Sender:       sender,
		Body:         body,
		CreatedAtUTC: iso(msg.CreatedAt),
	}).Error
	if err != nil {
		return SessionSteeringMessage{}, fmt.Errorf("send steering message: %w", err)
	}
	return msg, nil
}

// PendingSteeringMessages returns the undelivered steering messages
// addressed to sessionID, oldest first.
func (s *Store) PendingSteeringMessages(ctx context.Context, sessionID string) ([]SessionSteeringMessage, error) {
	var rows []sessionMessageRow
	err := s.gdb.WithContext(ctx).
		Where(map[string]any{"session_id": sessionID, "delivered_at_utc": nil}).
		Order("created_at_utc, id").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load pending steering messages: %w", err)
	}
	out := make([]SessionSteeringMessage, 0, len(rows))
	for _, row := range rows {
		created, perr := parseStored(row.ID, "created_at_utc", row.CreatedAtUTC)
		if perr != nil {
			return nil, fmt.Errorf("load pending steering messages: %w", perr)
		}
		out = append(out, SessionSteeringMessage{
			ID: row.ID, SessionID: row.SessionID, Sender: row.Sender,
			Body: row.Body, CreatedAt: created,
		})
	}
	sortChronological(out, messageKey)
	return out, nil
}

// messageKey is the chronological sort key of a steering message.
func messageKey(m SessionSteeringMessage) (time.Time, string) { return m.CreatedAt, m.ID }

// sortMessageRows orders rows chronologically, or fails on the first row
// whose created_at_utc does not parse.
func sortMessageRows(rows []sessionMessageRow) error {
	created := make(map[string]time.Time, len(rows))
	for _, r := range rows {
		t, err := parseStored(r.ID, "created_at_utc", r.CreatedAtUTC)
		if err != nil {
			return err
		}
		created[r.ID] = t
	}
	sortChronological(rows, func(r sessionMessageRow) (time.Time, string) { return created[r.ID], r.ID })
	return nil
}

// DrainSteeringMessages atomically drains every pending message for
// sessionID: each message becomes a session.steered event (actor user,
// payload sender/body) and is marked delivered, all in one
// transaction. An empty pending set appends nothing and is a no-op.
// The caller supplies the seq-assignment event writer for the session.
// The writer advances only after the transaction commits, and by the
// number of events it appended, so a rollback leaves it level with the log.
func (w *EventWriter) DrainSteeringMessages(ctx context.Context, sessionID string) (int, error) {
	if w.closed || w.store == nil || w.store.db == nil {
		return 0, ErrClosed
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var drained int64
	seq := w.next
	txErr := w.store.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		drained = 0 // a retried closure must not double count
		var rows []sessionMessageRow
		if err := tx.Where(map[string]any{"session_id": sessionID, "delivered_at_utc": nil}).
			Order("created_at_utc, id").Find(&rows).Error; err != nil {
			return err
		}
		// Parse and order every row BEFORE the first insert or delivered
		// mark, so a corrupt timestamp aborts with nothing written.
		if err := sortMessageRows(rows); err != nil {
			return err
		}
		for _, row := range rows {
			if err := w.insertEvent(ctx, tx, seq+drained, "user", "session.steered", map[string]any{
				"sender": row.Sender, "body": row.Body,
			}); err != nil {
				if isUniqueConstraint(err) {
					return fmt.Errorf("%w: session %s seq %d", ErrSeqCollision, sessionID, seq+drained)
				}
				return err
			}
			if err := tx.Model(&sessionMessageRow{}).
				Where(map[string]any{"id": row.ID, "delivered_at_utc": nil}).
				Updates(map[string]any{"delivered_at_utc": iso(time.Now().UTC())}).Error; err != nil {
				return err
			}
			drained++
		}
		return nil
	})
	if txErr != nil {
		return 0, fmt.Errorf("drain steering messages: %w", txErr)
	}
	w.next = seq + drained
	return int(drained), nil
}

// newMessageID generates the message row id (UUIDv7, matching session
// id ordering properties).
func newMessageID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// UUIDv7 generation cannot fail on modernc-covered platforms;
		// degrade to a time-ordered fallback rather than panic.
		return fmt.Sprintf("msg-%d", time.Now().UTC().UnixNano())
	}
	return id.String()
}

// DrainSteeringMessagesFor is a convenience wrapper that drains
// through a freshly-registered writer for the session.
func (s *Store) DrainSteeringMessagesFor(ctx context.Context, sessionID string) (int, error) {
	w, err := s.NewEventWriter(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = w.Close() }()
	return w.DrainSteeringMessages(ctx, sessionID)
}
