package harness_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan/harness"
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
	panic("mockChatProvider.Embed must not be called in LLMCritic tests")
}
func (m *mockChatProvider) Models() ([]provider.Model, error) {
	panic("mockChatProvider.Models must not be called in LLMCritic tests")
}

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

type capturingChatProvider struct {
	response string
	called   bool
}

func (m *capturingChatProvider) Name() string { return "capturing-mock" }
func (m *capturingChatProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}
func (m *capturingChatProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	m.called = true
	return provider.ChatResponse{Message: provider.Message{Role: "assistant", Content: m.response}}, nil
}
func (m *capturingChatProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	panic("capturingChatProvider.Embed must not be called in LLMCritic tests")
}
func (m *capturingChatProvider) Models() ([]provider.Model, error) {
	panic("capturingChatProvider.Models must not be called in LLMCritic tests")
}

func newTestCritic(enabled bool) *harness.LLMCritic {
	critic, err := harness.NewLLMCritic(enabled, "mock-model")
	Expect(err).NotTo(HaveOccurred())
	return critic
}

func validCriticResponse() string {
	return "VERDICT: PASS\nCONFIDENCE: 0.95\n\nRUBRIC:\n" +
		"- FEASIBILITY: PASS - All tasks are independently executable\n" +
		"- CONSISTENCY: PASS - Rationale aligns with task definitions\n" +
		"- TASK COMPLETENESS: PASS - Acceptance criteria are specific and verifiable\n" +
		"- WAVE ORDERING LOGIC: PASS - Dependencies correctly sequenced across waves\n" +
		"- PLAN COVERAGE: PASS - Tasks cover the full stated objective\n" +
		"- EVIDENCE QUALITY: PASS - Code references are accurate and grounded\n\n" +
		"ISSUES:\n- none\n\nSUGGESTIONS:\n- none"
}

func failingCriticResponse() string {
	return "VERDICT: FAIL\nCONFIDENCE: 0.80\n\nRUBRIC:\n" +
		"- FEASIBILITY: PASS - Tasks are executable\n" +
		"- CONSISTENCY: FAIL - Success criteria mention features not covered by tasks\n" +
		"- TASK COMPLETENESS: PASS - Criteria are verifiable\n" +
		"- WAVE ORDERING LOGIC: PASS - Dependencies are correct\n" +
		"- PLAN COVERAGE: FAIL - Missing verification tasks for error handling\n" +
		"- EVIDENCE QUALITY: PASS - References are grounded\n\n" +
		"ISSUES:\n" +
		"- Success criteria reference rate limiting but no task implements it\n" +
		"- No tasks cover error handling verification\n\n" +
		"SUGGESTIONS:\n" +
		"- Add a task for rate limiting implementation\n" +
		"- Add error handling verification tasks to Wave 3"
}

var _ = Describe("LLMCritic", func() {
	var (
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Review", func() {
		Context("when critic is disabled", func() {
			It("returns VerdictDisabled without calling the provider", func() {
				critic := newTestCritic(false)
				capProvider := &capturingChatProvider{response: validCriticResponse()}
				result, err := critic.Review(ctx, nil, "# Plan text", nil, capProvider)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Verdict).To(Equal(harness.VerdictDisabled))
				Expect(result.Confidence).To(Equal(1.0))
				Expect(capProvider.called).To(BeFalse())
			})
		})

		Context("when provider returns valid PASS response", func() {
			It("parses verdict, confidence, and rubric results", func() {
				critic := newTestCritic(true)
				mockProv := &mockChatProvider{response: validCriticResponse()}
				result, err := critic.Review(ctx, nil, "# Plan text", nil, mockProv)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Verdict).To(Equal(harness.VerdictPass))
				Expect(result.Confidence).To(Equal(0.95))
				Expect(result.RubricResults).To(HaveLen(6))
				Expect(result.RubricResults).To(HaveKeyWithValue("FEASIBILITY", "PASS"))
				Expect(result.RubricResults).To(HaveKeyWithValue("CONSISTENCY", "PASS"))
				Expect(result.RubricResults).To(HaveKeyWithValue("TASK COMPLETENESS", "PASS"))
				Expect(result.RubricResults).To(HaveKeyWithValue("WAVE ORDERING LOGIC", "PASS"))
				Expect(result.RubricResults).To(HaveKeyWithValue("PLAN COVERAGE", "PASS"))
				Expect(result.RubricResults).To(HaveKeyWithValue("EVIDENCE QUALITY", "PASS"))
				Expect(result.Issues).To(BeEmpty())
				Expect(result.Suggestions).To(BeEmpty())
			})
		})

		Context("when provider returns valid FAIL response with issues", func() {
			It("parses verdict, rubric failures, issues, and suggestions", func() {
				critic := newTestCritic(true)
				mockProv := &mockChatProvider{response: failingCriticResponse()}
				result, err := critic.Review(ctx, nil, "# Plan text", nil, mockProv)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Verdict).To(Equal(harness.VerdictFail))
				Expect(result.Confidence).To(Equal(0.80))
				Expect(result.RubricResults).To(HaveLen(6))
				Expect(result.RubricResults).To(HaveKeyWithValue("CONSISTENCY", "FAIL"))
				Expect(result.RubricResults).To(HaveKeyWithValue("PLAN COVERAGE", "FAIL"))
				Expect(result.RubricResults).To(HaveKeyWithValue("FEASIBILITY", "PASS"))
				Expect(result.Issues).To(HaveLen(2))
				Expect(result.Issues).To(ContainElement(ContainSubstring("rate limiting")))
				Expect(result.Suggestions).To(HaveLen(2))
				Expect(result.Suggestions).To(ContainElement(ContainSubstring("rate limiting")))
			})
		})

		Context("when provider returns empty response", func() {
			It("returns an error, not a silent PASS", func() {
				critic := newTestCritic(true)
				mockProv := &mockChatProvider{response: ""}
				result, err := critic.Review(ctx, nil, "# Plan text", nil, mockProv)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("critic response missing VERDICT"))
				Expect(result).To(BeNil())
			})
		})

		Context("when provider returns free-form prose", func() {
			It("returns an error for unstructured response", func() {
				critic := newTestCritic(true)
				mockProv := &mockChatProvider{response: "The plan looks good overall. I think it covers all the bases."}
				result, err := critic.Review(ctx, nil, "# Plan text", nil, mockProv)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("critic response missing VERDICT"))
				Expect(result).To(BeNil())
			})
		})

		Context("when provider returns response with missing RUBRIC block", func() {
			It("returns an error for incomplete response", func() {
				critic := newTestCritic(true)
				response := "VERDICT: PASS\nCONFIDENCE: 0.9\n\nISSUES:\n- none\n\nSUGGESTIONS:\n- none"
				mockProv := &mockChatProvider{response: response}
				result, err := critic.Review(ctx, nil, "# Plan text", nil, mockProv)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("RUBRIC"))
				Expect(result).To(BeNil())
			})
		})

		Context("when provider returns response with fewer than 6 rubric entries", func() {
			It("returns an error for incomplete rubric", func() {
				critic := newTestCritic(true)
				response := "VERDICT: PASS\nCONFIDENCE: 0.9\n\nRUBRIC:\n" +
					"- FEASIBILITY: PASS - Tasks are fine\n" +
					"- CONSISTENCY: PASS - Looks good\n\n" +
					"ISSUES:\n- none\n\nSUGGESTIONS:\n- none"
				mockProv := &mockChatProvider{response: response}
				result, err := critic.Review(ctx, nil, "# Plan text", nil, mockProv)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("rubric"))
				Expect(result).To(BeNil())
			})
		})

		Context("when provider returns invalid verdict value", func() {
			It("returns an error for unrecognised verdict", func() {
				critic := newTestCritic(true)
				response := "VERDICT: MAYBE\nCONFIDENCE: 0.9\n\nRUBRIC:\n" +
					"- FEASIBILITY: PASS - ok\n" +
					"- CONSISTENCY: PASS - ok\n" +
					"- TASK COMPLETENESS: PASS - ok\n" +
					"- WAVE ORDERING LOGIC: PASS - ok\n" +
					"- PLAN COVERAGE: PASS - ok\n" +
					"- EVIDENCE QUALITY: PASS - ok\n\n" +
					"ISSUES:\n- none\n\nSUGGESTIONS:\n- none"
				mockProv := &mockChatProvider{response: response}
				result, err := critic.Review(ctx, nil, "# Plan text", nil, mockProv)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("verdict"))
				Expect(result).To(BeNil())
			})
		})

		Context("when provider returns response with missing CONFIDENCE", func() {
			It("returns an error for missing confidence", func() {
				critic := newTestCritic(true)
				response := "VERDICT: PASS\n\nRUBRIC:\n" +
					"- FEASIBILITY: PASS - ok\n" +
					"- CONSISTENCY: PASS - ok\n" +
					"- TASK COMPLETENESS: PASS - ok\n" +
					"- WAVE ORDERING LOGIC: PASS - ok\n" +
					"- PLAN COVERAGE: PASS - ok\n" +
					"- EVIDENCE QUALITY: PASS - ok\n\n" +
					"ISSUES:\n- none\n\nSUGGESTIONS:\n- none"
				mockProv := &mockChatProvider{response: response}
				result, err := critic.Review(ctx, nil, "# Plan text", nil, mockProv)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("CONFIDENCE"))
				Expect(result).To(BeNil())
			})
		})

		Context("when provider Chat fails", func() {
			It("returns the provider error", func() {
				critic := newTestCritic(true)
				mockProv := &errorChatProvider{}
				result, err := critic.Review(ctx, nil, "# Plan text", nil, mockProv)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("chat error"))
				Expect(result).To(BeNil())
			})
		})

		Context("when VERDICT is case-insensitive", func() {
			It("accepts lowercase pass", func() {
				critic := newTestCritic(true)
				response := "VERDICT: pass\nCONFIDENCE: 0.85\n\nRUBRIC:\n" +
					"- FEASIBILITY: PASS - ok\n" +
					"- CONSISTENCY: PASS - ok\n" +
					"- TASK COMPLETENESS: PASS - ok\n" +
					"- WAVE ORDERING LOGIC: PASS - ok\n" +
					"- PLAN COVERAGE: PASS - ok\n" +
					"- EVIDENCE QUALITY: PASS - ok\n\n" +
					"ISSUES:\n- none\n\nSUGGESTIONS:\n- none"
				mockProv := &mockChatProvider{response: response}
				result, err := critic.Review(ctx, nil, "# Plan text", nil, mockProv)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Verdict).To(Equal(harness.VerdictPass))
			})
		})
	})

	Describe("Harness integration", func() {
		Context("when critic is enabled and plan passes validation and critic", func() {
			It("returns a passing evaluation result with critic approval", func() {
				projectRoot := projectRootFromWorkingDir()
				criticProv := &mockChatProvider{response: validCriticResponse()}
				critic := newTestCritic(true)
				h := newTestHarness(projectRoot, harness.WithCritic(critic, criticProv))
				streamer := &mockStreamer{responses: []string{loadValidPlan()}}
				result, err := h.Evaluate(context.Background(), streamer, "planner", "Generate a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.AttemptCount).To(Equal(1))
				Expect(result.ValidationResult).NotTo(BeNil())
				Expect(result.ValidationResult.Valid).To(BeTrue())
			})
		})

		Context("when critic is enabled and critic returns FAIL", func() {
			It("triggers a retry with critic feedback", func() {
				projectRoot := projectRootFromWorkingDir()
				criticProv := &mockChatProvider{response: failingCriticResponse()}
				critic := newTestCritic(true)
				h := newTestHarness(projectRoot, harness.WithCritic(critic, criticProv), harness.WithMaxRetries(3))
				streamer := &mockStreamer{responses: []string{
					loadValidPlan(),
					loadValidPlan(),
					loadValidPlan(),
				}}
				result, err := h.Evaluate(context.Background(), streamer, "planner", "Generate a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.AttemptCount).To(BeNumerically(">", 1))
			})
		})

		Context("when critic is not configured", func() {
			It("skips critic and returns on validation pass alone", func() {
				projectRoot := projectRootFromWorkingDir()
				h := newTestHarness(projectRoot)
				streamer := &mockStreamer{responses: []string{loadValidPlan()}}
				result, err := h.Evaluate(context.Background(), streamer, "planner", "Generate a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.AttemptCount).To(Equal(1))
				Expect(result.ValidationResult).NotTo(BeNil())
				Expect(result.ValidationResult.Valid).To(BeTrue())
			})
		})
	})
})
