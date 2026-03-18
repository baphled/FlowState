package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

type executableMockTool struct {
	name        string
	description string
	execResult  tool.ToolResult
	execErr     error
	execCalled  bool
	lastInput   tool.ToolInput
}

func (t *executableMockTool) Name() string        { return t.name }
func (t *executableMockTool) Description() string { return t.description }
func (t *executableMockTool) Execute(_ context.Context, input tool.ToolInput) (tool.ToolResult, error) {
	t.execCalled = true
	t.lastInput = input
	return t.execResult, t.execErr
}
func (t *executableMockTool) Schema() tool.ToolSchema { return tool.ToolSchema{} }

type streamSequenceProvider struct {
	name      string
	sequences [][]provider.StreamChunk
	callIndex int
}

func (p *streamSequenceProvider) Name() string { return p.name }

func (p *streamSequenceProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if p.callIndex >= len(p.sequences) {
		ch := make(chan provider.StreamChunk)
		close(ch)
		return ch, nil
	}

	chunks := p.sequences[p.callIndex]
	p.callIndex++

	ch := make(chan provider.StreamChunk, len(chunks))
	go func() {
		defer close(ch)
		for _, chunk := range chunks {
			ch <- chunk
		}
	}()
	return ch, nil
}

func (p *streamSequenceProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *streamSequenceProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

func (p *streamSequenceProvider) Models() ([]provider.Model, error) {
	return nil, nil
}

var _ = Describe("Engine Tool Call Loop", func() {
	var (
		chatProvider *streamSequenceProvider
		manifest     agent.AgentManifest
		testTool     *executableMockTool
	)

	BeforeEach(func() {
		chatProvider = &streamSequenceProvider{
			name:      "test-chat-provider",
			sequences: [][]provider.StreamChunk{},
		}

		manifest = agent.AgentManifest{
			ID:   "test-agent",
			Name: "Test Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a helpful assistant.",
			},
			ContextManagement: agent.DefaultContextManagement(),
		}

		testTool = &executableMockTool{
			name:        "test_tool",
			description: "A test tool",
			execResult:  tool.ToolResult{Output: "tool executed successfully"},
		}
	})

	Describe("ProcessToolCalls", func() {
		Context("when stream contains a tool call", func() {
			BeforeEach(func() {
				chatProvider.sequences = [][]provider.StreamChunk{
					{
						{
							EventType: "tool_call",
							ToolCall: &provider.ToolCall{
								ID:        "call_123",
								Name:      "test_tool",
								Arguments: map[string]interface{}{"arg1": "value1"},
							},
						},
					},
					{
						{Content: "Tool result processed.", Done: true},
					},
				}
			})

			It("executes the tool when tool_call event is received", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        []tool.Tool{testTool},
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Please use the tool")

				Expect(err).NotTo(HaveOccurred())

				var received []provider.StreamChunk
				for chunk := range chunks {
					received = append(received, chunk)
				}

				Expect(testTool.execCalled).To(BeTrue())
				Expect(testTool.lastInput.Name).To(Equal("test_tool"))
				Expect(testTool.lastInput.Arguments).To(HaveKeyWithValue("arg1", "value1"))
			})

			It("feeds tool result back to provider", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        []tool.Tool{testTool},
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Please use the tool")

				Expect(err).NotTo(HaveOccurred())

				var received []provider.StreamChunk
				for chunk := range chunks {
					received = append(received, chunk)
				}

				Expect(chatProvider.callIndex).To(Equal(2))
				Expect(received).NotTo(BeEmpty())

				var hasContent bool
				for _, chunk := range received {
					if chunk.Content != "" {
						hasContent = true
						break
					}
				}
				Expect(hasContent).To(BeTrue())
			})
		})

		Context("when tool name is unknown", func() {
			BeforeEach(func() {
				chatProvider.sequences = [][]provider.StreamChunk{
					{
						{
							EventType: "tool_call",
							ToolCall: &provider.ToolCall{
								ID:        "call_456",
								Name:      "unknown_tool",
								Arguments: map[string]interface{}{},
							},
						},
					},
				}
			})

			It("returns error in stream", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        []tool.Tool{testTool},
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Use unknown tool")

				Expect(err).NotTo(HaveOccurred())

				var lastChunk provider.StreamChunk
				for chunk := range chunks {
					lastChunk = chunk
				}

				Expect(lastChunk.Error).To(HaveOccurred())
				Expect(lastChunk.Error.Error()).To(ContainSubstring("unknown_tool"))
			})
		})

		Context("when tool execution fails", func() {
			BeforeEach(func() {
				testTool.execErr = errors.New("tool execution failed")
				chatProvider.sequences = [][]provider.StreamChunk{
					{
						{
							EventType: "tool_call",
							ToolCall: &provider.ToolCall{
								ID:        "call_789",
								Name:      "test_tool",
								Arguments: map[string]interface{}{},
							},
						},
					},
					{
						{Content: "I see the tool failed.", Done: true},
					},
				}
			})

			It("feeds error result back to provider", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        []tool.Tool{testTool},
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Use the tool")

				Expect(err).NotTo(HaveOccurred())

				var received []provider.StreamChunk
				for chunk := range chunks {
					received = append(received, chunk)
				}

				Expect(chatProvider.callIndex).To(Equal(2))
			})
		})

		Context("with multiple tool calls in sequence", func() {
			var secondTool *executableMockTool

			BeforeEach(func() {
				secondTool = &executableMockTool{
					name:        "second_tool",
					description: "Another tool",
					execResult:  tool.ToolResult{Output: "second tool result"},
				}

				chatProvider.sequences = [][]provider.StreamChunk{
					{
						{
							EventType: "tool_call",
							ToolCall: &provider.ToolCall{
								ID:        "call_1",
								Name:      "test_tool",
								Arguments: map[string]interface{}{"step": "first"},
							},
						},
					},
					{
						{
							EventType: "tool_call",
							ToolCall: &provider.ToolCall{
								ID:        "call_2",
								Name:      "second_tool",
								Arguments: map[string]interface{}{"step": "second"},
							},
						},
					},
					{
						{Content: "Both tools completed.", Done: true},
					},
				}
			})

			It("executes both tools in sequence", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        []tool.Tool{testTool, secondTool},
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Use both tools")

				Expect(err).NotTo(HaveOccurred())

				var received []provider.StreamChunk
				for chunk := range chunks {
					received = append(received, chunk)
				}

				Expect(testTool.execCalled).To(BeTrue())
				Expect(secondTool.execCalled).To(BeTrue())
				Expect(chatProvider.callIndex).To(Equal(3))
			})
		})

		Context("with context query tools", func() {
			var (
				searchTool *executableMockTool
				getMsgTool *executableMockTool
			)

			BeforeEach(func() {
				searchTool = &executableMockTool{
					name:        "search_context",
					description: "Search conversation history",
					execResult:  tool.ToolResult{Output: "found relevant context"},
				}

				getMsgTool = &executableMockTool{
					name:        "get_messages",
					description: "Get messages by range",
					execResult:  tool.ToolResult{Output: "message content here"},
				}

				chatProvider.sequences = [][]provider.StreamChunk{
					{
						{
							EventType: "tool_call",
							ToolCall: &provider.ToolCall{
								ID:        "call_search",
								Name:      "search_context",
								Arguments: map[string]interface{}{"query": "test query"},
							},
						},
					},
					{
						{Content: "Found the context.", Done: true},
					},
				}
			})

			It("dispatches context query tools like regular tools", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        []tool.Tool{searchTool, getMsgTool},
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Search for something")

				Expect(err).NotTo(HaveOccurred())

				var received []provider.StreamChunk
				for chunk := range chunks {
					received = append(received, chunk)
				}

				Expect(searchTool.execCalled).To(BeTrue())
				Expect(searchTool.lastInput.Arguments).To(HaveKeyWithValue("query", "test query"))
			})
		})

		Context("tool result storage", func() {
			var (
				tempDir string
				store   *ctxstore.FileContextStore
			)

			BeforeEach(func() {
				var err error
				tempDir, err = os.MkdirTemp("", "engine-tool-test-*")
				Expect(err).NotTo(HaveOccurred())

				storePath := filepath.Join(tempDir, "context.json")
				store, err = ctxstore.NewFileContextStore(storePath, "test-model")
				Expect(err).NotTo(HaveOccurred())

				chatProvider.sequences = [][]provider.StreamChunk{
					{
						{
							EventType: "tool_call",
							ToolCall: &provider.ToolCall{
								ID:        "call_store_test",
								Name:      "test_tool",
								Arguments: map[string]interface{}{},
							},
						},
					},
					{
						{Content: "Tool processed.", Done: true},
					},
				}
			})

			AfterEach(func() {
				os.RemoveAll(tempDir)
			})

			It("stores tool results in context store", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        []tool.Tool{testTool},
					Store:        store,
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Use the tool")

				Expect(err).NotTo(HaveOccurred())

				for range chunks {
				}

				messages := store.AllMessages()
				var hasToolRole bool
				for _, msg := range messages {
					if msg.Role == "tool" {
						hasToolRole = true
						break
					}
				}
				Expect(hasToolRole).To(BeTrue())
			})

			It("does not embed tool results", func() {
				embeddingProvider := &streamSequenceProvider{
					name: "test-embed-provider",
				}

				eng := engine.New(engine.Config{
					ChatProvider:      chatProvider,
					EmbeddingProvider: embeddingProvider,
					Manifest:          manifest,
					Tools:             []tool.Tool{testTool},
					Store:             store,
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Use the tool")

				Expect(err).NotTo(HaveOccurred())

				for range chunks {
				}

				results := store.Search([]float64{0.1, 0.2, 0.3}, 10)
				for _, result := range results {
					Expect(result.Message.Role).NotTo(Equal("tool"))
				}
			})
		})
	})
})
