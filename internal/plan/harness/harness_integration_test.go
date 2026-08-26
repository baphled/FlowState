package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan/harness"
	"github.com/baphled/flowstate/internal/provider"
)

type integrationMockStreamer struct {
	responses []string
	callCount int
}

func (m *integrationMockStreamer) Stream(_ context.Context, _, _ string) (<-chan provider.StreamChunk, error) {
	idx := m.callCount
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	resp := m.responses[idx]
	m.callCount++
	ch := make(chan provider.StreamChunk, 10)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: resp}
	}()
	return ch, nil
}

var _ = Describe("Integration", Label("integration"), func() {
	var (
		h           *harness.Harness
		ctx         context.Context
		projectRoot string
	)

	BeforeEach(func() {
		cwd, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		projectRoot = filepath.Join(cwd, "..", "..", "..")
		h = newTestHarness(projectRoot)
		ctx = context.Background()
	})

	It("accepts valid plan on first attempt", func() {
		validPlan := loadValidPlan()
		streamer := &integrationMockStreamer{responses: []string{validPlan}}

		result, err := h.Evaluate(ctx, streamer, "planner", "Generate a plan")

		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.AttemptCount).To(Equal(1))
		Expect(result.ValidationResult).NotTo(BeNil())
		Expect(result.ValidationResult.Valid).To(BeTrue())
		Expect(result.FinalScore).To(BeNumerically(">=", 0.7))
		Expect(streamer.callCount).To(Equal(1))
	})

	It("retries on invalid plan and succeeds", func() {
		harnessWithRetry := newTestHarness(projectRoot, harness.WithMaxRetries(2))
		streamer := &integrationMockStreamer{
			responses: []string{invalidPlan(), loadValidPlan()},
		}

		result, err := harnessWithRetry.Evaluate(ctx, streamer, "planner", "Generate a plan")

		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.AttemptCount).To(Equal(2))
		Expect(result.ValidationResult).NotTo(BeNil())
		Expect(result.ValidationResult.Valid).To(BeTrue())
		Expect(streamer.callCount).To(Equal(2))
	})

	It("returns best-effort after max retries", func() {
		harnessWithRetry := newTestHarness(projectRoot, harness.WithMaxRetries(3))
		streamer := &integrationMockStreamer{
			responses: []string{invalidPlan(), invalidPlan(), invalidPlan()},
		}

		result, err := harnessWithRetry.Evaluate(ctx, streamer, "planner", "Generate a plan")

		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.AttemptCount).To(Equal(3))
		Expect(result.ValidationResult).NotTo(BeNil())
		Expect(result.ValidationResult.Valid).To(BeFalse())
		Expect(result.ValidationResult.Warnings).NotTo(BeEmpty())
		Expect(streamer.callCount).To(Equal(3))
	})

	It("bypasses validation for interview-phase messages", func() {
		interviewResponse := "Can you tell me more about your project requirements?"
		streamer := &integrationMockStreamer{responses: []string{interviewResponse}}

		result, err := h.Evaluate(ctx, streamer, "planner", "Start planning")

		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.PlanText).To(Equal(interviewResponse))
		Expect(result.AttemptCount).To(Equal(1))
		Expect(result.ValidationResult).To(BeNil())
		Expect(streamer.callCount).To(Equal(1))
	})

	It("respects context cancellation", func() {
		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		streamer := &integrationMockStreamer{responses: []string{loadValidPlan()}}

		result, err := h.Evaluate(cancelledCtx, streamer, "planner", "Generate a plan")

		Expect(err).To(MatchError(context.Canceled))
		Expect(result).To(BeNil())
		Expect(streamer.callCount).To(Equal(0))
	})
})

func BenchmarkHarnessOverhead(b *testing.B) {
	cwd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	projectRoot := filepath.Join(cwd, "..", "..", "..")
	h := newTestHarness(projectRoot)
	data, err := os.ReadFile("../testdata/valid_plan.md")
	if err != nil {
		b.Fatal(err)
	}
	validPlan := string(data)

	b.ResetTimer()
	for b.Loop() {
		streamer := &integrationMockStreamer{responses: []string{validPlan}}
		_, _ = h.Evaluate(context.Background(), streamer, "planner", "Generate a plan")
	}
}
