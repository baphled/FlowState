package openaicompat_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
	"github.com/baphled/flowstate/internal/testutils"
)

const (
	openaiE2EModel       = "gpt-4o"
	openaiE2ESystemText  = "You are a helpful assistant. Respond directly to the user."
	openaiGoldenFileName = "openai_hello_response.golden.json"
)

var (
	openaiE2EOnce       sync.Once
	openaiE2ECached     []provider.StreamChunk
	openaiE2ESkipped    bool
	openaiE2EGoldenPath string
)

func createRealOpenAIProvider() *openaiAPI.Client {
	// Try to get OpenAI key from environment variable
	// For now, we'll rely on environment variable or manual auth
	// If adding to auth.json, the key would be in a structured field
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil
	}

	client := openaiAPI.NewClient(option.WithAPIKey(apiKey))
	return &client
}

func ensureOpenAIE2EData() {
	openaiE2EOnce.Do(func() {
		openaiE2EGoldenPath = filepath.Join("testdata", openaiGoldenFileName)

		// Try to load from golden file
		player := testutils.NewGoldenPlayer(openaiE2EGoldenPath)
		if chunks, err := player.Load(); err == nil {
			openaiE2ECached = chunks
			return
		}

		// No golden file, try to record from live API
		client := createRealOpenAIProvider()
		if client == nil {
			openaiE2ESkipped = true
			return
		}

		// Build and execute the streaming request
		messages := openaicompat.BuildMessages([]provider.Message{
			{Role: "system", Content: openaiE2ESystemText},
			{Role: "user", Content: "hello"},
		})

		params := openaiAPI.ChatCompletionNewParams{
			Model:    openaiE2EModel,
			Messages: messages,
		}

		// Use RunStream to get chunks
		ch := openaicompat.RunStream(context.Background(), *client, params)
		var chunks []provider.StreamChunk
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}

		if len(chunks) == 0 {
			openaiE2ESkipped = true
			return
		}

		openaiE2ECached = chunks

		// Save to golden file
		recorder := testutils.NewGoldenRecorder(openaiE2EGoldenPath)
		_ = recorder.Save(chunks)
	})
}

var _ = Describe("OpenAI E2E Streaming", func() {
	BeforeEach(func() {
		ensureOpenAIE2EData()
		if openaiE2ESkipped {
			Skip("OpenAI API key not available or golden file cannot be created")
		}
	})

	It("should replay recorded OpenAI streaming response", func() {
		By("creating a replay provider with cached chunks")
		replayProvider := testutils.NewReplayProvider("test-openai", openaiE2ECached)
		Expect(replayProvider).NotTo(BeNil())

		By("streaming a simple request")
		req := provider.ChatRequest{
			Model: openaiE2EModel,
			Messages: []provider.Message{
				{Role: "system", Content: openaiE2ESystemText},
				{Role: "user", Content: "hello"},
			},
		}

		ch, err := replayProvider.Stream(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		Expect(ch).NotTo(BeNil())

		By("collecting all streamed chunks")
		var receivedChunks []provider.StreamChunk
		var finalContent string
		for chunk := range ch {
			receivedChunks = append(receivedChunks, chunk)
			if chunk.Content != "" {
				finalContent += chunk.Content
			}
		}

		By("verifying we received chunks")
		Expect(receivedChunks).NotTo(BeEmpty())

		By("verifying the final message contains expected content")
		Expect(finalContent).NotTo(BeEmpty())
		Expect(receivedChunks[len(receivedChunks)-1].Done).To(BeTrue())
	})

	It("should verify streaming format consistency", func() {
		By("replaying from golden file")
		player := testutils.NewGoldenPlayer(openaiE2EGoldenPath)
		chunks, err := player.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(chunks).NotTo(BeEmpty())

		By("verifying chunk structure")
		var foundContent bool
		var foundDone bool
		for _, chunk := range chunks {
			if chunk.Content != "" {
				foundContent = true
			}
			if chunk.Done {
				foundDone = true
			}
		}
		Expect(foundContent).To(BeTrue(), "should have at least one chunk with content")
		Expect(foundDone).To(BeTrue(), "should have a final chunk with Done=true")
	})

	It("should handle replay provider Name() method", func() {
		replayProvider := testutils.NewReplayProvider("my-test-provider", openaiE2ECached)
		Expect(replayProvider.Name()).To(Equal("my-test-provider"))
	})

	It("should return error for unimplemented methods", func() {
		replayProvider := testutils.NewReplayProvider("test-openai", openaiE2ECached)

		By("attempting to call Chat")
		_, err := replayProvider.Chat(context.Background(), provider.ChatRequest{})
		Expect(err).To(HaveOccurred())

		By("attempting to call Embed")
		_, err = replayProvider.Embed(context.Background(), provider.EmbedRequest{
			Input: "test",
			Model: openaiE2EModel,
		})
		Expect(err).To(HaveOccurred())

		By("attempting to call Models")
		_, err = replayProvider.Models()
		Expect(err).To(HaveOccurred())
	})
})
