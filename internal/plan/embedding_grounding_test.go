package plan_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/provider"
)

// mockEmbedProvider implements provider.Provider for deterministic embeddings.
type mockEmbedProvider struct{}

func (m *mockEmbedProvider) Name() string { return "mock-embed" }
func (m *mockEmbedProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}
func (m *mockEmbedProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (m *mockEmbedProvider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	h := 0.0
	for _, c := range req.Input {
		h += float64(c)
	}
	return []float64{h / 1000.0, 1.0 - h/1000.0}, nil
}
func (m *mockEmbedProvider) Models() ([]provider.Model, error) { return nil, nil }

var _ = Describe("EmbeddingGrounder", func() {
	var (
		tmpDir   string
		g        *plan.EmbeddingGrounder
		provider provider.Provider
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "embedtest")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { os.RemoveAll(tmpDir) })
		// Write a simple .go file
		file := filepath.Join(tmpDir, "foo.go")
		os.WriteFile(file, []byte("package foo\nfunc Bar() {}\ntype Baz struct{}\n"), 0o600)
		g = plan.NewEmbeddingGrounder()
		provider = &mockEmbedProvider{}
	})

	It("returns non-error for valid Go project directory", func() {
		ctx := context.Background()
		out, err := g.InjectContext(ctx, tmpDir, "Bar", provider)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).NotTo(BeEmpty())
	})

	It("returns empty string when query matches no code", func() {
		ctx := context.Background()
		out, err := g.InjectContext(ctx, tmpDir, "NonexistentSymbol", provider)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(BeEmpty())
	})

	It("returns context string with relevant code", func() {
		ctx := context.Background()
		out, err := g.InjectContext(ctx, tmpDir, "Bar", provider)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(ContainSubstring("### Relevant Code"))
		Expect(out).To(ContainSubstring("Bar"))
	})

	It("returns consistent LastIndexed value across calls", func() {
		ctx := context.Background()
		_, err := g.InjectContext(ctx, tmpDir, "Bar", provider)
		Expect(err).NotTo(HaveOccurred())
		first := g.LastIndexed()
		_, err = g.InjectContext(ctx, tmpDir, "Baz", provider)
		Expect(err).NotTo(HaveOccurred())
		second := g.LastIndexed()
		Expect(second).To(Equal(first))
	})

	It("returns error for non-existent directory", func() {
		ctx := context.Background()
		_, err := g.InjectContext(ctx, "/nonexistent/path", "Bar", provider)
		Expect(err).To(HaveOccurred())
	})

	It("handles empty directory without error", func() {
		emptyDir, err := os.MkdirTemp("", "emptyembedtest")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { os.RemoveAll(emptyDir) })
		ctx := context.Background()
		out, err := g.InjectContext(ctx, emptyDir, "Bar", provider)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(BeEmpty())
	})

	It("skips non-Go files in directory", func() {
		txtFile := filepath.Join(tmpDir, "readme.txt")
		Expect(os.WriteFile(txtFile, []byte("This is not Go code"), 0o600)).To(Succeed())
		ctx := context.Background()
		out, err := g.InjectContext(ctx, tmpDir, "readme", provider)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(BeEmpty())
	})
})
