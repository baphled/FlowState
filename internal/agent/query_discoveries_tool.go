package agent

import (
	"encoding/json"
	"errors"

	"github.com/baphled/flowstate/internal/recall"
)

// QueryDiscoveriesTool queries recall discoveries using the configured filters.
type QueryDiscoveriesTool struct {
	Kind     string
	Affects  string
	Priority string
	Store    interface {
		Query(any) ([]any, error)
	}
}

// Run returns matching discoveries as JSON.
//
// Expected:
//   - Store implements the Query method.
//   - The receiver contains the desired discovery filters.
//
// Returns:
//   - A JSON array of matching discoveries.
//   - An error when querying or marshalling fails.
//
// Side effects:
//   - Queries the configured store.
func (t *QueryDiscoveriesTool) Run() (string, error) {
	q := recall.DiscoveryQuery{
		Kind:     t.Kind,
		Affects:  t.Affects,
		Priority: t.Priority,
	}
	results, err := t.Store.Query(q)
	if err != nil {
		return "", err
	}
	if results == nil {
		return "[]", nil
	}

	var filtered []*recall.Discovery
	for _, result := range results {
		if discovery, ok := result.(*recall.Discovery); ok {
			if t.matchesQuery(discovery) {
				filtered = append(filtered, discovery)
			}
		}
	}

	out, err := json.Marshal(filtered)
	if err != nil {
		return "", errors.New("failed to marshal discoveries")
	}
	return string(out), nil
}

// matchesQuery reports whether a discovery satisfies the configured filters.
//
// Expected:
//   - discovery is non-nil.
//
// Returns:
//   - True when the discovery matches all configured filters.
//   - False otherwise.
//
// Side effects:
//   - None.
func (t *QueryDiscoveriesTool) matchesQuery(discovery *recall.Discovery) bool {
	if t.Kind != "" && discovery.Kind != t.Kind {
		return false
	}
	if t.Affects != "" && discovery.Affects != t.Affects {
		return false
	}
	if t.Priority != "" && discovery.Priority != t.Priority {
		return false
	}
	return true
}
