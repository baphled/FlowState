package recall

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/learning"
)

// Observation represents a normalised recall result.
type Observation struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`
	AgentID   string    `json:"agent_id"`
	Timestamp time.Time `json:"timestamp"`
	Content   string    `json:"content"`
}

// Source provides recall observations for a query.
type Source interface {
	// Query returns recall observations for the supplied query and limit.
	Query(ctx context.Context, query string, limit int) ([]Observation, error)
}

// Broker queries multiple recall sources and merges their results.
type Broker interface {
	// Query returns merged observations from all configured sources.
	Query(ctx context.Context, query string, limit int) ([]Observation, error)
}

// broker coordinates recall queries across the configured sources.
type broker struct {
	sessionSource   Source
	chainSource     Source
	hierarchySource Source
	learningSource  Source
	extraSources    []Source
}

// NewRecallBroker creates a broker that queries all supplied sources.
//
// Expected: all source arguments may be nil and ctx is supplied per query. Extra variadic sources are appended to the fan-out set.
// Returns: a Broker that fans out to the configured sources and merges their results.
// Side effects: stores the supplied sources for later concurrent querying.
func NewRecallBroker(sessionSource, chainSource, hierarchySource, learningSource Source, extra ...Source) Broker {
	return &broker{
		sessionSource:   sessionSource,
		chainSource:     chainSource,
		hierarchySource: hierarchySource,
		learningSource:  learningSource,
		extraSources:    extra,
	}
}

// Query returns merged observations from all configured sources.
//
// Expected: ctx is non-nil when query scoping is required.
// Returns: observations merged from every configured source, ordered by freshness.
// Side effects: queries all configured sources concurrently and logs source errors.
func (b *broker) Query(ctx context.Context, query string, limit int) ([]Observation, error) {
	if ctx == nil {
		return []Observation{}, nil
	}

	agentID := b.agentIDFromContext(ctx)
	results := b.collect(ctx, query, limit)
	merged := b.merge(results, agentID)
	b.sortByFreshness(merged)
	return b.limitResults(merged, limit), nil
}

// collect queries all configured sources in parallel and preserves their result sets.
//
// Expected: ctx is usable by each source query and query/limit are forwarded unchanged.
// Returns: one result slice per configured source, with nil entries for skipped or failed sources.
// Side effects: launches concurrent source queries and logs any query failures.
func (b *broker) collect(ctx context.Context, query string, limit int) [][]Observation {
	sources := []Source{b.sessionSource, b.chainSource, b.hierarchySource, b.learningSource}
	sources = append(sources, b.extraSources...)
	results := make([][]Observation, len(sources))

	var wg sync.WaitGroup
	wg.Add(len(sources))
	for i, source := range sources {
		go func(index int, source Source) {
			defer wg.Done()
			if source == nil {
				return
			}
			observations, err := source.Query(ctx, query, limit)
			if err != nil {
				log.Printf("warning: recall source query failed: %v", err)
				return
			}
			results[index] = observations
		}(i, source)
	}
	wg.Wait()
	return results
}

// merge filters and combines source results into a single slice.
//
// Expected: results contains per-source observation slices and agentID is the active agent scope.
// Returns: a flattened slice containing only observations allowed by the agent scope.
// Side effects: none.
func (b *broker) merge(results [][]Observation, agentID string) []Observation {
	merged := make([]Observation, 0)
	for _, sourceResults := range results {
		for _, observation := range sourceResults {
			if agentID != "" && observation.AgentID != "" && observation.AgentID != agentID {
				continue
			}
			merged = append(merged, observation)
		}
	}
	return merged
}

// sortByFreshness orders observations from newest to oldest.
//
// Expected: merged contains observations that can be reordered in place.
// Returns: none.
// Side effects: mutates merged by sorting it in descending timestamp order.
func (b *broker) sortByFreshness(merged []Observation) {
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Timestamp.Equal(merged[j].Timestamp) {
			return merged[i].ID < merged[j].ID
		}
		return merged[i].Timestamp.After(merged[j].Timestamp)
	})
}

// limitResults trims the merged observations to the requested maximum.
//
// Expected: merged contains the final ordered observations and limit is the requested cap.
// Returns: the original slice when limit is non-positive or within bounds, otherwise a truncated slice.
// Side effects: none.
func (b *broker) limitResults(merged []Observation, limit int) []Observation {
	if limit > 0 && len(merged) > limit {
		return merged[:limit]
	}
	return merged
}

// agentIDFromContext extracts the active agent identifier from the query context.
//
// Expected: ctx may contain a string value under learning.AgentIDKey.
// Returns: the agent identifier when present and valid, otherwise an empty string.
// Side effects: none.
func (b *broker) agentIDFromContext(ctx context.Context) string {
	value := ctx.Value(learning.AgentIDKey)
	if value == nil {
		return ""
	}
	if agentID, ok := value.(string); ok {
		return agentID
	}
	return ""
}
