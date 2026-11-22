package store

import (
	"context"
	"fmt"
	"time"
)

// CreateChildSession inserts a session row linked to parentID in one
// operation and returns it with status active. An empty parentID
// creates a root session, persisted as a SQL NULL parent_id.
func (s *Store) CreateChildSession(ctx context.Context, id, parentID, provider, modelID, harness, configSHA string) (*Session, error) {
	return s.insertSession(ctx, id, parentID, provider, modelID, harness, configSHA)
}

// ListSessions returns sessions ordered by creation time (oldest first),
// breaking ties by ID. A non-empty status filters on it. Invalid stored
// timestamps return a parsing error rather than a zero timestamp.
func (s *Store) ListSessions(ctx context.Context, status string) ([]Session, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	q := s.gdb.WithContext(ctx).Model(&sessionRow{})
	if status != "" {
		q = q.Where(map[string]any{"status": status})
	}
	var rows []sessionRow
	if err := q.Order("created_at_utc, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := []Session{}
	for _, r := range rows {
		sess := Session{
			ID: r.ID, Status: r.Status, Provider: r.Provider, ModelID: r.ModelID,
			Harness: r.Harness, ConfigSHA: r.ConfigSHA,
		}
		if r.ParentID != nil {
			sess.ParentID = *r.ParentID
		}
		var err error
		if sess.CreatedAt, err = time.Parse(time.RFC3339Nano, r.CreatedAtUTC); err != nil {
			return nil, err
		}
		if sess.UpdatedAt, err = time.Parse(time.RFC3339Nano, r.UpdatedAtUTC); err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	sortChronological(out, func(s Session) (time.Time, string) { return s.CreatedAt, s.ID })
	return out, nil
}

// TamperEventPayload rewrites an event payload without fixing
// payload_sha256. It exists ONLY for tests that verify Replay's hash
// checking (VA-E1); production code must never call it.
func (s *Store) TamperEventPayload(ctx context.Context, sessionID string, seq int64, payloadJSON string) error {
	q := s.gdb.WithContext(ctx).Model(&eventRow{}).
		Where(map[string]any{"session_id": sessionID, "seq": seq}).
		Update("payload_json", payloadJSON)
	return q.Error
}

// UpdateSessionStatus sets sessions.status and refreshes
// updated_at_utc. Unknown ids are ErrUnknownSession.
func (s *Store) UpdateSessionStatus(ctx context.Context, id, status string) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	q := s.gdb.WithContext(ctx).Model(&sessionRow{}).
		Where(map[string]any{"id": id}).
		Updates(map[string]any{"status": status, "updated_at_utc": iso(time.Now().UTC())})
	if q.Error != nil {
		return fmt.Errorf("update status %s for %s: %w", status, id, q.Error)
	}
	if q.RowsAffected == 0 {
		return fmt.Errorf("%w: %s", ErrUnknownSession, id)
	}
	return nil
}
