package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tool"
)

type executableMockTool struct {
	name        string
	description string
	execResult  tool.Result
	execErr     error
	execCalled  bool
	lastInput   tool.Input
}

func (t *executableMockTool) Name() string        { return t.name }
func (t *executableMockTool) Description() string { return t.description }
func (t *executableMockTool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	t.execCalled = true
	t.lastInput = input
	return t.execResult, t.execErr
}
func (t *executableMockTool) Schema() tool.Schema { return tool.Schema{} }

type streamSequenceProvider struct {
	name      string
	sequences [][]provider.StreamChunk
	callIndex int
}

func (p *streamSequenceProvider) Name() string { return p.name }

func (p *streamSequenceProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if p.callIndex >= len(p.sequences) {
		ch := make(chan provider.StreamChunk, 16)
		close(ch)
		return ch, nil
	}

	chunks := p.sequences[p.callIndex]
	p.callIndex++

	ch := make(chan provider.StreamChunk, len(chunks))
	go func() {
		defer close(ch)
		for i := range chunks {
			ch <- chunks[i]
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

var _ = Describe("Engine Permission Check", func() {
	var (
		chatProvider *streamSequenceProvider
		manifest     agent.Manifest
		testTool     *executableMockTool
		registry     *tool.Registry
	)

	BeforeEach(func() {
		chatProvider = &streamSequenceProvider{
			name:      "test-chat-provider",
			sequences: [][]provider.StreamChunk{},
		}

		manifest = agent.Manifest{
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
			execResult:  tool.Result{Output: "tool executed successfully"},
		}

		registry = tool.NewRegistry()
		registry.Register(testTool)
	})

	Context("when tool permission is Allow", func() {
		BeforeEach(func() {
			registry.SetPermission("test_tool", tool.Allow)
			chatProvider.sequences = [][]provider.StreamChunk{
				{
					{
						EventType: "tool_call",
						ToolCall: &provider.ToolCall{
							ID:        "call_allow",
							Name:      "test_tool",
							Arguments: map[string]interface{}{"arg": "val"},
						},
					},
				},
				{
					{Content: "Tool completed.", Done: true},
				},
			}
		})

		It("executes the tool immediately", func() {
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Tools:        []tool.Tool{testTool},
				ToolRegistry: registry,
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Use the tool")
			Expect(err).NotTo(HaveOccurred())

			for v := range chunks {
				_ = v
			}

			Expect(testTool.execCalled).To(BeTrue())
		})
	})

	Context("when tool permission is Deny", func() {
		BeforeEach(func() {
			registry.SetPermission("test_tool", tool.Deny)
			chatProvider.sequences = [][]provider.StreamChunk{
				{
					{
						EventType: "tool_call",
						ToolCall: &provider.ToolCall{
							ID:        "call_deny",
							Name:      "test_tool",
							Arguments: map[string]interface{}{"arg": "val"},
						},
					},
				},
			}
		})

		It("does not execute the tool", func() {
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Tools:        []tool.Tool{testTool},
				ToolRegistry: registry,
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Use the tool")
			Expect(err).NotTo(HaveOccurred())

			var lastChunk provider.StreamChunk
			for chunk := range chunks {
				lastChunk = chunk
			}

			Expect(testTool.execCalled).To(BeFalse())
			Expect(lastChunk.Error).To(HaveOccurred())
			Expect(lastChunk.Error.Error()).To(ContainSubstring("denied"))
		})
	})

	Context("when tool permission is Ask", func() {
		BeforeEach(func() {
			registry.SetPermission("test_tool", tool.Ask)
			chatProvider.sequences = [][]provider.StreamChunk{
				{
					{
						EventType: "tool_call",
						ToolCall: &provider.ToolCall{
							ID:        "call_ask",
							Name:      "test_tool",
							Arguments: map[string]interface{}{"arg": "val"},
						},
					},
				},
				{
					{Content: "Tool completed.", Done: true},
				},
			}
		})

		Context("when user approves", func() {
			It("executes the tool", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        []tool.Tool{testTool},
					ToolRegistry: registry,
					PermissionHandler: func(req tool.PermissionRequest) (bool, error) {
						return true, nil
					},
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Use the tool")
				Expect(err).NotTo(HaveOccurred())

				for v := range chunks {
					_ = v
				}

				Expect(testTool.execCalled).To(BeTrue())
			})
		})

		Context("when user denies", func() {
			It("does not execute the tool", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        []tool.Tool{testTool},
					ToolRegistry: registry,
					PermissionHandler: func(req tool.PermissionRequest) (bool, error) {
						return false, nil
					},
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Use the tool")
				Expect(err).NotTo(HaveOccurred())

				var lastChunk provider.StreamChunk
				for chunk := range chunks {
					lastChunk = chunk
				}

				Expect(testTool.execCalled).To(BeFalse())
				Expect(lastChunk.Error).To(HaveOccurred())
				Expect(lastChunk.Error.Error()).To(ContainSubstring("denied"))
			})
		})

		Context("when no permission handler is configured", func() {
			It("defaults to deny", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					Tools:        []tool.Tool{testTool},
					ToolRegistry: registry,
				})

				ctx := context.Background()
				chunks, err := eng.Stream(ctx, "test-agent", "Use the tool")
				Expect(err).NotTo(HaveOccurred())

				var lastChunk provider.StreamChunk
				for chunk := range chunks {
					lastChunk = chunk
				}

				Expect(testTool.execCalled).To(BeFalse())
				Expect(lastChunk.Error).To(HaveOccurred())
				Expect(lastChunk.Error.Error()).To(ContainSubstring("denied"))
			})
		})
	})

	Context("when permission handler receives correct request info", func() {
		It("passes tool name and arguments to the handler", func() {
			registry.SetPermission("test_tool", tool.Ask)
			chatProvider.sequences = [][]provider.StreamChunk{
				{
					{
						EventType: "tool_call",
						ToolCall: &provider.ToolCall{
							ID:        "call_info",
							Name:      "test_tool",
							Arguments: map[string]interface{}{"key": "value"},
						},
					},
				},
				{
					{Content: "Done.", Done: true},
				},
			}

			var capturedReq tool.PermissionRequest
			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     manifest,
				Tools:        []tool.Tool{testTool},
				ToolRegistry: registry,
				PermissionHandler: func(req tool.PermissionRequest) (bool, error) {
					capturedReq = req
					return true, nil
				},
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Use the tool")
			Expect(err).NotTo(HaveOccurred())

			for v := range chunks {
				_ = v
			}

			Expect(capturedReq.ToolName).To(Equal("test_tool"))
			Expect(capturedReq.Arguments).To(HaveKeyWithValue("key", "value"))
		})
	})
})

var _ = Describe("Engine Tool Call Loop", func() {
	var (
		chatProvider *streamSequenceProvider
		manifest     agent.Manifest
		testTool     *executableMockTool
	)

	BeforeEach(func() {
		chatProvider = &streamSequenceProvider{
			name:      "test-chat-provider",
			sequences: [][]provider.StreamChunk{},
		}

		manifest = agent.Manifest{
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
			execResult:  tool.Result{Output: "tool executed successfully"},
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

				for v := range chunks {
					_ = v
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
					execResult:  tool.Result{Output: "second tool result"},
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

				for v := range chunks {
					_ = v
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
					execResult:  tool.Result{Output: "found relevant context"},
				}

				getMsgTool = &executableMockTool{
					name:        "get_messages",
					description: "Get messages by range",
					execResult:  tool.Result{Output: "message content here"},
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

				for v := range chunks {
					_ = v
				}

				Expect(searchTool.execCalled).To(BeTrue())
				Expect(searchTool.lastInput.Arguments).To(HaveKeyWithValue("query", "test query"))
			})
		})

		Context("tool result storage", func() {
			var (
				tempDir string
				store   *recall.FileContextStore
			)

			BeforeEach(func() {
				var err error
				tempDir, err = os.MkdirTemp("", "engine-tool-test-*")
				Expect(err).NotTo(HaveOccurred())

				storePath := filepath.Join(tempDir, "context.json")
				store, err = recall.NewFileContextStore(storePath, "test-model")
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

				for v := range chunks {
					_ = v
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

				for v := range chunks {
					_ = v
				}

				results := store.Search([]float64{0.1, 0.2, 0.3}, 10)
				for _, result := range results {
					Expect(result.Message.Role).NotTo(Equal("tool"))
				}
			})
		})
	})

	Describe("ToolCallChunkForwarding", func() {
		Context("when the provider emits a tool_call chunk", func() {
			BeforeEach(func() {
				chatProvider.sequences = [][]provider.StreamChunk{
					{
						{
							EventType: "tool_call",
							ToolCall: &provider.ToolCall{
								ID:        "call_forward",
								Name:      "test_tool",
								Arguments: map[string]interface{}{"key": "value"},
							},
						},
					},
					{
						{Content: "Tool completed.", Done: true},
					},
				}
			})

			It("forwards the tool_call chunk to the output channel", func() {
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

				var toolCallChunks []provider.StreamChunk
				for _, chunk := range received {
					if chunk.EventType == "tool_call" && chunk.ToolCall != nil {
						toolCallChunks = append(toolCallChunks, chunk)
					}
				}

				Expect(toolCallChunks).To(HaveLen(1))
				Expect(toolCallChunks[0].ToolCall.Name).To(Equal("test_tool"))
				Expect(toolCallChunks[0].ToolCall.ID).To(Equal("call_forward"))
			})
		})
	})
})

var _ = Describe("Engine tool call context store", func() {
	It("stores assistant tool_use message before tool result in context store", func() {
		tmpDir, err := os.MkdirTemp("", "engine-tooluse-store")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { os.RemoveAll(tmpDir) })

		storePath := filepath.Join(tmpDir, "context.json")
		store, err := recall.NewFileContextStore(storePath, "")
		Expect(err).NotTo(HaveOccurred())

		testTool := &executableMockTool{
			name:        "test_tool",
			description: "A test tool",
			execResult:  tool.Result{Output: "tool output"},
		}

		registry := tool.NewRegistry()
		registry.Register(testTool)
		registry.SetPermission("test_tool", tool.Allow)

		chatProvider := &streamSequenceProvider{
			name: "test-provider",
			sequences: [][]provider.StreamChunk{
				{
					{
						EventType: "tool_call",
						ToolCall: &provider.ToolCall{
							ID:        "call_store_test",
							Name:      "test_tool",
							Arguments: map[string]interface{}{"key": "val"},
						},
					},
				},
				{
					{Content: "Final response.", Done: true},
				},
			},
		}

		manifest := agent.Manifest{
			ID:   "test-agent",
			Name: "Test Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a helpful assistant.",
			},
			ContextManagement: agent.DefaultContextManagement(),
		}

		eng := engine.New(engine.Config{
			ChatProvider: chatProvider,
			Manifest:     manifest,
			Tools:        []tool.Tool{testTool},
			ToolRegistry: registry,
		})
		eng.SetContextStore(store, "test-session")

		chunks, streamErr := eng.Stream(context.Background(), "test-agent", "Use the tool")
		Expect(streamErr).NotTo(HaveOccurred())

		for chunk := range chunks {
			_ = chunk
		}

		msgs := store.AllMessages()
		Expect(len(msgs)).To(BeNumerically(">=", 4))

		var roles []string
		for _, m := range msgs {
			roles = append(roles, m.Role)
		}

		Expect(roles).To(ContainElement("assistant"))
		Expect(roles).To(ContainElement("tool"))

		assistantToolUseIdx := -1
		toolResultIdx := -1
		for idx, m := range msgs {
			if m.Role == "assistant" && len(m.ToolCalls) > 0 && m.ToolCalls[0].Name == "test_tool" {
				assistantToolUseIdx = idx
			}
			if m.Role == "tool" && len(m.ToolCalls) > 0 && m.ToolCalls[0].ID == "call_store_test" {
				toolResultIdx = idx
			}
		}

		Expect(assistantToolUseIdx).NotTo(Equal(-1))
		Expect(toolResultIdx).NotTo(Equal(-1))
		Expect(assistantToolUseIdx).To(BeNumerically("<", toolResultIdx))

		assistantMsg := msgs[assistantToolUseIdx]
		Expect(assistantMsg.ToolCalls[0].ID).To(Equal("call_store_test"))
		Expect(assistantMsg.ToolCalls[0].Name).To(Equal("test_tool"))
	})
})

var _ = Describe("Engine tool call dispatch by chunk shape", func() {
	// These specs pin the engine's tool-call gate to the StreamChunk.ToolCall
	// field rather than a conjunction with EventType. The openaicompat provider
	// emits tool-call chunks without setting EventType (see
	// internal/provider/openaicompat/openaicompat.go), and the engine previously
	// silently dropped those, producing the 3-minute stall observed in
	// session-1775944430840782553. The session accumulator already dispatches
	// by shape (internal/session/accumulator.go:98); the engine must match.
	//
	It("dispatches a tool call when the chunk carries ToolCall but no EventType", func() {
		chatProvider := &streamSequenceProvider{
			name: "shape-dispatch-provider",
			sequences: [][]provider.StreamChunk{
				{
					{
						// EventType deliberately empty: mirrors openaicompat's
						// current (broken) emission shape.
						ToolCall: &provider.ToolCall{
							ID:        "call_shape",
							Name:      "test_tool",
							Arguments: map[string]interface{}{"arg": "value"},
						},
					},
				},
				{
					{Content: "Tool result observed.", Done: true},
				},
			},
		}

		testTool := &executableMockTool{
			name:        "test_tool",
			description: "A test tool",
			execResult:  tool.Result{Output: "shape-dispatched result"},
		}

		manifest := agent.Manifest{
			ID:   "test-agent",
			Name: "Test Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a helpful assistant.",
			},
			ContextManagement: agent.DefaultContextManagement(),
		}

		eng := engine.New(engine.Config{
			ChatProvider: chatProvider,
			Manifest:     manifest,
			Tools:        []tool.Tool{testTool},
		})

		ctx := context.Background()
		chunks, err := eng.Stream(ctx, "test-agent", "Use the tool")
		Expect(err).NotTo(HaveOccurred())

		for v := range chunks {
			_ = v
		}

		Expect(testTool.execCalled).To(BeTrue(),
			"engine must dispatch tool calls by StreamChunk.ToolCall shape, "+
				"not by EventType == \"tool_call\"; this is the fix for "+
				"non-anthropic providers that omit EventType")
		Expect(chatProvider.callIndex).To(Equal(2),
			"after dispatching the tool the engine must re-enter the stream loop "+
				"so the provider produces a follow-up response")
	})
})

var _ = Describe("Engine tool result emission", func() {
	It("emits tool result chunks on outChan after tool execution", func() {
		tmpDir, err := os.MkdirTemp("", "engine-toolresult-emit")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { os.RemoveAll(tmpDir) })

		storePath := filepath.Join(tmpDir, "context.json")
		store, err := recall.NewFileContextStore(storePath, "")
		Expect(err).NotTo(HaveOccurred())

		testTool := &executableMockTool{
			name:        "test_tool",
			description: "A test tool",
			execResult:  tool.Result{Output: "tool output here"},
		}

		registry := tool.NewRegistry()
		registry.Register(testTool)
		registry.SetPermission("test_tool", tool.Allow)

		chatProvider := &streamSequenceProvider{
			name: "test-provider",
			sequences: [][]provider.StreamChunk{
				{
					{
						EventType: "tool_call",
						ToolCall: &provider.ToolCall{
							ID:        "call_emit_test",
							Name:      "test_tool",
							Arguments: map[string]interface{}{"key": "val"},
						},
					},
				},
				{
					{Content: "Final response.", Done: true},
				},
			},
		}

		manifest := agent.Manifest{
			ID:   "test-agent",
			Name: "Test Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a helpful assistant.",
			},
			ContextManagement: agent.DefaultContextManagement(),
		}

		eng := engine.New(engine.Config{
			ChatProvider: chatProvider,
			Manifest:     manifest,
			Tools:        []tool.Tool{testTool},
			ToolRegistry: registry,
		})
		eng.SetContextStore(store, "test-session")

		chunks, streamErr := eng.Stream(context.Background(), "test-agent", "Use the tool")
		Expect(streamErr).NotTo(HaveOccurred())

		var receivedChunks []provider.StreamChunk
		for chunk := range chunks {
			receivedChunks = append(receivedChunks, chunk)
		}

		var toolResultChunk *provider.StreamChunk
		for i := range receivedChunks {
			if receivedChunks[i].EventType == "tool_result" && receivedChunks[i].ToolResult != nil {
				toolResultChunk = &receivedChunks[i]
				break
			}
		}

		Expect(toolResultChunk).NotTo(BeNil())
		Expect(toolResultChunk.ToolResult.Content).To(Equal("tool output here"))
		Expect(toolResultChunk.ToolResult.IsError).To(BeFalse())
	})
})
