package recall

import (
	"context"
	"errors"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/learning"
)

// ErrAllSourcesFailed is the sentinel error returned by Broker.Query
// when every configured (non-nil) source returned an error.
//
// M9 (Bug Hunt Findings May 2026). Before this sentinel existed the
// broker swallowed every per-source error, logged a warning, and
// returned (nil, nil) — making a complete recall outage
// indistinguishable from a zero-result query against a healthy
// broker. Long conversations could therefore silently degrade to
// recency-only retrieval with no observable signal anywhere on the
// wire.
//
// Callers that need only a single discrimination point should
// `errors.Is(err, ErrAllSourcesFailed)`. Callers that want the
// individual per-source failures (operator triage, structured
// logging) can `errors.Unwrap` the returned error: the broker joins
// the per-source errors via errors.Join so each underlying cause
// remains reachable via errors.Is / errors.As on its concrete type.
//
// A *partial* failure (some sources succeed, others fail) intentionally
// does NOT surface as ErrAllSourcesFailed. The healthy sources'
// observations are still returned and err is nil. This preserves the
// pre-M9 lenience for the common transient-source case while keeping
// the all-failed mode discriminable.
var ErrAllSourcesFailed = errors.New("recall: all configured sources failed")

type dateRangeKey struct{}

// DateRange specifies an optional time window for filtering recall results.
// Both From and To are inclusive. Zero values mean "no bound" on that end.
type DateRange struct {
	From time.Time
	To   time.Time
}

// WithDateRange returns a context carrying the given DateRange for recall queries.
func WithDateRange(ctx context.Context, dr DateRange) context.Context {
	return context.WithValue(ctx, dateRangeKey{}, dr)
}

// dateRangeFromContext extracts any DateRange attached to the context.
func dateRangeFromContext(ctx context.Context) (DateRange, bool) {
	v := ctx.Value(dateRangeKey{})
	if v == nil {
		return DateRange{}, false
	}
	dr, ok := v.(DateRange)
	return dr, ok
}

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
// Returns: observations merged from every configured source, ordered
// by freshness. When every configured (non-nil) source returns an
// error the result is empty and err wraps ErrAllSourcesFailed —
// callers can discriminate "all recall sources broken" from "no
// matches" via errors.Is(err, ErrAllSourcesFailed). Partial failures
// (some sources succeed) preserve the pre-M9 lenience: healthy
// observations are returned and err is nil.
// Side effects: queries all configured sources concurrently and logs source errors.
func (b *broker) Query(ctx context.Context, query string, limit int) ([]Observation, error) {
	if ctx == nil {
		return []Observation{}, nil
	}

	agentID := b.agentIDFromContext(ctx)
	results, sourceErrs, configuredCount := b.collectWithErrors(ctx, query, limit)

	// M9: if every configured source errored, surface a typed error
	// so the caller can distinguish total failure from "no results".
	// Zero configured sources is intentionally NOT a failure — that's
	// a benign no-op config, not a fan-out exhaustion.
	if configuredCount > 0 && len(sourceErrs) == configuredCount {
		joined := errors.Join(sourceErrs...)
		return []Observation{}, errors.Join(ErrAllSourcesFailed, joined)
	}

	merged := b.merge(results, agentID)
	if dr, ok := dateRangeFromContext(ctx); ok {
		merged = b.filterByDateRange(merged, dr)
	}
	b.sortByFreshness(merged)
	return b.limitResults(merged, limit), nil
}

// collectWithErrors queries all configured sources in parallel and
// returns their result sets alongside the per-source errors and the
// number of non-nil sources that were dispatched.
//
// Expected: ctx is usable by each source query and query/limit are forwarded unchanged.
// Returns:
//   - results: one observation slice per configured source, with nil
//     entries for nil or failed sources.
//   - sourceErrs: aggregated per-source errors in undefined order. The
//     caller compares len(sourceErrs) against configuredCount to
//     decide whether every configured source failed (M9 sentinel
//     return).
//   - configuredCount: the number of non-nil sources actually
//     dispatched. nil entries are absent rather than failed, so they
//     must not skew the all-failed determination.
//
// Side effects: launches concurrent source queries and logs any query failures.
func (b *broker) collectWithErrors(ctx context.Context, query string, limit int) ([][]Observation, []error, int) {
	sources := []Source{b.sessionSource, b.chainSource, b.hierarchySource, b.learningSource}
	sources = append(sources, b.extraSources...)
	results := make([][]Observation, len(sources))

	var (
		mu              sync.Mutex
		sourceErrs      []error
		configuredCount int
		wg              sync.WaitGroup
	)
	wg.Add(len(sources))
	for i, source := range sources {
		go func(index int, source Source) {
			defer wg.Done()
			if source == nil {
				return
			}
			mu.Lock()
			configuredCount++
			mu.Unlock()
			observations, err := source.Query(ctx, query, limit)
			if err != nil {
				log.Printf("warning: recall source query failed: %v", err)
				mu.Lock()
				sourceErrs = append(sourceErrs, err)
				mu.Unlock()
				return
			}
			results[index] = observations
		}(i, source)
	}
	wg.Wait()
	return results, sourceErrs, configuredCount
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

func (b *broker) filterByDateRange(merged []Observation, dr DateRange) []Observation {
	filtered := make([]Observation, 0, len(merged))
	for _, obs := range merged {
		if !dr.From.IsZero() && obs.Timestamp.Before(dr.From) {
			continue
		}
		if !dr.To.IsZero() && obs.Timestamp.After(dr.To) {
			continue
		}
		filtered = append(filtered, obs)
	}
	return filtered
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
