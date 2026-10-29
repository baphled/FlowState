package store

import (
	"context"
	"fmt"
	"time"
)

// ListSessions returns sessions ordered by creation time (oldest
// first). A non-empty status filters on it. It absorbs the projection
// error by returning a zero timestamp with the parsed session.
func (s *Store) ListSessions(ctx context.Context, status string) ([]Session, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	q := `SELECT id, created_at_utc, updated_at_utc, status, provider, model_id, harness, config_sha256 FROM sessions`
	args := []any{}
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at_utc, rowid`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var sess Session
		var created, updated string
		if err := rows.Scan(&sess.ID, &created, &updated, &sess.Status, &sess.Provider, &sess.ModelID, &sess.Harness, &sess.ConfigSHA); err != nil {
			return nil, err
		}
		if sess.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		if sess.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// TamperEventPayload rewrites an event payload without fixing
// payload_sha256. It exists ONLY for tests that verify Replay's hash
// checking (VA-E1); production code must never call it.
func (s *Store) TamperEventPayload(ctx context.Context, sessionID string, seq int64, payloadJSON string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE events SET payload_json=? WHERE session_id=? AND seq=?`,
		payloadJSON, sessionID, seq)
	return err
}

// UpdateSessionStatus sets sessions.status and refreshes
// updated_at_utc. Unknown ids are ErrUnknownSession.
func (s *Store) UpdateSessionStatus(ctx context.Context, id, status string) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET status=?, updated_at_utc=? WHERE id=?`,
		status, iso(time.Now().UTC().Add(time.Millisecond)), id)
	if err != nil {
		return fmt.Errorf("update status %s for %s: %w", status, id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", ErrUnknownSession, id)
	}
	return nil
}
