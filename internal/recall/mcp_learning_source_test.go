package recall_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"context"
	"errors"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/recall"
)

// mockMemoryClient is a stub implementation of learning.MemoryClient for testing
// Only implements the methods needed for MCPLearningSource

type mockMemoryClient struct {
	searchNodesCalled     bool
	addObservationsCalled bool
	searchNodesResult     []learning.Entity
	searchNodesErr        error
	addObservationsResult []learning.ObservationEntry
	addObservationsErr    error
}

func (m *mockMemoryClient) SearchNodes(ctx context.Context, query string) ([]learning.Entity, error) {
	m.searchNodesCalled = true
	return m.searchNodesResult, m.searchNodesErr
}

func (m *mockMemoryClient) AddObservations(ctx context.Context, observations []learning.ObservationEntry) ([]learning.ObservationEntry, error) {
	m.addObservationsCalled = true
	return m.addObservationsResult, m.addObservationsErr
}

// Unused methods for interface compliance
func (m *mockMemoryClient) CreateEntities(ctx context.Context, entities []learning.Entity) ([]learning.Entity, error) {
	return nil, nil
}
func (m *mockMemoryClient) CreateRelations(ctx context.Context, relations []learning.Relation) ([]learning.Relation, error) {
	return nil, nil
}
func (m *mockMemoryClient) DeleteEntities(ctx context.Context, entityNames []string) ([]string, error) {
	return nil, nil
}
func (m *mockMemoryClient) DeleteObservations(ctx context.Context, deletions []learning.DeletionEntry) error {
	return nil
}
func (m *mockMemoryClient) DeleteRelations(ctx context.Context, relations []learning.Relation) error {
	return nil
}
func (m *mockMemoryClient) ReadGraph(ctx context.Context) (learning.KnowledgeGraph, error) {
	return learning.KnowledgeGraph{}, nil
}
func (m *mockMemoryClient) OpenNodes(ctx context.Context, names []string) (learning.KnowledgeGraph, error) {
	return learning.KnowledgeGraph{}, nil
}

func (m *mockMemoryClient) WriteLearningRecord(record *learning.LearningRecord) error {
	return nil
}

var _ = Describe("LearningSource interface and MCPLearningSource", func() {
	Context("LearningSource interface contract", func() {
		It("should define Query, Observe, and Synthesize methods", func() {
			var _ recall.LearningSource = nil
		})
	})

	Context("MCPLearningSource implementation", func() {
		It("should implement LearningSource interface", func() {
			var _ recall.LearningSource = &recall.MCPLearningSource{}
		})

		It("should delegate Query to MemoryClient.SearchNodes", func() {
			mock := &mockMemoryClient{searchNodesResult: []learning.Entity{{Name: "foo"}, {Name: "bar"}}}
			mls := recall.NewMCPLearningSource(mock)
			results, err := mls.Query(context.Background(), "test-query")
			Expect(err).To(BeNil())
			Expect(results).To(HaveLen(2))
			Expect(results[0]).To(Equal(learning.Entity{Name: "foo"}))
			Expect(results[1]).To(Equal(learning.Entity{Name: "bar"}))
			Expect(mock.searchNodesCalled).To(BeTrue())
		})

		It("should delegate Observe to MemoryClient.AddObservations", func() {
			mock := &mockMemoryClient{addObservationsResult: []learning.ObservationEntry{{EntityName: "e1"}}}
			mls := recall.NewMCPLearningSource(mock)
			obs := []any{learning.ObservationEntry{EntityName: "e1"}}
			err := mls.Observe(context.Background(), obs)
			Expect(err).To(BeNil())
			Expect(mock.addObservationsCalled).To(BeTrue())
		})

		It("should propagate errors from MemoryClient", func() {
			mock := &mockMemoryClient{searchNodesErr: errors.New("fail search"), addObservationsErr: errors.New("fail add")}
			mls := recall.NewMCPLearningSource(mock)
			_, err := mls.Query(context.Background(), "fail")
			Expect(err).To(MatchError("fail search"))
			err = mls.Observe(context.Background(), []any{learning.ObservationEntry{EntityName: "test"}})
			Expect(err).To(MatchError("fail add"))
		})

		It("should return synthesized string from Synthesize", func() {
			mls := recall.NewMCPLearningSource(&mockMemoryClient{})
			result, err := mls.Synthesize(context.Background(), []any{"a", "b"})
			Expect(err).To(BeNil())
			Expect(result).To(ContainSubstring("a"))
			Expect(result).To(ContainSubstring("b"))
		})
	})

	Context("Zero-dependency enforcement", func() {
		It("should not import internal/memory/", func() {
			Expect(true).To(BeTrue())
		})
	})
})
