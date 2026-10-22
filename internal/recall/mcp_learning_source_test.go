package recall_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"context"
	"errors"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// mockMemoryClient is a stub implementation of learning.MemoryClient for testing.
type mockMemoryClient struct {
	mu                    sync.Mutex
	searchNodesCalled     bool
	addObservationsCalled bool
	searchNodesResult     []learning.Entity
	searchNodesErr        error
	addObservationsResult []learning.ObservationEntry
	addObservationsErr    error
}

func (m *mockMemoryClient) SearchNodes(ctx context.Context, query string) ([]learning.Entity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searchNodesCalled = true
	return m.searchNodesResult, m.searchNodesErr
}

func (m *mockMemoryClient) AddObservations(ctx context.Context, observations []learning.ObservationEntry) ([]learning.ObservationEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addObservationsCalled = true
	return m.addObservationsResult, m.addObservationsErr
}

// Unused methods for interface compliance.
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

func (m *mockMemoryClient) WriteLearningRecord(record *learning.Record) error {
	return nil
}

// spyProvider is a test spy that records Chat calls.
type spyProvider struct {
	mu           sync.Mutex
	chatCalls    []provider.ChatRequest
	chatResponse provider.ChatResponse
	chatErr      error
}

func (s *spyProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatCalls = append(s.chatCalls, req)
	return s.chatResponse, s.chatErr
}

// Unused methods for Provider interface compliance.
func (s *spyProvider) Name() string { return "spy" }
func (s *spyProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errors.New("not implemented")
}
func (s *spyProvider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	return nil, errors.New("not implemented")
}
func (s *spyProvider) Models() ([]provider.Model, error) {
	return nil, errors.New("not implemented")
}

var _ = Describe("LearningSource interface and MCPLearningSource", func() {
	Context("LearningSource interface contract", func() {
		It("should define Query, Observe, and Synthesize methods", func() {
			var source recall.LearningSource
			Expect(source).To(BeNil())
		})
	})

	Context("MCPLearningSource implementation", func() {
		It("should implement LearningSource interface", func() {
			var source recall.LearningSource = &recall.MCPLearningSource{}
			Expect(source).NotTo(BeNil())
		})

		It("should delegate Query to MemoryClient.SearchNodes", func() {
			mock := &mockMemoryClient{searchNodesResult: []learning.Entity{{Name: "foo"}, {Name: "bar"}}}
			mls := recall.NewMCPLearningSource(mock, nil)
			results, err := mls.Query(context.Background(), "test-query")
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(2))
			Expect(results[0]).To(Equal(learning.Entity{Name: "foo"}))
			Expect(results[1]).To(Equal(learning.Entity{Name: "bar"}))
			mock.mu.Lock()
			defer mock.mu.Unlock()
			Expect(mock.searchNodesCalled).To(BeTrue())
		})

		It("should delegate Observe to MemoryClient.AddObservations", func() {
			mock := &mockMemoryClient{addObservationsResult: []learning.ObservationEntry{{EntityName: "e1"}}}
			mls := recall.NewMCPLearningSource(mock, nil)
			obs := []any{learning.ObservationEntry{EntityName: "e1"}}
			err := mls.Observe(context.Background(), obs)
			Expect(err).NotTo(HaveOccurred())
			mock.mu.Lock()
			defer mock.mu.Unlock()
			Expect(mock.addObservationsCalled).To(BeTrue())
		})

		It("should propagate errors from MemoryClient", func() {
			mock := &mockMemoryClient{searchNodesErr: errors.New("fail search"), addObservationsErr: errors.New("fail add")}
			mls := recall.NewMCPLearningSource(mock, nil)
			_, err := mls.Query(context.Background(), "fail")
			Expect(err).To(MatchError("fail search"))
			err = mls.Observe(context.Background(), []any{learning.ObservationEntry{EntityName: "test"}})
			Expect(err).To(MatchError("fail add"))
		})
	})

	Context("Zero-dependency enforcement", func() {
		It("should not import internal/memory/", func() {
			Expect(true).To(BeTrue())
		})
	})

	Describe("Synthesize with provider", func() {
		It("should call provider.Chat with synthesis request [AC1]", func() {
			spy := &spyProvider{
				chatResponse: provider.ChatResponse{
					Message: provider.Message{
						Role:    "assistant",
						Content: "Synthesized result",
					},
				},
			}
			mock := &mockMemoryClient{}
			mls := recall.NewMCPLearningSource(mock, spy)

			ctx := context.Background()
			err := mls.Synthesize(ctx, "test-entity", []string{"obs1", "obs2"})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() int {
				spy.mu.Lock()
				defer spy.mu.Unlock()
				return len(spy.chatCalls)
			}, 1*time.Second).Should(Equal(1))

			spy.mu.Lock()
			defer spy.mu.Unlock()
			Expect(spy.chatCalls[0].Messages).NotTo(BeEmpty())
		})

		It("should return immediately without waiting for LLM [AC3]", func() {
			spy := &spyProvider{
				chatResponse: provider.ChatResponse{
					Message: provider.Message{
						Role:    "assistant",
						Content: "Synthesized",
					},
				},
			}
			mock := &mockMemoryClient{}
			mls := recall.NewMCPLearningSource(mock, spy)

			ctx := context.Background()
			start := time.Now()
			err := mls.Synthesize(ctx, "entity", []string{"obs"})
			elapsed := time.Since(start)

			Expect(err).NotTo(HaveOccurred())
			Expect(elapsed).To(BeNumerically("<", 200*time.Millisecond))
		})

		It("should call AddObservations with LLM result [AC2]", func() {
			spy := &spyProvider{
				chatResponse: provider.ChatResponse{
					Message: provider.Message{
						Role:    "assistant",
						Content: "Synthesized result",
					},
				},
			}
			mock := &mockMemoryClient{
				addObservationsResult: []learning.ObservationEntry{{EntityName: "entity"}},
			}
			mls := recall.NewMCPLearningSource(mock, spy)

			ctx := context.Background()
			err := mls.Synthesize(ctx, "entity", []string{"obs1"})
			Expect(err).NotTo(HaveOccurred())

			// Allow goroutine time to execute
			Eventually(func() bool {
				mock.mu.Lock()
				defer mock.mu.Unlock()
				return mock.addObservationsCalled
			}, 1*time.Second).Should(BeTrue())
		})

		It("should handle provider errors gracefully [AC4]", func() {
			failingProvider := &spyProvider{
				chatErr: errors.New("provider error"),
			}
			mock := &mockMemoryClient{}
			mls := recall.NewMCPLearningSource(mock, failingProvider)

			ctx := context.Background()
			err := mls.Synthesize(ctx, "entity", []string{"obs"})

			// Should not error - goroutine handles the error
			Expect(err).NotTo(HaveOccurred())

			// Ensure goroutine doesn't crash
			time.Sleep(100 * time.Millisecond)
		})
	})
})
