package zai_test

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
	zaiE2EModel       = "glm-5"
	zaiE2ESystemText  = "You are a helpful assistant. Respond directly to the user."
	zaiGoldenFileName = "zai_hello_response.golden.json"
)

var (
	zaiE2EOnce       sync.Once
	zaiE2ECached     []provider.StreamChunk
	zaiE2ESkipped    bool
	zaiE2EGoldenPath string
)

func createRealZAIProvider() *openaiAPI.Client {
	// Try to get Z.AI key from environment variable or auth
	apiKey := os.Getenv("ZAI_API_KEY")
	if apiKey == "" {
		return nil
	}

	client := openaiAPI.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL("https://api.z.ai/api/paas/v4"),
	)
	return &client
}

func ensureZAIE2EData() {
	zaiE2EOnce.Do(func() {
		zaiE2EGoldenPath = filepath.Join("testdata", zaiGoldenFileName)

		// Try to load from golden file
		player := testutils.NewGoldenPlayer(zaiE2EGoldenPath)
		if chunks, err := player.Load(); err == nil {
			zaiE2ECached = chunks
			return
		}

		// No golden file, try to record from live API
		client := createRealZAIProvider()
		if client == nil {
			zaiE2ESkipped = true
			return
		}

		// Build and execute the streaming request
		messages := openaicompat.BuildMessages([]provider.Message{
			{Role: "system", Content: zaiE2ESystemText},
			{Role: "user", Content: "hello"},
		})

		params := openaiAPI.ChatCompletionNewParams{
			Model:    zaiE2EModel,
			Messages: messages,
		}

		// Use RunStream to get chunks
		ch := openaicompat.RunStream(context.Background(), *client, params, "zai")
		var chunks []provider.StreamChunk
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}

		if len(chunks) == 0 {
			zaiE2ESkipped = true
			return
		}

		zaiE2ECached = chunks

		// Save to golden file
		recorder := testutils.NewGoldenRecorder(zaiE2EGoldenPath)
		_ = recorder.Save(chunks)
	})
}

var _ = Describe("Z.AI E2E Streaming", func() {
	BeforeEach(func() {
		ensureZAIE2EData()
		if zaiE2ESkipped {
			Skip("Z.AI API key not available or golden file cannot be created")
		}
	})

	It("should replay recorded Z.AI streaming response", func() {
		By("creating a replay provider with cached chunks")
		replayProvider := testutils.NewReplayProvider("test-zai", zaiE2ECached)
		Expect(replayProvider).NotTo(BeNil())

		By("streaming a simple request")
		req := provider.ChatRequest{
			Model: zaiE2EModel,
			Messages: []provider.Message{
				{Role: "system", Content: zaiE2ESystemText},
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
		player := testutils.NewGoldenPlayer(zaiE2EGoldenPath)
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
		replayProvider := testutils.NewReplayProvider("my-test-provider", zaiE2ECached)
		Expect(replayProvider.Name()).To(Equal("my-test-provider"))
	})

	It("should return error for unimplemented methods", func() {
		replayProvider := testutils.NewReplayProvider("test-zai", zaiE2ECached)

		By("attempting to call Chat")
		_, err := replayProvider.Chat(context.Background(), provider.ChatRequest{})
		Expect(err).To(HaveOccurred())

		By("attempting to call Embed")
		_, err = replayProvider.Embed(context.Background(), provider.EmbedRequest{
			Input: "test",
			Model: zaiE2EModel,
		})
		Expect(err).To(HaveOccurred())

		By("attempting to call Models")
		_, err = replayProvider.Models()
		Expect(err).To(HaveOccurred())
	})
})
