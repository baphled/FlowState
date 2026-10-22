package ollama_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/ollama"
	"github.com/baphled/flowstate/internal/testutils"
)

const (
	ollamaE2EModel       = "llama3.2"
	ollamaE2ESystemText  = "You are a helpful assistant. Respond directly to the user."
	ollamaGoldenFileName = "ollama_hello_response.golden.json"
	ollamaDefaultHost    = "http://localhost:11434"
)

var (
	ollamaE2EOnce       sync.Once
	ollamaE2ECached     []provider.StreamChunk
	ollamaE2ESkipped    bool
	ollamaE2EGoldenPath string
)

func createRealOllamaProvider() provider.Provider {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = ollamaDefaultHost
	}

	httpClient := &http.Client{
		Timeout: 0,
	}

	p, err := ollama.NewWithClient(host, httpClient)
	if err != nil {
		return nil
	}
	return p
}

func ensureOllamaE2EData() {
	ollamaE2EOnce.Do(func() {
		ollamaE2EGoldenPath = filepath.Join("testdata", ollamaGoldenFileName)

		// Try to load from golden file
		player := testutils.NewGoldenPlayer(ollamaE2EGoldenPath)
		if chunks, err := player.Load(); err == nil {
			ollamaE2ECached = chunks
			return
		}

		// No golden file, try to record from live API
		p := createRealOllamaProvider()
		if p == nil {
			ollamaE2ESkipped = true
			return
		}

		// Build the streaming request
		req := provider.ChatRequest{
			Provider: "ollama",
			Model:    ollamaE2EModel,
			Messages: []provider.Message{
				{Role: "system", Content: ollamaE2ESystemText},
				{Role: "user", Content: "hello"},
			},
		}

		// Stream from the API
		ch, err := p.Stream(context.Background(), req)
		if err != nil {
			ollamaE2ESkipped = true
			return
		}

		var chunks []provider.StreamChunk
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}

		if len(chunks) == 0 {
			ollamaE2ESkipped = true
			return
		}

		ollamaE2ECached = chunks

		// Save to golden file
		recorder := testutils.NewGoldenRecorder(ollamaE2EGoldenPath)
		_ = recorder.Save(chunks)
	})
}

var _ = Describe("Ollama E2E Streaming", func() {
	BeforeEach(func() {
		ensureOllamaE2EData()
		if ollamaE2ESkipped {
			Skip("Ollama API not available or golden file cannot be created")
		}
	})

	It("loads cached golden chunks successfully", func() {
		Expect(ollamaE2ECached).NotTo(BeEmpty())
	})

	It("first chunk has content or is final", func() {
		Expect(ollamaE2ECached).ToNot(BeEmpty())
		firstChunk := ollamaE2ECached[0]
		Expect(firstChunk.Content != "" || firstChunk.Error != nil || firstChunk.Done).To(BeTrue())
	})

	It("all chunks have valid structure", func() {
		for _, chunk := range ollamaE2ECached {
			Expect(chunk.EventType == "" || chunk.Content != "" || chunk.Done || chunk.Error != nil).To(BeTrue())
		}
	})

	It("collects text content from chunks", func() {
		var text string
		for _, chunk := range ollamaE2ECached {
			if chunk.Content != "" {
				text += chunk.Content
			}
		}
		Expect(text).NotTo(BeEmpty())
	})

	It("stream completes successfully", func() {
		Expect(ollamaE2ECached).ToNot(BeEmpty())
		lastChunk := ollamaE2ECached[len(ollamaE2ECached)-1]
		Expect(lastChunk.Done).To(BeTrue())
	})

	It("streaming completes without errors", func() {
		hasError := false
		for _, chunk := range ollamaE2ECached {
			if chunk.Error != nil {
				hasError = true
			}
		}
		Expect(hasError).To(BeFalse())
	})

	It("handles multiple sequential chunks correctly", func() {
		Expect(ollamaE2ECached).ToNot(BeEmpty())

		for i := range len(ollamaE2ECached) - 1 {
			currentChunk := ollamaE2ECached[i]
			Expect(currentChunk.Done).To(BeFalse())
		}
	})

	It("golden file persists and reloads correctly", func() {
		Expect(ollamaE2EGoldenPath).NotTo(BeEmpty())
		player := testutils.NewGoldenPlayer(ollamaE2EGoldenPath)
		reloadedChunks, err := player.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(reloadedChunks).To(HaveLen(len(ollamaE2ECached)))

		for i, expected := range ollamaE2ECached {
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
		for _, chunk := range ollamaE2ECached {
			if chunk.Content != "" {
				fullResponse += chunk.Content
			}
		}
		Expect(len(fullResponse)).To(BeNumerically(">=", 10))
	})

	It("handles API availability gracefully via fallback", func() {
		Expect(ollamaE2ECached).NotTo(BeEmpty())
	})

	It("does not skip when golden file exists", func() {
		if _, err := os.Stat(ollamaE2EGoldenPath); err == nil {
			Expect(ollamaE2ESkipped).To(BeFalse())
		}
	})
})
