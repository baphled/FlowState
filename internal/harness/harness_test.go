package harness_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/harness"
	"github.com/baphled/flowstate/internal/provider"
)

// Compile-time interface satisfaction checks.
// These fail at build time if any mock does not implement the interface correctly.
var _ harness.Validator = (*mockValidator)(nil)
var _ harness.Critic = (*mockCritic)(nil)
var _ harness.Voter = (*mockVoter)(nil)
var _ harness.Evaluator = (*mockEvaluator)(nil)

type mockValidator struct{}

func (m *mockValidator) Validate(_ string) (*harness.ValidationResult, error) {
	return &harness.ValidationResult{Valid: true}, nil
}

type mockCritic struct{}

func (m *mockCritic) Review(_ context.Context, _ string, _ *harness.ValidationResult, _ provider.Provider) (*harness.CriticResult, error) {
	return &harness.CriticResult{Verdict: harness.VerdictPass}, nil
}

type mockVoter struct{}

func (m *mockVoter) Vote(_ context.Context, _ harness.Streamer, _ string, _ string, _ string, _ float64) (*harness.VoteResult, error) {
	return &harness.VoteResult{WasTriggered: false}, nil
}

type mockEvaluator struct{}

func (m *mockEvaluator) Evaluate(_ context.Context, _ harness.Streamer, _ string, _ string) (*harness.EvaluationResult, error) {
	return &harness.EvaluationResult{Output: "", AttemptCount: 1}, nil
}

func (m *mockEvaluator) StreamEvaluate(_ context.Context, _ harness.Streamer, _ string, _ string) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

var _ = Describe("Harness interfaces", func() {
	Describe("Validator", func() {
		It("mock satisfies the interface and returns a result", func() {
			var v harness.Validator = &mockValidator{}
			result, err := v.Validate("some output")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Valid).To(BeTrue())
		})
	})

	Describe("Critic", func() {
		It("mock satisfies the interface and returns a result", func() {
			var c harness.Critic = &mockCritic{}
			result, err := c.Review(context.Background(), "output", &harness.ValidationResult{Valid: true}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Verdict).To(Equal(harness.VerdictPass))
		})
	})

	Describe("Voter", func() {
		It("mock satisfies the interface and returns a result", func() {
			var v harness.Voter = &mockVoter{}
			result, err := v.Vote(context.Background(), nil, "agent-1", "message", "output", 0.8)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.WasTriggered).To(BeFalse())
		})
	})

	Describe("Evaluator", func() {
		It("mock satisfies Evaluate and returns a result", func() {
			var e harness.Evaluator = &mockEvaluator{}
			result, err := e.Evaluate(context.Background(), nil, "agent-1", "message")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.AttemptCount).To(Equal(1))
		})

		It("mock satisfies StreamEvaluate and returns a channel", func() {
			var e harness.Evaluator = &mockEvaluator{}
			ch, err := e.StreamEvaluate(context.Background(), nil, "agent-1", "message")
			Expect(err).NotTo(HaveOccurred())
			Expect(ch).NotTo(BeNil())
		})
	})

	Describe("CriticVerdict constants", func() {
		It("VerdictPass has expected value", func() {
			Expect(harness.VerdictPass).To(Equal(harness.CriticVerdict("PASS")))
		})

		It("VerdictFail has expected value", func() {
			Expect(harness.VerdictFail).To(Equal(harness.CriticVerdict("FAIL")))
		})

		It("VerdictDisabled has expected value", func() {
			Expect(harness.VerdictDisabled).To(Equal(harness.CriticVerdict("DISABLED")))
		})
	})

	Describe("ValidationResult zero value", func() {
		It("has Valid=false", func() {
			var result harness.ValidationResult
			Expect(result.Valid).To(BeFalse())
		})
	})
})
