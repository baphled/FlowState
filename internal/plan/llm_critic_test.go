package plan_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/provider"
)

type mockChatProvider struct {
	response string
}

func (m *mockChatProvider) Name() string { return "mock" }
func (m *mockChatProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}
func (m *mockChatProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{Message: provider.Message{Role: "assistant", Content: m.response}}, nil
}
func (m *mockChatProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}
func (m *mockChatProvider) Models() ([]provider.Model, error) { return nil, nil }

type errorChatProvider struct{}

func (m *errorChatProvider) Name() string { return "error-mock" }
func (m *errorChatProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errors.New("chat error")
}
func (m *errorChatProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errors.New("chat error")
}
func (m *errorChatProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, errors.New("chat error")
}
func (m *errorChatProvider) Models() ([]provider.Model, error) { return nil, errors.New("chat error") }

var _ = Describe("LLMCritic", func() {
	var (
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("returns PASS verdict when enabled and LLM responds PASS", func() {
		critic := plan.NewLLMCritic(true, "mock-model")
		mockProvider := &mockChatProvider{response: "VERDICT: PASS\nISSUES: none\nSUGGESTIONS: none\nCONFIDENCE: 1.0"}
		result, err := critic.Review(ctx, "# Plan\nDo X", mockProvider)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.Verdict).To(Equal(plan.VerdictPass))
		Expect(result.Issues).To(BeEmpty())
		Expect(result.Confidence).To(Equal(1.0))
	})

	It("returns FAIL verdict and issues when enabled and LLM responds FAIL", func() {
		critic := plan.NewLLMCritic(true, "mock-model")
		mockProvider := &mockChatProvider{response: "VERDICT: FAIL\nISSUES:\nMissing step 2\nSUGGESTIONS:\nAdd step 2\nCONFIDENCE: 0.7"}
		result, err := critic.Review(ctx, "# Plan\nDo X", mockProvider)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.Verdict).To(Equal(plan.VerdictFail))
		Expect(result.Issues).NotTo(BeEmpty())
		Expect(result.Confidence).To(Equal(0.7))
	})

	It("returns DISABLED verdict when disabled", func() {
		critic := plan.NewLLMCritic(false, "mock-model")
		mockProvider := &mockChatProvider{response: "VERDICT: PASS\nISSUES: none\nSUGGESTIONS: none\nCONFIDENCE: 1.0"}
		result, err := critic.Review(ctx, "# Plan\nDo X", mockProvider)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.Verdict).To(Equal(plan.VerdictDisabled))
		Expect(result.Confidence).To(Equal(1.0))
	})

	It("returns FAIL with unspecified issues when FAIL verdict has no issue lines", func() {
		critic := plan.NewLLMCritic(true, "mock-model")
		mockProvider := &mockChatProvider{response: "VERDICT: FAIL\nISSUES: none\nSUGGESTIONS: none\nCONFIDENCE: 0.5"}
		result, err := critic.Review(ctx, "# Plan\nDo X", mockProvider)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Verdict).To(Equal(plan.VerdictFail))
		Expect(result.Issues).To(ContainElement("Unspecified failure"))
	})

	It("parses suggestions from LLM response", func() {
		critic := plan.NewLLMCritic(true, "mock-model")
		mockProvider := &mockChatProvider{response: "VERDICT: PASS\nISSUES: none\nSUGGESTIONS:\nAdd more detail\nImprove structure\nCONFIDENCE: 0.9"}
		result, err := critic.Review(ctx, "# Plan\nDo X", mockProvider)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Verdict).To(Equal(plan.VerdictPass))
		Expect(result.Suggestions).To(HaveLen(2))
		Expect(result.Suggestions).To(ContainElement("Add more detail"))
		Expect(result.Suggestions).To(ContainElement("Improve structure"))
	})

	It("defaults to PASS verdict when response has no VERDICT line", func() {
		critic := plan.NewLLMCritic(true, "mock-model")
		mockProvider := &mockChatProvider{response: "The plan looks good overall."}
		result, err := critic.Review(ctx, "# Plan\nDo X", mockProvider)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Verdict).To(Equal(plan.VerdictPass))
	})

	It("handles empty LLM response gracefully", func() {
		critic := plan.NewLLMCritic(true, "mock-model")
		mockProvider := &mockChatProvider{response: ""}
		result, err := critic.Review(ctx, "# Plan\nDo X", mockProvider)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.Verdict).To(Equal(plan.VerdictPass))
	})

	It("returns error when provider Chat fails", func() {
		critic := plan.NewLLMCritic(true, "mock-model")
		mockProvider := &errorChatProvider{}
		_, err := critic.Review(ctx, "# Plan\nDo X", mockProvider)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("chat error"))
	})
})
