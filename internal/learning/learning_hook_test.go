package learning_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"context"

	"github.com/baphled/flowstate/internal/learning"
)

var _ = Describe("LearningHook", func() {
	Context("when invoked with an execution context containing AgentID", func() {
		It("should populate AgentID in the learning record", func() {
			// Arrange: create a context with AgentID
			testAgentID := "agent-123"
			ctx := context.WithValue(context.Background(), learning.AgentIDKey, testAgentID)

			// Use a stub MemoryClient to capture what is written
			var capturedAgentID string
			stubClient := &stubMemoryClient{
				onWrite: func(record *learning.Record) error {
					capturedAgentID = record.AgentID
					return nil
				},
			}

			hook := learning.NewLearningHook(stubClient)

			// Act: invoke the hook
			err := hook.Handle(ctx, &learning.ToolCallResult{})

			// Assert: AgentID should be set in the record
			Expect(err).NotTo(HaveOccurred())
			Expect(capturedAgentID).To(Equal(testAgentID))
		})
	})
})

type stubMemoryClient struct {
	onWrite func(record *learning.Record) error
}

func (s *stubMemoryClient) CreateEntities(ctx context.Context, entities []learning.Entity) ([]learning.Entity, error) {
	return nil, nil
}

func (s *stubMemoryClient) CreateRelations(ctx context.Context, relations []learning.Relation) ([]learning.Relation, error) {
	return nil, nil
}

func (s *stubMemoryClient) AddObservations(ctx context.Context, observations []learning.ObservationEntry) ([]learning.ObservationEntry, error) {
	return nil, nil
}

func (s *stubMemoryClient) DeleteEntities(ctx context.Context, entityNames []string) ([]string, error) {
	return nil, nil
}

func (s *stubMemoryClient) DeleteObservations(ctx context.Context, deletions []learning.DeletionEntry) error {
	return nil
}

func (s *stubMemoryClient) DeleteRelations(ctx context.Context, relations []learning.Relation) error {
	return nil
}

func (s *stubMemoryClient) ReadGraph(ctx context.Context) (learning.KnowledgeGraph, error) {
	return learning.KnowledgeGraph{}, nil
}

func (s *stubMemoryClient) SearchNodes(ctx context.Context, query string) ([]learning.Entity, error) {
	return nil, nil
}

func (s *stubMemoryClient) OpenNodes(ctx context.Context, names []string) (learning.KnowledgeGraph, error) {
	return learning.KnowledgeGraph{}, nil
}

func (s *stubMemoryClient) WriteLearningRecord(record *learning.Record) error {
	if s.onWrite != nil {
		return s.onWrite(record)
	}
	return nil
}

var _ = Describe("LearningHook Integration", func() {
	Context("golden disc provider integration", func() {
		It("should persist learning records without calling external services", func() {
			// Arrange: Create a mock MemoryClient that tracks calls
			callCount := 0
			mockClient := &integrationMemoryClient{
				writeLearningCalls: 0,
				onWrite: func(record *learning.Record) error {
					callCount++
					Expect(record.AgentID).To(Equal("test-agent"))
					return nil
				},
			}

			hook := learning.NewLearningHook(mockClient)
			ctx := context.WithValue(context.Background(), learning.AgentIDKey, "test-agent")
			result := &learning.ToolCallResult{}

			// Act: Handle the tool call result
			err := hook.Handle(ctx, result)

			// Assert: No errors and exactly one write call
			Expect(err).NotTo(HaveOccurred())
			Expect(callCount).To(Equal(1))
		})

		It("should extract AgentID from context correctly", func() {
			testCases := []struct {
				agentID     string
				expectValue string
			}{
				{"agent-uuid-123", "agent-uuid-123"},
				{"orchestrator-1", "orchestrator-1"},
				{"", ""}, // AgentID not set
			}

			for _, tc := range testCases {
				mockClient := &integrationMemoryClient{
					onWrite: func(record *learning.Record) error {
						Expect(record.AgentID).To(Equal(tc.expectValue))
						return nil
					},
				}

				hook := learning.NewLearningHook(mockClient)
				ctx := context.WithValue(context.Background(), learning.AgentIDKey, tc.agentID)
				result := &learning.ToolCallResult{}

				err := hook.Handle(ctx, result)
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("should not make external API calls during Handle", func() {
			// This test ensures learning hook operates synchronously
			// and doesn't spawn goroutines that call external services
			mockClient := &integrationMemoryClient{
				onWrite: func(record *learning.Record) error {
					return nil
				},
			}

			hook := learning.NewLearningHook(mockClient)
			ctx := context.WithValue(context.Background(), learning.AgentIDKey, "test-agent")
			result := &learning.ToolCallResult{}

			// Act: Call Handle and immediately check state
			// If there are external calls, they should be visible here
			err := hook.Handle(ctx, result)
			Expect(err).NotTo(HaveOccurred())

			// All writes should be synchronous
			Expect(mockClient.writeLearningCalls).To(Equal(1))
		})
	})
})

// integrationMemoryClient is a mock MemoryClient for integration testing.
type integrationMemoryClient struct {
	writeLearningCalls int
	onWrite            func(record *learning.Record) error
}

func (c *integrationMemoryClient) CreateEntities(ctx context.Context, entities []learning.Entity) ([]learning.Entity, error) {
	return nil, nil
}

func (c *integrationMemoryClient) CreateRelations(ctx context.Context, relations []learning.Relation) ([]learning.Relation, error) {
	return nil, nil
}

func (c *integrationMemoryClient) AddObservations(ctx context.Context, observations []learning.ObservationEntry) ([]learning.ObservationEntry, error) {
	return nil, nil
}

func (c *integrationMemoryClient) DeleteEntities(ctx context.Context, entityNames []string) ([]string, error) {
	return nil, nil
}

func (c *integrationMemoryClient) DeleteObservations(ctx context.Context, deletions []learning.DeletionEntry) error {
	return nil
}

func (c *integrationMemoryClient) DeleteRelations(ctx context.Context, relations []learning.Relation) error {
	return nil
}

func (c *integrationMemoryClient) ReadGraph(ctx context.Context) (learning.KnowledgeGraph, error) {
	return learning.KnowledgeGraph{}, nil
}

func (c *integrationMemoryClient) SearchNodes(ctx context.Context, query string) ([]learning.Entity, error) {
	return nil, nil
}

func (c *integrationMemoryClient) OpenNodes(ctx context.Context, names []string) (learning.KnowledgeGraph, error) {
	return learning.KnowledgeGraph{}, nil
}

func (c *integrationMemoryClient) WriteLearningRecord(record *learning.Record) error {
	c.writeLearningCalls++
	if c.onWrite != nil {
		return c.onWrite(record)
	}
	return nil
}
