package vaultindex_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/vaultindex"
)

func words(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "w"
	}
	return strings.Join(parts, " ")
}

var _ = Describe("Chunker", func() {
	Describe("NewChunker defaults", func() {
		It("falls back to defaults when size is non-positive", func() {
			c := vaultindex.NewChunker(0, 50)
			Expect(c.Size()).To(Equal(vaultindex.DefaultChunkSize))
			Expect(c.Overlap()).To(Equal(50))
		})

		It("falls back to default overlap when overlap is invalid", func() {
			c := vaultindex.NewChunker(200, 250)
			Expect(c.Size()).To(Equal(200))
			Expect(c.Overlap()).To(Equal(vaultindex.DefaultChunkOverlap))
		})
	})

	Describe("Chunk", func() {
		It("returns nil for empty input", func() {
			c := vaultindex.NewChunker(10, 2)
			Expect(c.Chunk("")).To(BeNil())
		})

		It("returns nil for whitespace-only input", func() {
			c := vaultindex.NewChunker(10, 2)
			Expect(c.Chunk("   \n\t  ")).To(BeNil())
		})

		It("returns a single chunk when text is shorter than the window", func() {
			c := vaultindex.NewChunker(10, 2)
			Expect(c.Chunk("hello world")).To(Equal([]string{"hello world"}))
		})

		It("splits long input into windows of the configured size", func() {
			c := vaultindex.NewChunker(4, 1)
			chunks := c.Chunk("a b c d e f g h")
			Expect(chunks).To(Equal([]string{
				"a b c d",
				"d e f g",
				"g h",
			}))
		})

		It("preserves the requested overlap between consecutive chunks", func() {
			c := vaultindex.NewChunker(5, 2)
			chunks := c.Chunk(words(12))
			Expect(len(chunks)).To(BeNumerically(">=", 2))
			firstTokens := strings.Fields(chunks[0])
			secondTokens := strings.Fields(chunks[1])
			Expect(firstTokens[len(firstTokens)-2:]).To(Equal(secondTokens[:2]))
		})
	})
})
