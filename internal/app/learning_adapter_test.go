package app

import (
	"context"
	"errors"
	"testing"

	"github.com/baphled/flowstate/internal/recall"
)

// stubRecallBroker is a minimal recall.Broker double recording queries.
type stubRecallBroker struct {
	observations []recall.Observation
	err          error
	queries      []string
}

// Query records the query and returns canned observations or an error.
func (s *stubRecallBroker) Query(ctx context.Context, query string, limit int) ([]recall.Observation, error) {
	s.queries = append(s.queries, query)
	if s.err != nil {
		return nil, s.err
	}
	if len(s.observations) > limit {
		return s.observations[:limit], nil
	}
	return s.observations, nil
}

func TestSessionStartAdapterSearchMapsObservations(t *testing.T) {
	broker := &stubRecallBroker{observations: []recall.Observation{
		{ID: "o1", Content: "prefer table-driven tests", AgentID: "scribe"},
		{ID: "o2", Content: "commit via make ai-commit", AgentID: "scribe"},
	}}
	adapter := NewSessionStartLearningAdapter(broker, 0)
	matches, err := adapter.Search("scribe learnings", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("want 2 matches, got %d", len(matches))
	}
	if matches[0].ID != "o1" || matches[0].AgentID != "scribe" || matches[0].Content != "prefer table-driven tests" {
		t.Fatalf("unexpected first match: %+v", matches[0])
	}
	if len(broker.queries) != 1 || broker.queries[0] != "scribe learnings" {
		t.Fatalf("unexpected broker queries: %v", broker.queries)
	}
}

func TestSessionStartAdapterDefaultsLimit(t *testing.T) {
	obs := make([]recall.Observation, 8)
	for i := range obs {
		obs[i] = recall.Observation{ID: string(rune('a' + i))}
	}
	adapter := NewSessionStartLearningAdapter(&stubRecallBroker{observations: obs}, 0)
	matches, err := adapter.Search("", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 5 {
		t.Fatalf("want default limit of 5, got %d", len(matches))
	}
}

func TestSessionStartAdapterNilBrokerReturnsEmpty(t *testing.T) {
	adapter := NewSessionStartLearningAdapter(nil, 3)
	matches, err := adapter.Search("anything", 3)
	if err != nil {
		t.Fatalf("nil broker must degrade, got error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("want empty matches, got %d", len(matches))
	}
}

func TestSessionStartAdapterPropagatesBrokerError(t *testing.T) {
	sentinel := errors.New("broker down")
	adapter := NewSessionStartLearningAdapter(&stubRecallBroker{err: sentinel}, 3)
	if _, err := adapter.Search("q", 3); err == nil {
		t.Fatal("want error propagation from broker")
	}
}
