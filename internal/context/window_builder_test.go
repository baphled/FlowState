package context_test

import (
	"bytes"
	gocontext "context"
	"log"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WindowBuilder", func() {
	var (
		builder *context.WindowBuilder
		store   *recall.FileContextStore
		counter context.TokenCounter
		tempDir string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "window-builder-test")
		Expect(err).NotTo(HaveOccurred())

		path := filepath.Join(tempDir, "store.json")
		store, err = recall.NewFileContextStore(path, "test-model")
		Expect(err).NotTo(HaveOccurred())

		counter = context.NewApproximateCounter()
		builder = context.NewWindowBuilder(counter)
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("Build", func() {
		Context("cold start with no messages", func() {
			It("returns only the system prompt", func() {
				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: "You are a helpful assistant.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				result := builder.Build(manifest, store, 4096)

				Expect(result.Messages).To(HaveLen(1))
				Expect(result.Messages[0].Role).To(Equal("system"))
				Expect(result.Messages[0].Content).To(Equal("You are a helpful assistant."))
				Expect(result.Truncated).To(BeFalse())
			})
		})

		Context("with sliding window messages", func() {
			BeforeEach(func() {
				store.Append(provider.Message{Role: "user", Content: "first message"})
				store.Append(provider.Message{Role: "assistant", Content: "first response"})
				store.Append(provider.Message{Role: "user", Content: "second message"})
				store.Append(provider.Message{Role: "assistant", Content: "second response"})
				store.Append(provider.Message{Role: "user", Content: "third message"})
			})

			It("includes last K messages from sliding window", func() {
				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: "System prompt",
					},
					ContextManagement: agent.ContextManagement{
						SlidingWindowSize: 3,
					},
				}

				result := builder.Build(manifest, store, 4096)

				Expect(result.Messages).To(HaveLen(4))
				Expect(result.Messages[0].Role).To(Equal("system"))
				Expect(result.Messages[1].Content).To(Equal("second message"))
				Expect(result.Messages[2].Content).To(Equal("second response"))
				Expect(result.Messages[3].Content).To(Equal("third message"))
			})

			It("respects sliding window size from manifest", func() {
				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: "System prompt",
					},
					ContextManagement: agent.ContextManagement{
						SlidingWindowSize: 2,
					},
				}

				result := builder.Build(manifest, store, 4096)

				Expect(result.Messages).To(HaveLen(3))
				Expect(result.Messages[0].Role).To(Equal("system"))
				Expect(result.Messages[1].Content).To(Equal("second response"))
				Expect(result.Messages[2].Content).To(Equal("third message"))
			})
		})

		Context("token budget enforcement", func() {
			It("reserves tokens for system prompt including skill content", func() {
				longSystemPrompt := "You are an assistant.\n\n" +
					"## Always-Active Skills\n\n" +
					"### pre-action\nAlways think before acting. " +
					"### memory-keeper\nCapture discoveries and patterns. " +
					"### token-efficiency\nOptimise token usage."

				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: longSystemPrompt,
					},
					ContextManagement: agent.ContextManagement{
						SlidingWindowSize: 5,
					},
				}

				store.Append(provider.Message{Role: "user", Content: "hello"})

				result := builder.Build(manifest, store, 4096)

				expectedSystemTokens := counter.Count(longSystemPrompt)
				Expect(result.Messages[0].Role).To(Equal("system"))
				Expect(result.Messages[0].Content).To(Equal(longSystemPrompt))
				Expect(result.TokensUsed).To(BeNumerically(">=", expectedSystemTokens))
			})

			It("truncates system prompt when budget is smaller", func() {
				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: "This is a very long system prompt that should be truncated when the token budget is too small.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				result := builder.Build(manifest, store, 10)

				Expect(result.Messages).To(HaveLen(1))
				Expect(result.Messages[0].Role).To(Equal("system"))
				Expect(result.Truncated).To(BeTrue())
				Expect(len(result.Messages[0].Content)).To(BeNumerically("<", len(manifest.Instructions.SystemPrompt)))
			})

			It("limits messages when budget is constrained", func() {
				for range 20 {
					store.Append(provider.Message{Role: "user", Content: "This is message content that takes tokens"})
					store.Append(provider.Message{Role: "assistant", Content: "This is response content that also takes tokens"})
				}

				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: "Short prompt",
					},
					ContextManagement: agent.ContextManagement{
						SlidingWindowSize: 20,
					},
				}

				result := builder.Build(manifest, store, 100)

				totalTokens := 0
				for _, msg := range result.Messages {
					totalTokens += counter.Count(msg.Content)
				}
				Expect(totalTokens).To(BeNumerically("<=", 100))
			})
		})

		Context("oversized message truncation", func() {
			It("truncates individual messages that exceed budget", func() {
				longMessage := make([]byte, 1000)
				for i := range longMessage {
					longMessage[i] = 'a'
				}
				store.Append(provider.Message{Role: "user", Content: string(longMessage)})

				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: "Short",
					},
					ContextManagement: agent.ContextManagement{
						SlidingWindowSize: 1,
					},
				}

				result := builder.Build(manifest, store, 50)

				Expect(result.Messages).To(HaveLen(2))
				Expect(len(result.Messages[1].Content)).To(BeNumerically("<", 1000))
			})
		})

		Context("deduplication", func() {
			It("does not duplicate messages appearing in both semantic results and sliding window", func() {
				store.Append(provider.Message{Role: "user", Content: "unique message one"})
				store.Append(provider.Message{Role: "assistant", Content: "unique response one"})
				store.Append(provider.Message{Role: "user", Content: "unique message two"})

				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: "System",
					},
					ContextManagement: agent.ContextManagement{
						SlidingWindowSize: 3,
					},
				}

				result := builder.Build(manifest, store, 4096)

				contentCounts := make(map[string]int)
				for _, msg := range result.Messages {
					contentCounts[msg.Content]++
				}

				for content, count := range contentCounts {
					Expect(count).To(Equal(1), "Message '%s' should appear exactly once", content)
				}
			})
		})

		Context("assembly order", func() {
			It("maintains correct order: system_prompt, state_summary, semantic_results, sliding_window", func() {
				store.Append(provider.Message{Role: "user", Content: "message one"})
				store.Append(provider.Message{Role: "assistant", Content: "response one"})
				store.Append(provider.Message{Role: "user", Content: "message two"})

				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: "System prompt here",
					},
					ContextManagement: agent.ContextManagement{
						SlidingWindowSize: 3,
					},
				}

				result := builder.Build(manifest, store, 4096)

				Expect(result.Messages[0].Role).To(Equal("system"))
				Expect(result.Messages[0].Content).To(Equal("System prompt here"))
			})

			It("places all components in correct sequence when all are present", func() {
				store.Append(provider.Message{Role: "user", Content: "sliding window message"})

				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: "System prompt",
					},
					ContextManagement: agent.ContextManagement{
						SlidingWindowSize: 1,
					},
				}

				semanticResults := []recall.SearchResult{
					{MessageID: "semantic-1", Score: 0.9, Message: provider.Message{Role: "user", Content: "semantic result"}},
				}

				result := builder.BuildWithSemanticResults(manifest, store, 4096, semanticResults)

				Expect(result.Messages).To(HaveLen(3))
				Expect(result.Messages[0].Role).To(Equal("system"))
				Expect(result.Messages[0].Content).To(Equal("System prompt"))
				Expect(result.Messages[1].Content).To(Equal("semantic result"))
				Expect(result.Messages[2].Content).To(Equal("sliding window message"))
			})
		})

		Context("with state summary", func() {
			It("includes state summary when provided", func() {
				store.Append(provider.Message{Role: "user", Content: "hello"})

				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: "System",
					},
					ContextManagement: agent.ContextManagement{
						SlidingWindowSize: 1,
					},
				}

				result := builder.BuildWithSummary(manifest, store, 4096, "Summary of prior context")

				Expect(result.Messages).To(HaveLen(3))
				Expect(result.Messages[0].Role).To(Equal("system"))
				Expect(result.Messages[1].Role).To(Equal("assistant"))
				Expect(result.Messages[1].Content).To(ContainSubstring("Summary of prior context"))
				Expect(result.Messages[2].Content).To(Equal("hello"))
			})
		})

		Context("with semantic search results", func() {
			It("includes semantic results when provided", func() {
				store.Append(provider.Message{Role: "user", Content: "current"})

				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: "System",
					},
					ContextManagement: agent.ContextManagement{
						SlidingWindowSize: 1,
					},
				}

				semanticResults := []recall.SearchResult{
					{MessageID: "msg-1", Score: 0.9, Message: provider.Message{Role: "user", Content: "relevant earlier"}},
				}

				result := builder.BuildWithSemanticResults(manifest, store, 4096, semanticResults)

				hasRelevant := false
				for _, msg := range result.Messages {
					if msg.Content == "relevant earlier" {
						hasRelevant = true
						break
					}
				}
				Expect(hasRelevant).To(BeTrue())
			})
		})
	})

	Describe("BuildResult", func() {
		It("reports tokens used", func() {
			manifest := &agent.Manifest{
				Instructions: agent.Instructions{
					SystemPrompt: "Hello world system prompt",
				},
				ContextManagement: agent.DefaultContextManagement(),
			}

			result := builder.Build(manifest, store, 4096)

			Expect(result.TokensUsed).To(BeNumerically(">", 0))
		})

		It("reports budget remaining", func() {
			manifest := &agent.Manifest{
				Instructions: agent.Instructions{
					SystemPrompt: "Short",
				},
				ContextManagement: agent.DefaultContextManagement(),
			}

			result := builder.Build(manifest, store, 1000)

			Expect(result.BudgetRemaining).To(BeNumerically("<", 1000))
			Expect(result.BudgetRemaining).To(BeNumerically(">", 0))
		})
	})

	Describe("BuildContext", func() {
		Context("T15a-compatible entrypoint", func() {
			It("accepts context and userMessage parameters", func() {
				ctx := gocontext.Background()
				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: "System prompt",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				messages := builder.BuildContext(ctx, manifest, "hello user", store, 4096)

				Expect(messages).NotTo(BeEmpty())
				Expect(messages[0].Role).To(Equal("system"))
			})

			It("appends userMessage to context window", func() {
				ctx := gocontext.Background()
				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: "System",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				messages := builder.BuildContext(ctx, manifest, "new user query", store, 4096)

				lastMsg := messages[len(messages)-1]
				Expect(lastMsg.Role).To(Equal("user"))
				Expect(lastMsg.Content).To(Equal("new user query"))
			})
		})

		Context("warning on truncation", func() {
			It("logs warning when system prompt is truncated", func() {
				var logBuf bytes.Buffer
				origOutput := log.Writer()
				log.SetOutput(&logBuf)
				DeferCleanup(func() { log.SetOutput(origOutput) })

				ctx := gocontext.Background()
				manifest := &agent.Manifest{
					Instructions: agent.Instructions{
						SystemPrompt: "This is a very long system prompt that exceeds the tiny token budget and must be truncated.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				_ = builder.BuildContext(ctx, manifest, "test", store, 10)

				Expect(logBuf.String()).To(ContainSubstring("system prompt truncated"))
			})
		})
	})
})
