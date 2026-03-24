package plan_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/provider"
)

type mockChatProvider struct {
	response string
}

func (m *mockChatProvider) Name() string { return "mock" }
func (m *mockChatProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}
func (m *mockChatProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{Message: provider.Message{Role: "assistant", Content: m.response}}, nil
}
func (m *mockChatProvider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}
func (m *mockChatProvider) Models() ([]provider.Model, error) { return nil, nil }

var _ = Describe("LLMCritic", func() {
	var (
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("returns PASS verdict when enabled and LLM responds PASS", func() {
		critic := plan.NewLLMCritic(true, "mock-model")
		provider := &mockChatProvider{response: "VERDICT: PASS\nISSUES: none\nSUGGESTIONS: none\nCONFIDENCE: 1.0"}
		result, err := critic.Review(ctx, "# Plan\nDo X", provider)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.Verdict).To(Equal(plan.VerdictPass))
		Expect(result.Issues).To(BeEmpty())
	})

	It("returns FAIL verdict and issues when enabled and LLM responds FAIL", func() {
		critic := plan.NewLLMCritic(true, "mock-model")
		provider := &mockChatProvider{response: "VERDICT: FAIL\nISSUES: Missing step 2\nSUGGESTIONS: Add step 2\nCONFIDENCE: 0.7"}
		result, err := critic.Review(ctx, "# Plan\nDo X", provider)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.Verdict).To(Equal(plan.VerdictFail))
		Expect(result.Issues).NotTo(BeEmpty())
	})

	It("returns DISABLED verdict when disabled", func() {
		critic := plan.NewLLMCritic(false, "mock-model")
		provider := &mockChatProvider{response: "VERDICT: PASS\nISSUES: none\nSUGGESTIONS: none\nCONFIDENCE: 1.0"}
		result, err := critic.Review(ctx, "# Plan\nDo X", provider)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.Verdict).To(Equal(plan.VerdictDisabled))
	})
})
