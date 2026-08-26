package recall

import (
	"context"
	"fmt"
	"time"
)

// ChainSource adapts a ChainContextStore to the Source interface for the recall broker.
//
// Expected: store is a ChainContextStore; it may be nil.
// Returns: A Source that queries the chain context store and maps messages to Observations.
// Side effects: None.
type ChainSource struct {
	store ChainContextStore
}

// compile-time assertion that ChainSource implements Source.
var _ Source = (*ChainSource)(nil)

// NewChainSource constructs a ChainSource wrapping the supplied ChainContextStore.
//
// Expected: store may be nil; a nil store causes Query to return an empty result.
// Returns: A pointer to ChainSource.
// Side effects: None.
func NewChainSource(store ChainContextStore) *ChainSource {
	return &ChainSource{store: store}
}

// Query retrieves observations from the chain context store for the given query.
//
// Expected: ctx is non-nil and limit caps the result count; a nil store returns an empty slice.
// Returns: Observations mapped from chain messages, capped to limit when positive.
// Side effects: Reads messages from the underlying ChainContextStore.
func (c *ChainSource) Query(ctx context.Context, _ string, limit int) ([]Observation, error) {
	if c.store == nil {
		return []Observation{}, nil
	}
	n := limit
	if n <= 0 {
		n = 100
	}
	messages, err := c.store.GetByAgent("", n)
	if err != nil {
		return nil, err
	}
	chainID := c.store.ChainID()
	agentID := agentIDFromCtx(ctx)
	observations := make([]Observation, 0, len(messages))
	for i := range messages {
		msg := &messages[i]
		observations = append(observations, Observation{
			ID:        fmt.Sprintf("%s-%d", chainID, i),
			Source:    "chain",
			AgentID:   agentID,
			Timestamp: time.Now(),
			Content:   msg.Content,
		})
	}
	return observations, nil
}
