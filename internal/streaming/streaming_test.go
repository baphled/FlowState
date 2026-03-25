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
	for _, c := range m.chunks {
		ch <- c
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
		for _, c := range m.streamChunks {
			ch <- c
		}
		close(ch)
		return ch, nil
	}
	return streaming.PlanResultToChannel(m.result), nil
}

type mockHarnessConsumer struct {
	chunks       []string
	toolCalls    []string
	toolResults  []string
	errors       []error
	retryContent []string
	doneCount    int
	writeErr     error
	enableTool   bool
	enableResult bool
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
})
