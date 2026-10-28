package store

import (
	"context"
	"fmt"
	"time"
)

// ListSessions returns sessions ordered by creation time (oldest
// first). A non-empty status filters on it. It absorbs the projection
// previously done via Store.DB() in internal/session so *sql.DB never
// escapes the store package.
func (s *Store) ListSessions(ctx context.Context, status string) ([]Session, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	query := `SELECT id, created_at_utc, updated_at_utc, status, provider, model_id, harness, config_sha256
		 FROM sessions`
	var args []any
	if status != "" {
		query += " WHERE status=?"
		args = append(args, status)
	}
	query += " ORDER BY created_at_utc, id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var sess Session
		var created, updated string
		if err := rows.Scan(&sess.ID, &created, &updated, &sess.Status, &sess.Provider, &sess.ModelID, &sess.Harness, &sess.ConfigSHA); err != nil {
			return nil, fmt.Errorf("list scan: %w", err)
		}
		if sess.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("parse created_at %q: %w", created, err)
		}
		if sess.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			return nil, fmt.Errorf("parse updated_at %q: %w", updated, err)
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
		status, iso(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("update status %s for %s: %w", status, id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", ErrUnknownSession, id)
	}
	return nil
}
