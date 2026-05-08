package learning_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"context"
	"encoding/json"
	"fmt"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/mcp"
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

// panickingError is an error implementation whose Error method dereferences a
// nil pointer, simulating a misbehaving error returned by an upstream
// dependency. It exists to pin the defence-in-depth behaviour of Hook.Handle:
// a panic from Error() must not escape the hook goroutine.
type panickingError struct {
	inner *struct{ msg string }
}

func (e *panickingError) Error() string {
	// Force a nil pointer dereference, mirroring the runtime error signature
	// observed in the April 2026 panic.
	return e.inner.msg
}

var _ = Describe("LearningHook panic safety (regression for April 2026 nil pointer panic)", func() {
	Context("when the MemoryClient returns a typed-nil-Type *json.UnmarshalTypeError", func() {
		It("should return an error whose Error() method does not panic", func() {
			// This pins the original bug: MCPMemoryClient previously constructed
			// `&json.UnmarshalTypeError{Value: "MCP tool error", Type: nil}`;
			// the stdlib Error() method dereferences Type. Calling Error()
			// (or any code path that does, e.g. errors.Is, joining errors,
			// custom log handlers without %v's recover) panics with
			// "runtime error: invalid memory address or nil pointer dereference",
			// which is exactly the signature observed in flowstate.log on
			// 2026-04-27T20:12:11 and the five rapid-fire occurrences that
			// followed.
			poisoned := &json.UnmarshalTypeError{Value: "MCP tool error", Type: nil}
			stubClient := &stubMemoryClient{
				onWrite: func(record *learning.Record) error {
					return poisoned
				},
			}
			hook := learning.NewLearningHook(stubClient)
			ctx := context.Background()

			err := hook.Handle(ctx, &learning.ToolCallResult{})

			Expect(err).To(HaveOccurred())
			// Calling Error() on the surfaced error must not panic.
			Expect(func() {
				_ = err.Error()
			}).NotTo(Panic())
		})
	})

	Context("when the MemoryClient returns an error whose Error() method panics", func() {
		It("should not let the panic escape the hook goroutine", func() {
			stubClient := &stubMemoryClient{
				onWrite: func(record *learning.Record) error {
					return &panickingError{inner: nil}
				},
			}
			hook := learning.NewLearningHook(stubClient)
			ctx := context.Background()

			Expect(func() {
				err := hook.Handle(ctx, &learning.ToolCallResult{})
				// The hook must absorb the panic and return either nil or
				// an error whose Error() is itself safe.
				if err != nil {
					_ = err.Error()
				}
			}).NotTo(Panic())
		})
	})

	Context("when the MCP memory client receives an IsError tool result", func() {
		It("should return an error that is safe to log via Error() / fmt.Sprintf", func() {
			// Pins the source side: every WriteLearningRecord failure path that
			// the live system can actually exercise (CreateEntities → IsError)
			// must yield an error with a working Error() method, not a
			// typed-nil reflect.Type that panics on dereference.
			client := &mockMCPClient{
				result: &mcp.ToolResult{Content: "boom", IsError: true},
			}
			mem := &learning.MCPMemoryClient{
				MCPClient: client,
				MCPServer: "memory",
			}
			err := mem.WriteLearningRecord(&learning.Record{
				AgentID:   "agent-1",
				ToolsUsed: []string{"tool-a"},
				Outcome:   "completed",
			})
			Expect(err).To(HaveOccurred())
			Expect(func() {
				_ = err.Error()
				_ = fmt.Sprintf("%v", err)
			}).NotTo(Panic())
		})
	})
})

var _ = Describe("LearningHook (AC2-AC4 RED phase)", func() {
	Context("populating ToolsUsed from call stack", func() {
		It("should populate ToolsUsed in the learning record from execution context", func() {
			stubClient := &stubMemoryClient{
				onWrite: func(record *learning.Record) error {
					Expect(record.ToolsUsed).NotTo(BeEmpty())
					return nil
				},
			}
			hook := learning.NewLearningHook(stubClient)
			ctx := context.Background()
			_ = hook.Handle(ctx, &learning.ToolCallResult{})
		})
	})

	Context("populating Outcome from ToolCallResult", func() {
		It("should populate Outcome in the learning record from ToolCallResult.Outcome field", func() {
			stubClient := &stubMemoryClient{
				onWrite: func(record *learning.Record) error {
					Expect(record.Outcome).To(Equal("success"))
					return nil
				},
			}
			hook := learning.NewLearningHook(stubClient)
			ctx := context.Background()
			result := &learning.ToolCallResult{Outcome: "success"}
			_ = hook.Handle(ctx, result)
		})
	})

	Context("comprehensive field population", func() {
		It("should populate all three fields (AgentID, ToolsUsed, Outcome) in one call", func() {
			testAgentID := "agent-test-123"
			stubClient := &stubMemoryClient{
				onWrite: func(record *learning.Record) error {
					Expect(record.AgentID).To(Equal(testAgentID))
					Expect(record.ToolsUsed).ToNot(BeEmpty())
					Expect(record.Outcome).To(Equal("completed"))
					return nil
				},
			}
			hook := learning.NewLearningHook(stubClient)
			ctx := context.WithValue(context.Background(), learning.AgentIDKey, testAgentID)
			result := &learning.ToolCallResult{Outcome: "completed"}
			_ = hook.Handle(ctx, result)
		})
	})
})
