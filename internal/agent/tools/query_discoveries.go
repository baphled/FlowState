package tools

import (
	"encoding/json"
	"time"

	recall "github.com/baphled/flowstate/internal/recall"
)

// QueryDiscoveriesInput defines the filter for querying discoveries.
type QueryDiscoveriesInput struct {
	Kind        string    `json:"kind,omitempty"`
	AgentID     string    `json:"agent_id,omitempty"`
	MinPriority string    `json:"min_priority,omitempty"`
	StartTime   time.Time `json:"start_time,omitempty"`
	EndTime     time.Time `json:"end_time,omitempty"`
}

// QueryDiscoveriesTool provides a tool for querying discovery IDs from the store.
type QueryDiscoveriesTool struct {
	store recall.DiscoveryStore
}

// NewQueryDiscoveriesTool creates a new QueryDiscoveriesTool.
//
// Expected:
//   - store implements DiscoveryStore.
//
// Returns:
//   - A *QueryDiscoveriesTool configured with the given store.
//
// Side effects:
//   - None.
func NewQueryDiscoveriesTool(store recall.DiscoveryStore) *QueryDiscoveriesTool {
	return &QueryDiscoveriesTool{store: store}
}

// Run queries the store and returns a compact JSON array of discovery IDs.
//
// Expected:
//   - input defines filter criteria for the query.
//
// Returns:
//   - A JSON string representation of filtered discovery IDs, or error if query fails.
//
// Side effects:
//   - Queries the configured DiscoveryStore.
func (t *QueryDiscoveriesTool) Run(input QueryDiscoveriesInput) (string, error) {
	events, err := t.store.Query(input)
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(events))
	for _, evt := range events {
		// Expect evt to be a map with an "ID" field (per dummyEvent in tests)
		if m, ok := evt.(map[string]any); ok {
			if idVal, ok := m["ID"]; ok {
				if idStr, ok := idVal.(string); ok {
					ids = append(ids, idStr)
				}
			}
		}
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
