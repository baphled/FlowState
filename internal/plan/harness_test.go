package plan_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

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

type chunkMockStreamer struct {
	attempts  [][]provider.StreamChunk
	callCount int
}

func (m *chunkMockStreamer) Stream(_ context.Context, _, _ string) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 10)
	chunks := m.attempts[m.callCount]
	m.callCount++
	go func() {
		defer close(ch)
		for _, c := range chunks {
			ch <- c
		}
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

var _ = Describe("StreamEvaluate", func() {
	var (
		harness     *plan.PlanHarness
		projectRoot string
	)

	BeforeEach(func() {
		projectRoot = projectRootFromWorkingDir()
		harness = plan.NewPlanHarness(projectRoot)
	})

	Context("when streaming a valid plan", func() {
		It("forwards content chunks live and sends a final Done", func() {
			validPlan := loadValidPlan()
			chunks := splitPlanIntoChunks(validPlan, 3)
			streamer := &chunkMockStreamer{attempts: [][]provider.StreamChunk{chunks}}

			outCh, err := harness.StreamEvaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())

			received := drainChunks(outCh)
			contentChunks := filterContentChunks(received)
			Expect(contentChunks).To(HaveLen(3))

			lastChunk := received[len(received)-1]
			Expect(lastChunk.Done).To(BeTrue())
		})
	})

	Context("when the first attempt fails validation", func() {
		It("emits a harness_retry event and retries", func() {
			invalidChunks := []provider.StreamChunk{
				{Content: invalidPlan()},
				{Done: true},
			}
			validChunks := validPlanChunks()
			streamer := &chunkMockStreamer{attempts: [][]provider.StreamChunk{invalidChunks, validChunks}}

			outCh, err := harness.StreamEvaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())

			received := drainChunks(outCh)
			retryChunks := filterRetryChunks(received)
			Expect(retryChunks).To(HaveLen(1))
			Expect(retryChunks[0].EventType).To(Equal("harness_retry"))
			Expect(retryChunks[0].Content).To(ContainSubstring("attempt 1/3"))
			Expect(streamer.callCount).To(Equal(2))
		})
	})

	Context("when the streamer sends an inner Done chunk", func() {
		It("suppresses the inner Done and only sends the outer Done", func() {
			validPlan := loadValidPlan()
			chunks := []provider.StreamChunk{
				{Content: validPlan},
				{Done: true},
			}
			streamer := &chunkMockStreamer{attempts: [][]provider.StreamChunk{chunks}}

			outCh, err := harness.StreamEvaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())

			received := drainChunks(outCh)
			doneChunks := filterDoneChunks(received)
			Expect(doneChunks).To(HaveLen(1))

			contentChunks := filterContentChunks(received)
			Expect(contentChunks).NotTo(BeEmpty())
		})
	})

	Context("when the streamer emits an error mid-stream", func() {
		It("propagates the error to the output channel", func() {
			streamErr := errors.New("provider connection lost")
			chunks := []provider.StreamChunk{
				{Content: "partial content"},
				{Error: streamErr},
			}
			streamer := &chunkMockStreamer{attempts: [][]provider.StreamChunk{chunks}}

			outCh, err := harness.StreamEvaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())

			received := drainChunks(outCh)
			errorChunks := filterErrorChunks(received)
			Expect(errorChunks).To(HaveLen(1))
			Expect(errorChunks[0].Error).To(MatchError("provider connection lost"))
		})
	})

	Context("when the context is cancelled before streaming", func() {
		It("returns a context cancellation error", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			streamer := &chunkMockStreamer{attempts: [][]provider.StreamChunk{validPlanChunks()}}

			outCh, err := harness.StreamEvaluate(ctx, streamer, "planner", "Generate a plan")
			Expect(err).To(MatchError(context.Canceled))
			Expect(outCh).To(BeNil())
		})
	})

	Context("when the context is cancelled mid-stream", func() {
		It("closes the output channel", func() {
			ctx, cancel := context.WithCancel(context.Background())
			blockingCh := make(chan provider.StreamChunk)
			streamer := &blockingMockStreamer{ch: blockingCh}

			outCh, err := harness.StreamEvaluate(ctx, streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())

			cancel()
			Eventually(outCh, 2*time.Second).Should(BeClosed())
		})
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

type blockingMockStreamer struct {
	ch chan provider.StreamChunk
}

func (m *blockingMockStreamer) Stream(_ context.Context, _, _ string) (<-chan provider.StreamChunk, error) {
	return m.ch, nil
}

func splitPlanIntoChunks(planText string, count int) []provider.StreamChunk {
	chunkSize := len(planText) / count
	chunks := make([]provider.StreamChunk, 0, count)
	for i := range count {
		start := i * chunkSize
		end := start + chunkSize
		if i == count-1 {
			end = len(planText)
		}
		chunks = append(chunks, provider.StreamChunk{Content: planText[start:end]})
	}
	return chunks
}

func validPlanChunks() []provider.StreamChunk {
	return splitPlanIntoChunks(loadValidPlan(), 2)
}

func drainChunks(ch <-chan provider.StreamChunk) []provider.StreamChunk {
	var received []provider.StreamChunk
	for chunk := range ch {
		received = append(received, chunk)
	}
	return received
}

func filterContentChunks(chunks []provider.StreamChunk) []provider.StreamChunk {
	var filtered []provider.StreamChunk
	for _, c := range chunks {
		if c.Content != "" && !c.Done && c.Error == nil && c.EventType == "" {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

func filterRetryChunks(chunks []provider.StreamChunk) []provider.StreamChunk {
	var filtered []provider.StreamChunk
	for _, c := range chunks {
		if c.EventType == "harness_retry" {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

func filterDoneChunks(chunks []provider.StreamChunk) []provider.StreamChunk {
	var filtered []provider.StreamChunk
	for _, c := range chunks {
		if c.Done {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

func filterErrorChunks(chunks []provider.StreamChunk) []provider.StreamChunk {
	var filtered []provider.StreamChunk
	for _, c := range chunks {
		if c.Error != nil {
			filtered = append(filtered, c)
		}
	}
	return filtered
}
