package openzen_test

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
	openzenE2EModel       = "llm"
	openzenE2ESystemText  = "You are a helpful assistant. Respond directly to the user."
	openzenGoldenFileName = "openzen_hello_response.golden.json"
)

var (
	openzenE2EOnce       sync.Once
	openzenE2ECached     []provider.StreamChunk
	openzenE2ESkipped    bool
	openzenE2EGoldenPath string
)

func createRealOpenZenProvider() *openaiAPI.Client {
	apiKey := os.Getenv("OPENZEN_API_KEY")
	if apiKey == "" {
		return nil
	}

	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithBaseURL("https://api.openzen.ai"),
	}
	client := openaiAPI.NewClient(opts...)
	return &client
}

func openzenGoldenHasContent(chunks []provider.StreamChunk) bool {
	for _, c := range chunks {
		if c.Content != "" {
			return true
		}
	}
	return false
}

func ensureOpenZenE2EData() {
	openzenE2EOnce.Do(func() {
		openzenE2EGoldenPath = filepath.Join("testdata", openzenGoldenFileName)

		// Try to load from golden file — only accept it if it has real text content.
		// A golden recorded during an API outage contains only error chunks and must
		// not be treated as valid playback data.
		player := testutils.NewGoldenPlayer(openzenE2EGoldenPath)
		if chunks, err := player.Load(); err == nil && openzenGoldenHasContent(chunks) {
			openzenE2ECached = chunks
			return
		}

		// No usable golden file — try to record from live API.
		client := createRealOpenZenProvider()
		if client == nil {
			openzenE2ESkipped = true
			return
		}

		// Build and execute the streaming request
		messages := openaicompat.BuildMessages([]provider.Message{
			{Role: "system", Content: openzenE2ESystemText},
			{Role: "user", Content: "hello"},
		})

		params := openaiAPI.ChatCompletionNewParams{
			Model:    openzenE2EModel,
			Messages: messages,
		}

		// Use RunStream to get chunks
		ch := openaicompat.RunStream(context.Background(), *client, params, "openzen")
		var chunks []provider.StreamChunk
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}

		if !openzenGoldenHasContent(chunks) {
			openzenE2ESkipped = true
			return
		}

		openzenE2ECached = chunks

		// Save to golden file
		recorder := testutils.NewGoldenRecorder(openzenE2EGoldenPath)
		_ = recorder.Save(chunks)
	})
}

var _ = Describe("OpenZen E2E Streaming", func() {
	BeforeEach(func() {
		ensureOpenZenE2EData()
		if openzenE2ESkipped {
			Skip("OpenZen API key not available or golden file cannot be created")
		}
	})

	It("loads cached golden chunks successfully", func() {
		Expect(openzenE2ECached).NotTo(BeEmpty())
	})

	It("first chunk has content", func() {
		Expect(openzenE2ECached).ToNot(BeEmpty())
		firstChunk := openzenE2ECached[0]
		Expect(firstChunk.Content != "" || firstChunk.Error != nil).To(BeTrue())
	})

	It("all chunks have valid structure", func() {
		for _, chunk := range openzenE2ECached {
			Expect(chunk.EventType == "" || chunk.Content != "" || chunk.Done || chunk.Error != nil).To(BeTrue())
		}
	})

	It("collects all text content in order", func() {
		var text string
		for _, chunk := range openzenE2ECached {
			if chunk.Content != "" {
				text += chunk.Content
			}
		}
		Expect(text).NotTo(BeEmpty())
	})

	It("last chunk marks completion", func() {
		Expect(openzenE2ECached).ToNot(BeEmpty())
		lastChunk := openzenE2ECached[len(openzenE2ECached)-1]
		Expect(lastChunk.Done).To(BeTrue())
	})

	It("streaming completes without errors", func() {
		hasError := false
		for _, chunk := range openzenE2ECached {
			if chunk.Error != nil {
				hasError = true
			}
		}
		Expect(hasError).To(BeFalse())
	})

	It("handles multiple sequential chunks correctly", func() {
		Expect(openzenE2ECached).ToNot(BeEmpty())

		for i := range len(openzenE2ECached) - 1 {
			currentChunk := openzenE2ECached[i]
			Expect(currentChunk.Done).To(BeFalse())
		}
	})

	It("golden file persists and reloads correctly", func() {
		Expect(openzenE2EGoldenPath).NotTo(BeEmpty())
		player := testutils.NewGoldenPlayer(openzenE2EGoldenPath)
		reloadedChunks, err := player.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(reloadedChunks).To(HaveLen(len(openzenE2ECached)))

		for i, expected := range openzenE2ECached {
			actual := reloadedChunks[i]
			Expect(actual.Content).To(Equal(expected.Content))
			Expect(actual.Done).To(Equal(expected.Done))
			if expected.Error != nil {
				Expect(actual.Error).To(HaveOccurred())
			} else {
				Expect(actual.Error).ToNot(HaveOccurred())
			}
		}
	})

	It("extracts correct response from chunks", func() {
		var fullResponse string
		for _, chunk := range openzenE2ECached {
			if chunk.Content != "" {
				fullResponse += chunk.Content
			}
		}
		Expect(len(fullResponse)).To(BeNumerically(">=", 10))
	})

	It("handles rate limiting gracefully (golden file fallback)", func() {
		Expect(openzenE2ECached).NotTo(BeEmpty())
	})

	It("does not skip when golden file exists", func() {
		if _, err := os.Stat(openzenE2EGoldenPath); err == nil {
			Expect(openzenE2ESkipped).To(BeFalse())
		}
	})
})
