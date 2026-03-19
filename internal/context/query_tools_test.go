package context_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	fscontext "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

type mockProvider struct {
	embedResult []float64
	embedErr    error
	chatResult  provider.ChatResponse
	chatErr     error
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 16)
	close(ch)
	return ch, nil
}

func (m *mockProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return m.chatResult, m.chatErr
}

func (m *mockProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return m.embedResult, m.embedErr
}

func (m *mockProvider) Models() ([]provider.Model, error) {
	return nil, nil
}

type mockTokenCounter struct {
	countResult int
}

func (m *mockTokenCounter) Count(_ string) int {
	return m.countResult
}

func (m *mockTokenCounter) ModelLimit(_ string) int {
	return 4096
}

var _ = Describe("ContextTools", func() {
	var (
		tempDir   string
		storePath string
		store     *fscontext.FileContextStore
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "query-tools-test-*")
		Expect(err).NotTo(HaveOccurred())
		storePath = filepath.Join(tempDir, "context.json")

		store, err = fscontext.NewFileContextStore(storePath, "test-model")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("SearchContextTool", func() {
		var (
			mockProv   *mockProvider
			searchTool *fscontext.SearchContextTool
		)

		BeforeEach(func() {
			mockProv = &mockProvider{
				embedResult: []float64{0.1, 0.2, 0.3},
			}
			searchTool = fscontext.NewSearchContextTool(store, mockProv, 5)
		})

		It("returns correct name", func() {
			Expect(searchTool.Name()).To(Equal("search_context"))
		})

		It("returns correct description", func() {
			Expect(searchTool.Description()).To(Equal("Search conversation history semantically"))
		})

		Context("when store is empty", func() {
			It("returns empty output without error", func() {
				result, err := searchTool.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{"query": "test"},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(BeEmpty())
			})
		})

		Context("when store has messages with embeddings", func() {
			BeforeEach(func() {
				store.Append(provider.Message{Role: "user", Content: "Hello world"})
				store.Append(provider.Message{Role: "assistant", Content: "Hi there"})
				msgID := store.GetMessageID(0)
				store.StoreEmbedding(msgID, []float64{0.1, 0.2, 0.3}, "test-model", 3)
			})

			It("returns formatted results", func() {
				result, err := searchTool.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{"query": "hello"},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("user: Hello world"))
			})
		})

		Context("when Embed fails", func() {
			BeforeEach(func() {
				mockProv.embedErr = errors.New("embed failed")
				store.Append(provider.Message{Role: "user", Content: "Hello world"})
				store.Append(provider.Message{Role: "assistant", Content: "Hi there"})
			})

			It("falls back to recent messages", func() {
				result, err := searchTool.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{"query": "test"},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("Hello world"))
				Expect(result.Output).To(ContainSubstring("Hi there"))
			})
		})
	})

	Describe("GetMessagesTool", func() {
		var getMsgsTool *fscontext.GetMessagesTool

		BeforeEach(func() {
			getMsgsTool = fscontext.NewGetMessagesTool(store)
		})

		It("returns correct name", func() {
			Expect(getMsgsTool.Name()).To(Equal("get_messages"))
		})

		It("returns correct description", func() {
			Expect(getMsgsTool.Description()).To(Equal("Retrieve messages by range or recent count"))
		})

		Context("when store has messages", func() {
			BeforeEach(func() {
				store.Append(provider.Message{Role: "user", Content: "First"})
				store.Append(provider.Message{Role: "assistant", Content: "Second"})
				store.Append(provider.Message{Role: "user", Content: "Third"})
			})

			It("returns last N messages when count provided", func() {
				result, err := getMsgsTool.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{"count": float64(2)},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("Second"))
				Expect(result.Output).To(ContainSubstring("Third"))
				Expect(result.Output).NotTo(ContainSubstring("First"))
			})

			It("returns range when start and end provided", func() {
				result, err := getMsgsTool.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{"start": float64(0), "end": float64(1)},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("First"))
				Expect(result.Output).NotTo(ContainSubstring("Second"))
			})

			It("returns default 10 recent when no arguments", func() {
				result, err := getMsgsTool.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("First"))
				Expect(result.Output).To(ContainSubstring("Second"))
				Expect(result.Output).To(ContainSubstring("Third"))
			})
		})
	})

	Describe("SummarizeContextTool", func() {
		var (
			mockProv      *mockProvider
			mockCounter   *mockTokenCounter
			summarizeTool *fscontext.SummarizeContextTool
		)

		BeforeEach(func() {
			mockProv = &mockProvider{
				chatResult: provider.ChatResponse{
					Message: provider.Message{Role: "assistant", Content: "Summary: test conversation"},
				},
			}
			mockCounter = &mockTokenCounter{countResult: 10}
			summarizeTool = fscontext.NewSummarizeContextTool(store, mockProv, 2, mockCounter, "test-model")
		})

		It("returns correct name", func() {
			Expect(summarizeTool.Name()).To(Equal("summarize_context"))
		})

		It("returns correct description", func() {
			Expect(summarizeTool.Description()).To(Equal("Recursively summarize conversation history"))
		})

		Context("when store is empty", func() {
			It("returns no conversation history message", func() {
				result, err := summarizeTool.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal("No conversation history"))
			})
		})

		Context("when store has messages", func() {
			BeforeEach(func() {
				store.Append(provider.Message{Role: "user", Content: "Hello"})
				store.Append(provider.Message{Role: "assistant", Content: "Hi there"})
			})

			It("calls provider.Chat and returns summary", func() {
				result, err := summarizeTool.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("Summary: test conversation"))
			})
		})
	})

	Describe("ContextQueryTools constructor", func() {
		It("creates all three tools", func() {
			mockProv := &mockProvider{}
			mockCounter := &mockTokenCounter{}
			tools := fscontext.NewContextQueryTools(store, mockProv, mockCounter, "test-model")

			Expect(tools.Search).NotTo(BeNil())
			Expect(tools.GetMsgs).NotTo(BeNil())
			Expect(tools.Summarize).NotTo(BeNil())
		})
	})
})
