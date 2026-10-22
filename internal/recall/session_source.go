package recall

import (
	"context"
	"time"
)

// SessionSource adapts a FileContextStore to the Source interface for the recall broker.
//
// Expected: store is a pointer to a FileContextStore; it may be nil.
// Returns: A Source that queries the session context store and maps messages to Observations.
// Side effects: None.
type SessionSource struct {
	store *FileContextStore
}

// compile-time assertion that SessionSource implements Source.
var _ Source = (*SessionSource)(nil)

// NewSessionSource constructs a SessionSource wrapping the supplied FileContextStore.
//
// Expected: store may be nil; a nil store causes Query to return an empty result.
// Returns: A pointer to SessionSource.
// Side effects: None.
func NewSessionSource(store *FileContextStore) *SessionSource {
	return &SessionSource{store: store}
}

// Query retrieves observations from the session context store for the given query.
//
// Expected: ctx is non-nil and limit caps the result count; a nil store returns an empty slice.
// Returns: Observations mapped from stored messages, capped to limit when positive.
// Side effects: Reads messages from the underlying FileContextStore.
func (s *SessionSource) Query(ctx context.Context, _ string, limit int) ([]Observation, error) {
	if s.store == nil {
		return []Observation{}, nil
	}
	messages := s.store.GetStoredMessages()
	agentID := agentIDFromCtx(ctx)
	observations := make([]Observation, 0, len(messages))
	for i := range messages {
		msg := &messages[i]
		observations = append(observations, Observation{
			ID:        msg.ID,
			Source:    "session",
			AgentID:   agentID,
			Timestamp: time.Now(),
			Content:   msg.Message.Content,
		})
	}
	if limit > 0 && len(observations) > limit {
		observations = observations[:limit]
	}
	return observations, nil
}
