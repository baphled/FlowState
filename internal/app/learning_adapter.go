// Package app wires the learning recall bridge between the recall
// broker and the learning package's consumer-side interfaces.
package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/recall"
)

// SessionStartLearningAdapter implements learning.RecallClient by
// querying the recall broker at session start and injecting the
// recalled learnings into the agent context.
type SessionStartLearningAdapter struct {
	broker recall.Broker
	limit  int
}

// NewSessionStartLearningAdapter creates an adapter over the supplied
// recall broker.
//
// Expected:
//   - broker may be nil, in which case Search degrades to an empty result.
//   - limit defaults to 5 when zero.
//
// Returns:
//   - A SessionStartLearningAdapter satisfying learning.RecallClient.
//
// Side effects:
//   - None.
func NewSessionStartLearningAdapter(broker recall.Broker, limit int) *SessionStartLearningAdapter {
	if limit <= 0 {
		limit = 5
	}
	return &SessionStartLearningAdapter{broker: broker, limit: limit}
}

// Search performs a session-start recall query against the broker and
// maps observations to learning matches for agent-context injection.
//
// Expected:
//   - query is the session's priming query (agent id or opening topic).
//
// Returns:
//   - The top recalled learnings as learning.RecallMatch values.
//   - An empty slice when the broker is nil or returns no observations.
//   - An error when the broker query fails.
//
// Side effects:
//   - Emits a structured slog error when the broker is nil so the
//     missing dependency is surfaced at startup rather than silently
//     swallowed.
func (a *SessionStartLearningAdapter) Search(query string, limit int) ([]learning.RecallMatch, error) {
	if a.broker == nil {
		slog.Error("session-start learning adapter has no recall broker; prior learnings unavailable")
		return []learning.RecallMatch{}, nil
	}
	if limit <= 0 {
		limit = a.limit
	}
	observations, err := a.broker.Query(context.Background(), query, limit)
	if err != nil {
		return nil, fmt.Errorf("session-start recall query: %w", err)
	}
	matches := make([]learning.RecallMatch, 0, len(observations))
	for _, obs := range observations {
		matches = append(matches, learning.RecallMatch{
			ID:      obs.ID,
			Content: obs.Content,
			AgentID: obs.AgentID,
		})
	}
	return matches, nil
}
