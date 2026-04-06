package testutils_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/testutils"
)

func TestTestutils(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Testutils Suite")
}

var _ = Describe("GoldenRecorder and GoldenPlayer", func() {
	var tempDir string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "testutils-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
	})

	It("should save and load golden chunks", func() {
		chunks := []provider.StreamChunk{
			{Content: "Hello", Done: false},
			{Content: " World", Done: true},
		}

		goldenPath := filepath.Join(tempDir, "test.golden.json")
		recorder := testutils.NewGoldenRecorder(goldenPath)
		Expect(recorder.Save(chunks)).To(Succeed())

		player := testutils.NewGoldenPlayer(goldenPath)
		loaded, err := player.Load()
		Expect(err).NotTo(HaveOccurred())

		Expect(loaded).To(HaveLen(2))
		Expect(loaded[0].Content).To(Equal("Hello"))
		Expect(loaded[0].Done).To(BeFalse())
		Expect(loaded[1].Content).To(Equal(" World"))
		Expect(loaded[1].Done).To(BeTrue())
	})

	It("should fail to load missing file", func() {
		player := testutils.NewGoldenPlayer(filepath.Join(tempDir, "nonexistent.json"))
		_, err := player.Load()
		Expect(err).To(HaveOccurred())
	})

	It("should fail to load invalid JSON", func() {
		badPath := filepath.Join(tempDir, "bad.json")
		Expect(os.WriteFile(badPath, []byte("{invalid"), 0o600)).To(Succeed())

		player := testutils.NewGoldenPlayer(badPath)
		_, err := player.Load()
		Expect(err).To(HaveOccurred())
	})

	It("should preserve error messages in golden chunks", func() {
		chunks := []provider.StreamChunk{
			{Content: "text", Error: nil},
			{Content: "", Error: provider.ErrNoChoices},
		}

		goldenPath := filepath.Join(tempDir, "errors.golden.json")
		recorder := testutils.NewGoldenRecorder(goldenPath)
		Expect(recorder.Save(chunks)).To(Succeed())

		player := testutils.NewGoldenPlayer(goldenPath)
		loaded, err := player.Load()
		Expect(err).NotTo(HaveOccurred())

		Expect(loaded[0].Error).ToNot(HaveOccurred())
		Expect(loaded[1].Error).To(HaveOccurred())
		Expect(loaded[1].Error.Error()).To(Equal(provider.ErrNoChoices.Error()))
	})
})

var _ = Describe("ReplayProvider", func() {
	It("should replay chunks from stream", func() {
		chunks := []provider.StreamChunk{
			{Content: "chunk1", Done: false},
			{Content: "chunk2", Done: true},
		}

		rp := testutils.NewReplayProvider("test-replay", chunks)
		Expect(rp.Name()).To(Equal("test-replay"))

		ch, err := rp.Stream(context.Background(), provider.ChatRequest{})
		Expect(err).NotTo(HaveOccurred())

		var received []provider.StreamChunk
		for chunk := range ch {
			received = append(received, chunk)
		}

		Expect(received).To(HaveLen(2))
		Expect(received[0].Content).To(Equal("chunk1"))
		Expect(received[1].Content).To(Equal("chunk2"))
	})

	It("should return empty stream for empty chunks", func() {
		rp := testutils.NewReplayProvider("empty", []provider.StreamChunk{})

		ch, err := rp.Stream(context.Background(), provider.ChatRequest{})
		Expect(err).NotTo(HaveOccurred())

		var received []provider.StreamChunk
		for chunk := range ch {
			received = append(received, chunk)
		}

		Expect(received).To(BeEmpty())
	})

	It("should not implement Chat, Embed, or Models", func() {
		rp := testutils.NewReplayProvider("test", []provider.StreamChunk{})

		_, err := rp.Chat(context.Background(), provider.ChatRequest{})
		Expect(err).To(HaveOccurred())

		_, err = rp.Embed(context.Background(), provider.EmbedRequest{})
		Expect(err).To(HaveOccurred())

		_, err = rp.Models()
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Chunk conversion", func() {
	It("should convert provider chunks to golden chunks and back", func() {
		original := []provider.StreamChunk{
			{
				Content:   "test",
				Done:      false,
				EventType: "content_block_delta",
			},
			{
				Content: "done",
				Done:    true,
				Error:   provider.ErrNoChoices,
			},
		}

		// Test through the public API: save and load
		tempDir, err := os.MkdirTemp("", "testutils-conversion-*")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = os.RemoveAll(tempDir) }()

		goldenPath := filepath.Join(tempDir, "conversion.golden.json")
		recorder := testutils.NewGoldenRecorder(goldenPath)
		Expect(recorder.Save(original)).To(Succeed())

		player := testutils.NewGoldenPlayer(goldenPath)
		recovered, err := player.Load()
		Expect(err).NotTo(HaveOccurred())

		Expect(recovered).To(HaveLen(2))
		Expect(recovered[0].Content).To(Equal("test"))
		Expect(recovered[0].Done).To(BeFalse())
		Expect(recovered[1].Content).To(Equal("done"))
		Expect(recovered[1].Done).To(BeTrue())
		Expect(recovered[1].Error).To(HaveOccurred())
	})
})
