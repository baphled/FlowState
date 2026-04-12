package copilot_test

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
	copilotE2EModel       = "gpt-4o"
	copilotE2ESystemText  = "You are a helpful assistant. Respond directly to the user."
	copilotGoldenFileName = "copilot_hello_response.golden.json"
)

var (
	copilotE2EOnce       sync.Once
	copilotE2ECached     []provider.StreamChunk
	copilotE2ESkipped    bool
	copilotE2EGoldenPath string
)

func createRealCopilotProvider() *openaiAPI.Client {
	token := os.Getenv("GITHUB_COPILOT_TOKEN")
	if token == "" {
		return nil
	}

	opts := []option.RequestOption{
		option.WithAPIKey(token),
		option.WithBaseURL("https://api.githubcopilot.com"),
	}
	client := openaiAPI.NewClient(opts...)
	return &client
}

func ensureCopilotE2EData() {
	copilotE2EOnce.Do(func() {
		copilotE2EGoldenPath = filepath.Join("testdata", copilotGoldenFileName)

		// Try to load from golden file
		player := testutils.NewGoldenPlayer(copilotE2EGoldenPath)
		if chunks, err := player.Load(); err == nil {
			copilotE2ECached = chunks
			return
		}

		// No golden file, try to record from live API
		client := createRealCopilotProvider()
		if client == nil {
			copilotE2ESkipped = true
			return
		}

		// Build and execute the streaming request
		messages := openaicompat.BuildMessages([]provider.Message{
			{Role: "system", Content: copilotE2ESystemText},
			{Role: "user", Content: "hello"},
		})

		params := openaiAPI.ChatCompletionNewParams{
			Model:    copilotE2EModel,
			Messages: messages,
		}

		// Use RunStream to get chunks
		ch := openaicompat.RunStream(context.Background(), *client, params, "github-copilot")
		var chunks []provider.StreamChunk
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}

		if len(chunks) == 0 {
			copilotE2ESkipped = true
			return
		}

		copilotE2ECached = chunks

		// Save to golden file
		recorder := testutils.NewGoldenRecorder(copilotE2EGoldenPath)
		_ = recorder.Save(chunks)
	})
}

var _ = Describe("GitHub Copilot E2E Streaming", func() {
	BeforeEach(func() {
		ensureCopilotE2EData()
		if copilotE2ESkipped {
			Skip("GitHub Copilot token not available or golden file cannot be created")
		}
	})

	It("loads cached golden chunks successfully", func() {
		Expect(copilotE2ECached).NotTo(BeEmpty())
	})

	It("first chunk has content", func() {
		Expect(copilotE2ECached).ToNot(BeEmpty())
		firstChunk := copilotE2ECached[0]
		Expect(firstChunk.Content != "" || firstChunk.Error != nil).To(BeTrue())
	})

	It("all chunks have valid structure", func() {
		for _, chunk := range copilotE2ECached {
			Expect(chunk.EventType == "" || chunk.Content != "" || chunk.Done || chunk.Error != nil).To(BeTrue())
		}
	})

	It("collects all text content in order", func() {
		var text string
		for _, chunk := range copilotE2ECached {
			if chunk.Content != "" {
				text += chunk.Content
			}
		}
		Expect(text).NotTo(BeEmpty())
	})

	It("last chunk marks completion", func() {
		Expect(copilotE2ECached).ToNot(BeEmpty())
		lastChunk := copilotE2ECached[len(copilotE2ECached)-1]
		Expect(lastChunk.Done).To(BeTrue())
	})

	It("streaming completes without errors", func() {
		hasError := false
		for _, chunk := range copilotE2ECached {
			if chunk.Error != nil {
				hasError = true
			}
		}
		Expect(hasError).To(BeFalse())
	})

	It("handles multiple sequential chunks correctly", func() {
		Expect(copilotE2ECached).ToNot(BeEmpty())

		for i := range len(copilotE2ECached) - 1 {
			currentChunk := copilotE2ECached[i]
			Expect(currentChunk.Done).To(BeFalse())
		}
	})

	It("golden file persists and reloads correctly", func() {
		Expect(copilotE2EGoldenPath).NotTo(BeEmpty())
		player := testutils.NewGoldenPlayer(copilotE2EGoldenPath)
		reloadedChunks, err := player.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(reloadedChunks).To(HaveLen(len(copilotE2ECached)))

		for i, expected := range copilotE2ECached {
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
		for _, chunk := range copilotE2ECached {
			if chunk.Content != "" {
				fullResponse += chunk.Content
			}
		}
		Expect(len(fullResponse)).To(BeNumerically(">=", 10))
	})

	It("handles authentication gracefully (golden file fallback)", func() {
		Expect(copilotE2ECached).NotTo(BeEmpty())
	})

	It("does not skip when golden file exists", func() {
		if _, err := os.Stat(copilotE2EGoldenPath); err == nil {
			Expect(copilotE2ESkipped).To(BeFalse())
		}
	})
})
