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

type mockStreamer struct {
	responses []string
	callCount int
}

func (m *mockStreamer) Stream(ctx context.Context, agentID, message string) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 10)
	resp := m.responses[m.callCount]
	m.callCount++
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: resp}
	}()
	return ch, nil
}

var _ = Describe("PlanHarness", func() {
	var (
		harness     *plan.PlanHarness
		projectRoot string
	)

	BeforeEach(func() {
		projectRoot = projectRootFromWorkingDir()
		harness = plan.NewPlanHarness(projectRoot)
	})

	It("returns a valid result on the first attempt", func() {
		streamer := &mockStreamer{responses: []string{loadValidPlan()}}
		result, err := harness.Evaluate(context.Background(), streamer, "planner", "Generate a plan")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.AttemptCount).To(Equal(1))
		Expect(result.ValidationResult).NotTo(BeNil())
		Expect(result.ValidationResult.Valid).To(BeTrue())
		Expect(result.FinalScore).To(BeNumerically(">=", 0.8))
	})

	It("returns interview-phase text without validation", func() {
		response := "Tell me more about the goal."
		streamer := &mockStreamer{responses: []string{response}}
		result, err := harness.Evaluate(context.Background(), streamer, "planner", "Start")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.PlanText).To(Equal(response))
		Expect(result.AttemptCount).To(Equal(1))
		Expect(result.ValidationResult).To(BeNil())
	})

	It("retries after invalid output and returns a valid plan", func() {
		streamer := &mockStreamer{responses: []string{invalidPlan(), loadValidPlan()}}
		result, err := harness.Evaluate(context.Background(), streamer, "planner", "Generate a plan")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.AttemptCount).To(Equal(2))
		Expect(result.ValidationResult).NotTo(BeNil())
		Expect(result.ValidationResult.Valid).To(BeTrue())
	})

	It("returns best-effort results after exhausting retries", func() {
		streamer := &mockStreamer{responses: []string{invalidPlan(), invalidPlan(), invalidPlan()}}
		result, err := harness.Evaluate(context.Background(), streamer, "planner", "Generate a plan")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.AttemptCount).To(Equal(3))
		Expect(result.ValidationResult).NotTo(BeNil())
		Expect(result.ValidationResult.Valid).To(BeFalse())
		Expect(result.ValidationResult.Warnings).NotTo(BeEmpty())
	})

	It("returns a context error when cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		streamer := &mockStreamer{responses: []string{loadValidPlan()}}
		result, err := harness.Evaluate(ctx, streamer, "planner", "Generate a plan")
		Expect(err).To(MatchError(context.Canceled))
		Expect(result).To(BeNil())
	})
})

var _ = Describe("ValidatorChain", func() {
	var (
		chain       *plan.ValidatorChain
		projectRoot string
	)

	BeforeEach(func() {
		projectRoot = projectRootFromWorkingDir()
		chain = plan.NewValidatorChain(projectRoot)
	})

	It("short-circuits when plan has no title and no tasks", func() {
		planMissingTitleAndTasks := "---\nid: valid-id\n---\nNo tasks here."
		result, err := chain.Validate(planMissingTitleAndTasks)
		Expect(err).To(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Errors).To(ContainElement(ContainSubstring("missing title")))
		Expect(result.Errors).To(ContainElement(ContainSubstring("no tasks found")))
		Expect(result.Errors).NotTo(ContainElement(ContainSubstring("duplicate")))
		Expect(result.Errors).NotTo(ContainElement(ContainSubstring("dependency")))
		Expect(result.Errors).NotTo(ContainElement(ContainSubstring("effort")))
	})

	It("does not short-circuit when schema passes but assertions fail", func() {
		planPassesSchemaFailsAssertion := "---\nid: dup-plan\ntitle: Duplicate Tasks\ntasks:\n" +
			"  - title: Setup Database\n    estimated_effort: Simple\n" +
			"  - title: Setup Database\n    estimated_effort: Simple\n" +
			"---\n## Setup Database\n\nFirst task.\n\n**Estimated Effort**: Simple\n"
		result, err := chain.Validate(planPassesSchemaFailsAssertion)
		Expect(err).To(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Errors).To(ContainElement(ContainSubstring("duplicate task title")))
		Expect(result.Errors).NotTo(ContainElement(ContainSubstring("missing title")))
		Expect(result.Errors).NotTo(ContainElement(ContainSubstring("no tasks found")))
	})

	It("computes weighted score", func() {
		planText := loadValidPlan()
		result, err := chain.Validate(planText)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.Score).To(BeNumerically(">=", 0.7))
	})
})

func loadValidPlan() string {
	data, err := os.ReadFile("testdata/valid_plan.md")
	Expect(err).NotTo(HaveOccurred())
	return string(data)
}

func invalidPlan() string {
	return "---\nid: invalid-plan\ntitle: Invalid Plan\n---\n"
}

func projectRootFromWorkingDir() string {
	cwd, err := os.Getwd()
	Expect(err).NotTo(HaveOccurred())
	root, err := filepath.Abs(filepath.Join(cwd, "..", ".."))
	Expect(err).NotTo(HaveOccurred())
	return root
}
