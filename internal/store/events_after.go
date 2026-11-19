package store

import (
	"context"
	"fmt"
	"time"
)

// EventsAfter returns committed events for sessionID with seq greater
// than afterSeq, ordered by seq. Used by the follow (tail) surface to
// print only new events; digest verification is skipped here because
// Replay owns integrity checking.
func (s *Store) EventsAfter(ctx context.Context, sessionID string, afterSeq int64) ([]RecordedEvent, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	var rows []eventRow
	err := s.gdb.WithContext(ctx).
		Where(map[string]any{"session_id": sessionID}).
		Where("seq > ?", afterSeq).
		Order("seq").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("events after seq %d: %w", afterSeq, err)
	}
	out := make([]RecordedEvent, 0, len(rows))
	for _, r := range rows {
		created, _ := time.Parse(time.RFC3339Nano, r.CreatedAtUTC)
		out = append(out, RecordedEvent{
			SessionID: r.SessionID, Seq: r.Seq, CreatedAt: created,
			Actor: r.Actor, Type: r.Type, PayloadSHA: r.PayloadSHA, PayloadJSON: r.PayloadJSON,
		})
	}
	return out, nil
}
