package streaming_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
)

type mockStreamer struct {
	chunks []provider.StreamChunk
	err    error
}

func (m *mockStreamer) Stream(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan provider.StreamChunk, len(m.chunks))
	for i := range m.chunks {
		ch <- m.chunks[i]
	}
	close(ch)
	return ch, nil
}

type mockConsumer struct {
	chunks       []string
	toolCalls    []string
	toolResults  []string
	errors       []error
	doneCount    int
	writeErr     error
	enableTool   bool
	enableResult bool
}

func (m *mockConsumer) WriteChunk(content string) error {
	m.chunks = append(m.chunks, content)
	return m.writeErr
}

func (m *mockConsumer) WriteError(err error) {
	m.errors = append(m.errors, err)
}

func (m *mockConsumer) Done() {
	m.doneCount++
}

func (m *mockConsumer) WriteToolCall(name string) {
	if m.enableTool {
		m.toolCalls = append(m.toolCalls, name)
	}
}

func (m *mockConsumer) WriteToolResult(content string) {
	if m.enableResult {
		m.toolResults = append(m.toolResults, content)
	}
}

type mockRegistry struct {
	manifests map[string]*agent.Manifest
}

func (m *mockRegistry) Get(id string) (*agent.Manifest, bool) {
	manifest, ok := m.manifests[id]
	return manifest, ok
}

type mockHarness struct {
	result       *plan.EvaluationResult
	err          error
	streamChunks []provider.StreamChunk
}

func (m *mockHarness) Evaluate(
	_ context.Context,
	_ streaming.Streamer,
	_ string,
	_ string,
) (*plan.EvaluationResult, error) {
	return m.result, m.err
}

func (m *mockHarness) StreamEvaluate(
	_ context.Context,
	_ streaming.Streamer,
	_ string,
	_ string,
) (<-chan provider.StreamChunk, error) {
	if m.err != nil {
		return nil, m.err
	}
	if len(m.streamChunks) > 0 {
		ch := make(chan provider.StreamChunk, len(m.streamChunks))
		for i := range m.streamChunks {
			ch <- m.streamChunks[i]
		}
		close(ch)
		return ch, nil
	}
	return streaming.PlanResultToChannel(m.result), nil
}

type mockEventConsumer struct {
	chunks    []string
	errors    []error
	events    []streaming.Event
	doneCount int
	writeErr  error
	eventErr  error
}

func (m *mockEventConsumer) WriteChunk(content string) error {
	m.chunks = append(m.chunks, content)
	return m.writeErr
}

func (m *mockEventConsumer) WriteError(err error) {
	m.errors = append(m.errors, err)
}

func (m *mockEventConsumer) Done() {
	m.doneCount++
}

func (m *mockEventConsumer) WriteEvent(event streaming.Event) error {
	m.events = append(m.events, event)
	return m.eventErr
}

type mockDelegationConsumer struct {
	chunks      []string
	errors      []error
	delegations []streaming.DelegationEvent
	doneCount   int
	writeErr    error
	delegateErr error
}

func (m *mockDelegationConsumer) WriteChunk(content string) error {
	m.chunks = append(m.chunks, content)
	return m.writeErr
}

func (m *mockDelegationConsumer) WriteError(err error) {
	m.errors = append(m.errors, err)
}

func (m *mockDelegationConsumer) Done() {
	m.doneCount++
}

func (m *mockDelegationConsumer) WriteDelegation(event streaming.DelegationEvent) error {
	m.delegations = append(m.delegations, event)
	return m.delegateErr
}

type mockHarnessConsumer struct {
	chunks          []string
	toolCalls       []string
	toolResults     []string
	errors          []error
	retryContent    []string
	attemptStarts   []string
	completes       []string
	criticFeedbacks []string
	doneCount       int
	writeErr        error
	enableTool      bool
	enableResult    bool
}

func (m *mockHarnessConsumer) WriteChunk(content string) error {
	m.chunks = append(m.chunks, content)
	return m.writeErr
}

func (m *mockHarnessConsumer) WriteError(err error) {
	m.errors = append(m.errors, err)
}

func (m *mockHarnessConsumer) Done() {
	m.doneCount++
}

func (m *mockHarnessConsumer) WriteToolCall(name string) {
	if m.enableTool {
		m.toolCalls = append(m.toolCalls, name)
	}
}

func (m *mockHarnessConsumer) WriteToolResult(content string) {
	if m.enableResult {
		m.toolResults = append(m.toolResults, content)
	}
}

func (m *mockHarnessConsumer) WriteHarnessRetry(content string) {
	m.retryContent = append(m.retryContent, content)
}

func (m *mockHarnessConsumer) WriteAttemptStart(content string) {
	m.attemptStarts = append(m.attemptStarts, content)
}

func (m *mockHarnessConsumer) WriteComplete(content string) {
	m.completes = append(m.completes, content)
}

func (m *mockHarnessConsumer) WriteCriticFeedback(content string) {
	m.criticFeedbacks = append(m.criticFeedbacks, content)
}

var _ = Describe("Streaming", func() {
	Describe("Interface compliance", func() {
		It("engine.Engine satisfies streaming.Streamer at compile time", func() {
			var _ streaming.Streamer = (*engine.Engine)(nil)
		})
	})

	Describe("Run", func() {
		var (
			ctx      context.Context
			streamer *mockStreamer
			consumer *mockConsumer
		)

		BeforeEach(func() {
			ctx = context.Background()
			streamer = &mockStreamer{}
			consumer = &mockConsumer{}
		})

		Context("when streaming succeeds with content chunks", func() {
			BeforeEach(func() {
				streamer.chunks = []provider.StreamChunk{
					{Content: "hello "},
					{Content: "world"},
					{Done: true},
				}
			})

			It("delivers all content to the consumer", func() {
				err := streaming.Run(ctx, streamer, consumer, "test-agent", "test message")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.chunks).To(Equal([]string{"hello ", "world"}))
			})

			It("calls Done on the consumer", func() {
				err := streaming.Run(ctx, streamer, consumer, "test-agent", "test message")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.doneCount).To(Equal(1))
			})
		})

		Context("when Stream returns an error", func() {
			BeforeEach(func() {
				streamer.err = errors.New("stream failed")
			})

			It("calls WriteError and returns the error", func() {
				err := streaming.Run(ctx, streamer, consumer, "test-agent", "test message")
				Expect(err).To(MatchError("stream failed"))
				Expect(consumer.errors).To(HaveLen(1))
				Expect(consumer.errors[0]).To(MatchError("stream failed"))
			})

			It("calls Done even when Stream fails", func() {
				_ = streaming.Run(ctx, streamer, consumer, "test-agent", "test message")
				Expect(consumer.doneCount).To(Equal(1))
			})
		})

		Context("when a chunk carries an error", func() {
			BeforeEach(func() {
				streamer.chunks = []provider.StreamChunk{
					{Content: "partial"},
					{Error: errors.New("chunk error"), Done: true},
				}
			})

			It("delivers the error to the consumer via WriteError", func() {
				err := streaming.Run(ctx, streamer, consumer, "test-agent", "test message")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.chunks).To(Equal([]string{"partial"}))
				Expect(consumer.errors).To(HaveLen(1))
				Expect(consumer.errors[0]).To(MatchError("chunk error"))
			})
		})

		Context("when WriteChunk returns an error", func() {
			BeforeEach(func() {
				consumer.writeErr = errors.New("write failed")
				streamer.chunks = []provider.StreamChunk{
					{Content: "data"},
					{Done: true},
				}
			})

			It("returns the WriteChunk error", func() {
				err := streaming.Run(ctx, streamer, consumer, "test-agent", "test message")
				Expect(err).To(MatchError("write failed"))
			})

			It("still calls Done on the consumer", func() {
				_ = streaming.Run(ctx, streamer, consumer, "test-agent", "test message")
				Expect(consumer.doneCount).To(Equal(1))
			})
		})

		Context("when a chunk carries a tool call", func() {
			BeforeEach(func() {
				consumer.enableTool = true
				streamer.chunks = []provider.StreamChunk{
					{ToolCall: &provider.ToolCall{ID: "call1", Name: "bash"}},
					{Content: "result", Done: true},
				}
			})

			It("calls WriteToolCall on ToolCallConsumer implementations", func() {
				err := streaming.Run(ctx, streamer, consumer, "test-agent", "test message")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.toolCalls).To(Equal([]string{"bash"}))
			})
		})

		Context("when a chunk carries a skill_load tool call", func() {
			BeforeEach(func() {
				consumer.enableTool = true
				streamer.chunks = []provider.StreamChunk{
					{ToolCall: &provider.ToolCall{
						ID:   "call1",
						Name: "skill_load",
						Arguments: map[string]interface{}{
							"name": "pre-action",
						},
					}},
					{Content: "result", Done: true},
				}
			})

			It("extracts skill name and passes skill:name to WriteToolCall", func() {
				err := streaming.Run(ctx, streamer, consumer, "test-agent", "test message")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.toolCalls).To(Equal([]string{"skill:pre-action"}))
			})
		})

		Context("when a chunk carries a skill_load without name argument", func() {
			BeforeEach(func() {
				consumer.enableTool = true
				streamer.chunks = []provider.StreamChunk{
					{ToolCall: &provider.ToolCall{
						ID:        "call1",
						Name:      "skill_load",
						Arguments: map[string]interface{}{},
					}},
					{Content: "result", Done: true},
				}
			})

			It("passes skill_load as-is to WriteToolCall", func() {
				err := streaming.Run(ctx, streamer, consumer, "test-agent", "test message")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.toolCalls).To(Equal([]string{"skill_load"}))
			})
		})

		Context("when a chunk carries a tool result", func() {
			BeforeEach(func() {
				consumer.enableResult = true
				streamer.chunks = []provider.StreamChunk{
					{EventType: "tool_result", ToolResult: &provider.ToolResultInfo{Content: "output"}},
					{Content: "final", Done: true},
				}
			})

			It("calls WriteToolResult on ToolResultConsumer implementations", func() {
				err := streaming.Run(ctx, streamer, consumer, "test-agent", "test message")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.toolResults).To(Equal([]string{"output"}))
			})
		})
	})

	Describe("HarnessStreamer", func() {
		var (
			ctx      context.Context
			inner    *mockStreamer
			registry *mockRegistry
			harness  *mockHarness
		)

		BeforeEach(func() {
			ctx = context.Background()
			inner = &mockStreamer{}
			registry = &mockRegistry{manifests: make(map[string]*agent.Manifest)}
			harness = &mockHarness{}
		})

		Context("when agent has HarnessEnabled=false", func() {
			BeforeEach(func() {
				registry.manifests["test-agent"] = &agent.Manifest{
					ID:             "test-agent",
					Name:           "Test Agent",
					HarnessEnabled: false,
				}
				inner.chunks = []provider.StreamChunk{
					{Content: "passthrough"},
					{Done: true},
				}
			})

			It("passes through to the inner streamer", func() {
				hs := streaming.NewHarnessStreamer(inner, harness, registry)
				ch, err := hs.Stream(ctx, "test-agent", "hello")
				Expect(err).NotTo(HaveOccurred())

				var chunks []provider.StreamChunk
				for c := range ch {
					chunks = append(chunks, c)
				}
				Expect(chunks).To(HaveLen(2))
				Expect(chunks[0].Content).To(Equal("passthrough"))
				Expect(chunks[1].Done).To(BeTrue())
			})
		})

		Context("when agent has HarnessEnabled=true", func() {
			BeforeEach(func() {
				registry.manifests["planner"] = &agent.Manifest{
					ID:             "planner",
					Name:           "Planner",
					HarnessEnabled: true,
				}
				harness.result = &plan.EvaluationResult{
					PlanText:     "validated plan output",
					AttemptCount: 1,
					FinalScore:   0.95,
				}
			})

			It("routes through the harness and streams the result", func() {
				hs := streaming.NewHarnessStreamer(inner, harness, registry)
				ch, err := hs.Stream(ctx, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())

				var chunks []provider.StreamChunk
				for c := range ch {
					chunks = append(chunks, c)
				}
				Expect(chunks).To(HaveLen(2))
				Expect(chunks[0].Content).To(Equal("validated plan output"))
				Expect(chunks[1].Done).To(BeTrue())
			})
		})

		Context("when agent is not found in registry", func() {
			BeforeEach(func() {
				inner.chunks = []provider.StreamChunk{
					{Content: "fallback"},
					{Done: true},
				}
			})

			It("passes through to the inner streamer", func() {
				hs := streaming.NewHarnessStreamer(inner, harness, registry)
				ch, err := hs.Stream(ctx, "unknown-agent", "hello")
				Expect(err).NotTo(HaveOccurred())

				var chunks []provider.StreamChunk
				for c := range ch {
					chunks = append(chunks, c)
				}
				Expect(chunks).To(HaveLen(2))
				Expect(chunks[0].Content).To(Equal("fallback"))
			})
		})

		Context("when harness.Evaluate returns an error", func() {
			BeforeEach(func() {
				registry.manifests["planner"] = &agent.Manifest{
					ID:             "planner",
					Name:           "Planner",
					HarnessEnabled: true,
				}
				harness.err = errors.New("harness evaluation failed")
			})

			It("propagates the error", func() {
				hs := streaming.NewHarnessStreamer(inner, harness, registry)
				ch, err := hs.Stream(ctx, "planner", "create a plan")
				Expect(err).To(MatchError("harness evaluation failed"))
				Expect(ch).To(BeNil())
			})
		})

		Context("when StreamEvaluate emits live chunks", func() {
			BeforeEach(func() {
				registry.manifests["planner"] = &agent.Manifest{
					ID:             "planner",
					Name:           "Planner",
					HarnessEnabled: true,
				}
				harness.streamChunks = []provider.StreamChunk{
					{Content: "chunk-1 "},
					{Content: "chunk-2 "},
					{Content: "chunk-3"},
					{Done: true},
				}
			})

			It("delivers live chunks from StreamEvaluate", func() {
				hs := streaming.NewHarnessStreamer(inner, harness, registry)
				ch, err := hs.Stream(ctx, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())

				var contents []string
				for c := range ch {
					if c.Content != "" {
						contents = append(contents, c.Content)
					}
				}
				Expect(contents).To(Equal([]string{"chunk-1 ", "chunk-2 ", "chunk-3"}))
			})

			It("does not delegate to the inner streamer", func() {
				inner.chunks = []provider.StreamChunk{
					{Content: "should-not-appear"},
					{Done: true},
				}
				hs := streaming.NewHarnessStreamer(inner, harness, registry)
				ch, err := hs.Stream(ctx, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())

				var contents []string
				for c := range ch {
					if c.Content != "" {
						contents = append(contents, c.Content)
					}
				}
				Expect(contents).NotTo(ContainElement("should-not-appear"))
			})
		})
	})

	Describe("Run with harness_retry events", func() {
		var (
			ctx      context.Context
			streamer *mockStreamer
			consumer *mockHarnessConsumer
		)

		BeforeEach(func() {
			ctx = context.Background()
			streamer = &mockStreamer{}
			consumer = &mockHarnessConsumer{}
		})

		Context("when the stream contains a harness_retry event", func() {
			BeforeEach(func() {
				streamer.chunks = []provider.StreamChunk{
					{Content: "initial "},
					{EventType: "harness_retry", Content: "schema validation failed: missing title"},
					{Content: "retried output"},
					{Done: true},
				}
			})

			It("calls WriteHarnessRetry on the consumer", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.retryContent).To(HaveLen(1))
				Expect(consumer.retryContent[0]).To(Equal("schema validation failed: missing title"))
			})

			It("does not deliver retry event content as a regular chunk", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.chunks).To(Equal([]string{"initial ", "retried output"}))
			})

			It("still calls Done on the consumer", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.doneCount).To(Equal(1))
			})
		})

		Context("when the consumer does not implement HarnessEventConsumer", func() {
			It("silently skips harness_retry events", func() {
				plainConsumer := &mockConsumer{}
				streamer.chunks = []provider.StreamChunk{
					{Content: "before "},
					{EventType: "harness_retry", Content: "retry info"},
					{Content: "after"},
					{Done: true},
				}
				err := streaming.Run(ctx, streamer, plainConsumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(plainConsumer.chunks).To(Equal([]string{"before ", "after"}))
			})
		})

		Context("when multiple harness_retry events occur", func() {
			BeforeEach(func() {
				streamer.chunks = []provider.StreamChunk{
					{Content: "attempt-1 "},
					{EventType: "harness_retry", Content: "retry-1: missing field"},
					{Content: "attempt-2 "},
					{EventType: "harness_retry", Content: "retry-2: wrong format"},
					{Content: "attempt-3"},
					{Done: true},
				}
			})

			It("delivers all retry events to the consumer", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.retryContent).To(Equal([]string{
					"retry-1: missing field",
					"retry-2: wrong format",
				}))
			})

			It("delivers all non-retry content chunks", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.chunks).To(Equal([]string{
					"attempt-1 ",
					"attempt-2 ",
					"attempt-3",
				}))
			})
		})
	})

	Describe("Run with harness_attempt_start events", func() {
		var (
			ctx      context.Context
			streamer *mockStreamer
			consumer *mockHarnessConsumer
		)

		BeforeEach(func() {
			ctx = context.Background()
			streamer = &mockStreamer{}
			consumer = &mockHarnessConsumer{}
		})

		Context("when the stream contains a harness_attempt_start event", func() {
			BeforeEach(func() {
				streamer.chunks = []provider.StreamChunk{
					{EventType: "harness_attempt_start", Content: "attempt 2 of 3"},
					{Content: "plan output"},
					{Done: true},
				}
			})

			It("calls WriteAttemptStart on the consumer", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.attemptStarts).To(HaveLen(1))
				Expect(consumer.attemptStarts[0]).To(Equal("attempt 2 of 3"))
			})

			It("does not deliver attempt start content as a regular chunk", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.chunks).To(Equal([]string{"plan output"}))
			})
		})

		Context("when the consumer does not implement HarnessEventConsumer", func() {
			It("silently skips harness_attempt_start events", func() {
				plainConsumer := &mockConsumer{}
				streamer.chunks = []provider.StreamChunk{
					{Content: "before "},
					{EventType: "harness_attempt_start", Content: "attempt 1"},
					{Content: "after"},
					{Done: true},
				}
				err := streaming.Run(ctx, streamer, plainConsumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(plainConsumer.chunks).To(Equal([]string{"before ", "after"}))
			})
		})
	})

	Describe("Run with harness_complete events", func() {
		var (
			ctx      context.Context
			streamer *mockStreamer
			consumer *mockHarnessConsumer
		)

		BeforeEach(func() {
			ctx = context.Background()
			streamer = &mockStreamer{}
			consumer = &mockHarnessConsumer{}
		})

		Context("when the stream contains a harness_complete event", func() {
			BeforeEach(func() {
				streamer.chunks = []provider.StreamChunk{
					{Content: "plan text "},
					{EventType: "harness_complete", Content: "score: 0.95, attempts: 2"},
					{Done: true},
				}
			})

			It("calls WriteComplete on the consumer", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.completes).To(HaveLen(1))
				Expect(consumer.completes[0]).To(Equal("score: 0.95, attempts: 2"))
			})

			It("does not deliver complete content as a regular chunk", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.chunks).To(Equal([]string{"plan text "}))
			})
		})

		Context("when the consumer does not implement HarnessEventConsumer", func() {
			It("silently skips harness_complete events", func() {
				plainConsumer := &mockConsumer{}
				streamer.chunks = []provider.StreamChunk{
					{Content: "before "},
					{EventType: "harness_complete", Content: "done"},
					{Content: "after"},
					{Done: true},
				}
				err := streaming.Run(ctx, streamer, plainConsumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(plainConsumer.chunks).To(Equal([]string{"before ", "after"}))
			})
		})
	})

	Describe("Run with harness_critic_feedback events", func() {
		var (
			ctx      context.Context
			streamer *mockStreamer
			consumer *mockHarnessConsumer
		)

		BeforeEach(func() {
			ctx = context.Background()
			streamer = &mockStreamer{}
			consumer = &mockHarnessConsumer{}
		})

		Context("when the stream contains a harness_critic_feedback event", func() {
			BeforeEach(func() {
				streamer.chunks = []provider.StreamChunk{
					{Content: "plan draft "},
					{EventType: "harness_critic_feedback", Content: "missing error handling section"},
					{Content: "revised plan"},
					{Done: true},
				}
			})

			It("calls WriteCriticFeedback on the consumer", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.criticFeedbacks).To(HaveLen(1))
				Expect(consumer.criticFeedbacks[0]).To(Equal("missing error handling section"))
			})

			It("does not deliver critic feedback content as a regular chunk", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.chunks).To(Equal([]string{"plan draft ", "revised plan"}))
			})
		})

		Context("when the consumer does not implement HarnessEventConsumer", func() {
			It("silently skips harness_critic_feedback events", func() {
				plainConsumer := &mockConsumer{}
				streamer.chunks = []provider.StreamChunk{
					{Content: "before "},
					{EventType: "harness_critic_feedback", Content: "feedback"},
					{Content: "after"},
					{Done: true},
				}
				err := streaming.Run(ctx, streamer, plainConsumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(plainConsumer.chunks).To(Equal([]string{"before ", "after"}))
			})
		})

		Context("when multiple harness event types occur in one stream", func() {
			BeforeEach(func() {
				streamer.chunks = []provider.StreamChunk{
					{EventType: "harness_attempt_start", Content: "attempt 1"},
					{Content: "draft "},
					{EventType: "harness_critic_feedback", Content: "needs improvement"},
					{EventType: "harness_retry", Content: "retrying"},
					{EventType: "harness_attempt_start", Content: "attempt 2"},
					{Content: "final plan"},
					{EventType: "harness_complete", Content: "passed"},
					{Done: true},
				}
			})

			It("delivers each event type to the correct consumer method", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.attemptStarts).To(Equal([]string{"attempt 1", "attempt 2"}))
				Expect(consumer.criticFeedbacks).To(Equal([]string{"needs improvement"}))
				Expect(consumer.retryContent).To(Equal([]string{"retrying"}))
				Expect(consumer.completes).To(Equal([]string{"passed"}))
				Expect(consumer.chunks).To(Equal([]string{"draft ", "final plan"}))
			})
		})
	})

	Describe("Run with plan_artifact events", func() {
		var (
			ctx      context.Context
			streamer *mockStreamer
			consumer *mockEventConsumer
		)

		BeforeEach(func() {
			ctx = context.Background()
			streamer = &mockStreamer{}
			consumer = &mockEventConsumer{}
		})

		Context("when the stream contains a plan_artifact event", func() {
			BeforeEach(func() {
				streamer.chunks = []provider.StreamChunk{
					{EventType: "plan_artifact", Content: "# My Plan\nDo the work."},
					{Done: true},
				}
			})

			It("delivers a PlanArtifactEvent via WriteEvent to EventConsumer implementations", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.events).To(HaveLen(1))
				evt, ok := consumer.events[0].(streaming.PlanArtifactEvent)
				Expect(ok).To(BeTrue())
				Expect(evt.Content).To(Equal("# My Plan\nDo the work."))
				Expect(evt.Type()).To(Equal("plan_artifact"))
			})

			It("does not deliver plan_artifact content as a regular chunk", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.chunks).To(BeEmpty())
			})
		})

		Context("when the consumer does not implement EventConsumer", func() {
			It("silently skips plan_artifact events", func() {
				plainConsumer := &mockConsumer{}
				streamer.chunks = []provider.StreamChunk{
					{Content: "before "},
					{EventType: "plan_artifact", Content: "plan content"},
					{Content: "after"},
					{Done: true},
				}
				err := streaming.Run(ctx, streamer, plainConsumer, "planner", "create a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(plainConsumer.chunks).To(Equal([]string{"before ", "after"}))
			})
		})
	})

	Describe("Run with review_verdict events", func() {
		var (
			ctx      context.Context
			streamer *mockStreamer
			consumer *mockEventConsumer
		)

		BeforeEach(func() {
			ctx = context.Background()
			streamer = &mockStreamer{}
			consumer = &mockEventConsumer{}
		})

		Context("when the stream contains a review_verdict event", func() {
			BeforeEach(func() {
				streamer.chunks = []provider.StreamChunk{
					{EventType: "review_verdict", Content: `{"verdict":"pass","confidence":0.9,"issues":[]}`},
					{Done: true},
				}
			})

			It("delivers a ReviewVerdictEvent via WriteEvent to EventConsumer implementations", func() {
				err := streaming.Run(ctx, streamer, consumer, "reviewer", "review this")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.events).To(HaveLen(1))
				evt, ok := consumer.events[0].(streaming.ReviewVerdictEvent)
				Expect(ok).To(BeTrue())
				Expect(evt.Type()).To(Equal("review_verdict"))
			})

			It("does not deliver review_verdict content as a regular chunk", func() {
				err := streaming.Run(ctx, streamer, consumer, "reviewer", "review this")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.chunks).To(BeEmpty())
			})
		})

		Context("when the consumer does not implement EventConsumer", func() {
			It("silently skips review_verdict events", func() {
				plainConsumer := &mockConsumer{}
				streamer.chunks = []provider.StreamChunk{
					{Content: "before "},
					{EventType: "review_verdict", Content: "verdict content"},
					{Content: "after"},
					{Done: true},
				}
				err := streaming.Run(ctx, streamer, plainConsumer, "reviewer", "review this")
				Expect(err).NotTo(HaveOccurred())
				Expect(plainConsumer.chunks).To(Equal([]string{"before ", "after"}))
			})
		})
	})

	Describe("Run with status_transition events", func() {
		var (
			ctx      context.Context
			streamer *mockStreamer
			consumer *mockEventConsumer
		)

		BeforeEach(func() {
			ctx = context.Background()
			streamer = &mockStreamer{}
			consumer = &mockEventConsumer{}
		})

		Context("when the stream contains a status_transition event", func() {
			BeforeEach(func() {
				streamer.chunks = []provider.StreamChunk{
					{EventType: "status_transition", Content: `{"from":"interview","to":"generation","agentId":"planner"}`},
					{Done: true},
				}
			})

			It("delivers a StatusTransitionEvent via WriteEvent to EventConsumer implementations", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "plan it")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.events).To(HaveLen(1))
				evt, ok := consumer.events[0].(streaming.StatusTransitionEvent)
				Expect(ok).To(BeTrue())
				Expect(evt.Type()).To(Equal("status_transition"))
			})

			It("does not deliver status_transition content as a regular chunk", func() {
				err := streaming.Run(ctx, streamer, consumer, "planner", "plan it")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.chunks).To(BeEmpty())
			})
		})

		Context("when the consumer does not implement EventConsumer", func() {
			It("silently skips status_transition events", func() {
				plainConsumer := &mockConsumer{}
				streamer.chunks = []provider.StreamChunk{
					{Content: "before "},
					{EventType: "status_transition", Content: "transition content"},
					{Content: "after"},
					{Done: true},
				}
				err := streaming.Run(ctx, streamer, plainConsumer, "planner", "plan it")
				Expect(err).NotTo(HaveOccurred())
				Expect(plainConsumer.chunks).To(Equal([]string{"before ", "after"}))
			})
		})
	})

	Describe("PlanResultToChannel", func() {
		It("emits the plan text followed by a done sentinel", func() {
			result := &plan.EvaluationResult{
				PlanText:     "final plan output",
				AttemptCount: 2,
				FinalScore:   0.85,
			}
			ch := streaming.PlanResultToChannel(result)

			var chunks []provider.StreamChunk
			for c := range ch {
				chunks = append(chunks, c)
			}
			Expect(chunks).To(HaveLen(2))
			Expect(chunks[0].Content).To(Equal("final plan output"))
			Expect(chunks[0].Done).To(BeFalse())
			Expect(chunks[1].Done).To(BeTrue())
			Expect(chunks[1].Content).To(BeEmpty())
		})

		It("handles empty plan text", func() {
			result := &plan.EvaluationResult{
				PlanText:     "",
				AttemptCount: 1,
				FinalScore:   0.0,
			}
			ch := streaming.PlanResultToChannel(result)

			var chunks []provider.StreamChunk
			for c := range ch {
				chunks = append(chunks, c)
			}
			Expect(chunks).To(HaveLen(2))
			Expect(chunks[0].Content).To(BeEmpty())
			Expect(chunks[1].Done).To(BeTrue())
		})
	})

	Describe("Run with DelegationInfo chunks", func() {
		var (
			ctx      context.Context
			streamer *mockStreamer
		)

		BeforeEach(func() {
			ctx = context.Background()
			streamer = &mockStreamer{}
		})

		Context("when the stream contains a DelegationInfo chunk", func() {
			BeforeEach(func() {
				streamer.chunks = []provider.StreamChunk{
					{DelegationInfo: &provider.DelegationInfo{
						SourceAgent:  "orchestrator",
						TargetAgent:  "qa-agent",
						ChainID:      "chain-1",
						Status:       "started",
						ModelName:    "claude-sonnet-4",
						ProviderName: "anthropic",
						Description:  "Run tests",
						ToolCalls:    2,
						LastTool:     "delegate",
					}},
					{Content: "delegation output"},
					{DelegationInfo: &provider.DelegationInfo{
						SourceAgent:  "orchestrator",
						TargetAgent:  "qa-agent",
						ChainID:      "chain-1",
						Status:       "completed",
						ModelName:    "claude-sonnet-4",
						ProviderName: "anthropic",
						Description:  "Run tests",
						ToolCalls:    2,
						LastTool:     "delegate",
					}},
					{Done: true},
				}
			})

			It("calls WriteDelegation on DelegationConsumer implementations", func() {
				consumer := &mockDelegationConsumer{}
				err := streaming.Run(ctx, streamer, consumer, "test-agent", "test message")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.delegations).To(HaveLen(2))

				Expect(consumer.delegations[0].Status).To(Equal("started"))
				Expect(consumer.delegations[0].SourceAgent).To(Equal("orchestrator"))
				Expect(consumer.delegations[0].TargetAgent).To(Equal("qa-agent"))
				Expect(consumer.delegations[0].ChainID).To(Equal("chain-1"))
				Expect(consumer.delegations[0].ModelName).To(Equal("claude-sonnet-4"))
				Expect(consumer.delegations[0].ProviderName).To(Equal("anthropic"))
				Expect(consumer.delegations[0].Description).To(Equal("Run tests"))
				Expect(consumer.delegations[0].ToolCalls).To(Equal(2))
				Expect(consumer.delegations[0].LastTool).To(Equal("delegate"))

				Expect(consumer.delegations[1].Status).To(Equal("completed"))
				Expect(consumer.delegations[1].ChainID).To(Equal("chain-1"))
				Expect(consumer.delegations[1].ToolCalls).To(Equal(2))
				Expect(consumer.delegations[1].LastTool).To(Equal("delegate"))
			})

			It("does not deliver DelegationInfo as regular content", func() {
				consumer := &mockDelegationConsumer{}
				err := streaming.Run(ctx, streamer, consumer, "test-agent", "test message")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.chunks).To(Equal([]string{"delegation output"}))
			})
		})

		Context("when the consumer does not implement DelegationConsumer", func() {
			It("silently skips DelegationInfo chunks", func() {
				plainConsumer := &mockConsumer{}
				streamer.chunks = []provider.StreamChunk{
					{Content: "before "},
					{DelegationInfo: &provider.DelegationInfo{
						SourceAgent: "src",
						TargetAgent: "tgt",
						ChainID:     "chain-2",
						Status:      "started",
					}},
					{Content: "after"},
					{Done: true},
				}
				err := streaming.Run(ctx, streamer, plainConsumer, "test-agent", "test message")
				Expect(err).NotTo(HaveOccurred())
				Expect(plainConsumer.chunks).To(Equal([]string{"before ", "after"}))
			})
		})

		Context("when a DelegationInfo chunk has status failed", func() {
			It("delivers the failed event to the consumer", func() {
				consumer := &mockDelegationConsumer{}
				streamer.chunks = []provider.StreamChunk{
					{DelegationInfo: &provider.DelegationInfo{
						SourceAgent: "orchestrator",
						TargetAgent: "qa-agent",
						ChainID:     "chain-3",
						Status:      "started",
					}},
					{DelegationInfo: &provider.DelegationInfo{
						SourceAgent: "orchestrator",
						TargetAgent: "qa-agent",
						ChainID:     "chain-3",
						Status:      "failed",
					}},
					{Done: true},
				}
				err := streaming.Run(ctx, streamer, consumer, "test-agent", "test message")
				Expect(err).NotTo(HaveOccurred())
				Expect(consumer.delegations).To(HaveLen(2))
				Expect(consumer.delegations[0].Status).To(Equal("started"))
				Expect(consumer.delegations[1].Status).To(Equal("failed"))
			})
		})
	})
})
