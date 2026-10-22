package anthropic_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
	"github.com/baphled/flowstate/internal/testutils"
)

const (
	anthropicE2EModel       = "claude-3-5-sonnet-20241022"
	anthropicE2ESystemText  = "You are a helpful assistant. Respond directly to the user."
	anthropicGoldenFileName = "anthropic_hello_response.golden.json"
)

var (
	anthropicE2EOnce       sync.Once
	anthropicE2ECached     []provider.StreamChunk
	anthropicE2ESkipped    bool
	anthropicE2EGoldenPath string
)

func createRealAnthropicProvider() provider.Provider {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil
	}

	p, err := anthropic.New(apiKey)
	if err != nil {
		return nil
	}
	return p
}

func goldenHasContent(chunks []provider.StreamChunk) bool {
	for _, c := range chunks {
		if c.Content != "" {
			return true
		}
	}
	return false
}

func ensureAnthropicE2EData() {
	anthropicE2EOnce.Do(func() {
		anthropicE2EGoldenPath = filepath.Join("testdata", anthropicGoldenFileName)

		// Try to load from golden file — only accept it if it has real text content.
		// A golden recorded during an API outage contains only error chunks and must
		// not be treated as valid playback data.
		player := testutils.NewGoldenPlayer(anthropicE2EGoldenPath)
		if chunks, err := player.Load(); err == nil && goldenHasContent(chunks) {
			anthropicE2ECached = chunks
			return
		}

		// No usable golden file — try to record from live API.
		p := createRealAnthropicProvider()
		if p == nil {
			anthropicE2ESkipped = true
			return
		}

		// Build the streaming request
		req := provider.ChatRequest{
			Provider: "anthropic",
			Model:    anthropicE2EModel,
			Messages: []provider.Message{
				{Role: "system", Content: anthropicE2ESystemText},
				{Role: "user", Content: "hello"},
			},
		}

		// Stream from the API
		ch, err := p.Stream(context.Background(), req)
		if err != nil {
			anthropicE2ESkipped = true
			return
		}

		var chunks []provider.StreamChunk
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}

		if !goldenHasContent(chunks) {
			anthropicE2ESkipped = true
			return
		}

		anthropicE2ECached = chunks

		// Save to golden file
		recorder := testutils.NewGoldenRecorder(anthropicE2EGoldenPath)
		_ = recorder.Save(chunks)
	})
}

var _ = Describe("Anthropic E2E Streaming", func() {
	BeforeEach(func() {
		ensureAnthropicE2EData()
		if anthropicE2ESkipped {
			Skip("Anthropic API key not available or golden file cannot be created")
		}
	})

	It("loads cached golden chunks successfully", func() {
		Expect(anthropicE2ECached).NotTo(BeEmpty())
	})

	It("first chunk has content or is final", func() {
		Expect(anthropicE2ECached).ToNot(BeEmpty())
		firstChunk := anthropicE2ECached[0]
		Expect(firstChunk.Content != "" || firstChunk.Error != nil || firstChunk.Done).To(BeTrue())
	})

	It("all chunks have valid structure", func() {
		for _, chunk := range anthropicE2ECached {
			Expect(chunk.EventType == "" || chunk.Content != "" || chunk.Done || chunk.Error != nil).To(BeTrue())
		}
	})

	It("collects text content from chunks", func() {
		var text string
		for _, chunk := range anthropicE2ECached {
			if chunk.Content != "" {
				text += chunk.Content
			}
		}
		Expect(text).NotTo(BeEmpty())
	})

	It("stream completes successfully", func() {
		Expect(anthropicE2ECached).ToNot(BeEmpty())
		lastChunk := anthropicE2ECached[len(anthropicE2ECached)-1]
		Expect(lastChunk.Done).To(BeTrue())
	})

	It("streaming completes without errors", func() {
		hasError := false
		for _, chunk := range anthropicE2ECached {
			if chunk.Error != nil {
				hasError = true
			}
		}
		Expect(hasError).To(BeFalse())
	})

	It("handles multiple sequential chunks correctly", func() {
		Expect(anthropicE2ECached).ToNot(BeEmpty())

		for i := range len(anthropicE2ECached) - 1 {
			currentChunk := anthropicE2ECached[i]
			Expect(currentChunk.Done).To(BeFalse())
		}
	})

	It("golden file persists and reloads correctly", func() {
		Expect(anthropicE2EGoldenPath).NotTo(BeEmpty())
		player := testutils.NewGoldenPlayer(anthropicE2EGoldenPath)
		reloadedChunks, err := player.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(reloadedChunks).To(HaveLen(len(anthropicE2ECached)))

		for i, expected := range anthropicE2ECached {
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
		for _, chunk := range anthropicE2ECached {
			if chunk.Content != "" {
				fullResponse += chunk.Content
			}
		}
		Expect(len(fullResponse)).To(BeNumerically(">=", 10))
	})

	It("handles API availability gracefully via fallback", func() {
		Expect(anthropicE2ECached).NotTo(BeEmpty())
	})

	It("does not skip when golden file exists", func() {
		if _, err := os.Stat(anthropicE2EGoldenPath); err == nil {
			Expect(anthropicE2ESkipped).To(BeFalse())
		}
	})
})
