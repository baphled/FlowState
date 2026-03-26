package plan_test

import (
	"context"
	"encoding/json"
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

var _ = Describe("Harness", func() {
	var (
		harness     *plan.Harness
		projectRoot string
	)

	BeforeEach(func() {
		projectRoot = projectRootFromWorkingDir()
		harness = plan.NewHarness(projectRoot)
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
		harness     *plan.Harness
		projectRoot string
	)

	BeforeEach(func() {
		projectRoot = projectRootFromWorkingDir()
		harness = plan.NewHarness(projectRoot)
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

	Context("when the context is cancelled mid-send", func() {
		It("closes the output channel promptly when the consumer stops reading", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			streamer := &blockingMockStreamer{ch: make(chan provider.StreamChunk)}

			outCh, err := harness.StreamEvaluate(ctx, streamer, "planner", "msg")
			Expect(err).NotTo(HaveOccurred())

			cancel()
			Eventually(outCh, 2*time.Second).Should(BeClosed())
		})
	})

	Context("when streaming a valid plan on the first attempt", func() {
		It("emits harness_attempt_start on first attempt", func() {
			validPlan := loadValidPlan()
			chunks := []provider.StreamChunk{
				{Content: validPlan},
				{Done: true},
			}
			streamer := &chunkMockStreamer{attempts: [][]provider.StreamChunk{chunks}}

			outCh, err := harness.StreamEvaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())

			received := drainChunks(outCh)
			attemptStartChunks := filterEventChunks(received, "harness_attempt_start")
			Expect(attemptStartChunks).To(HaveLen(1))
			Expect(attemptStartChunks[0].Content).To(ContainSubstring(`"attempt":1`))
		})
	})

	Context("when the plan passes validation", func() {
		It("emits harness_complete before Done", func() {
			validPlan := loadValidPlan()
			chunks := []provider.StreamChunk{
				{Content: validPlan},
				{Done: true},
			}
			streamer := &chunkMockStreamer{attempts: [][]provider.StreamChunk{chunks}}

			outCh, err := harness.StreamEvaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())

			received := drainChunks(outCh)
			completeChunks := filterEventChunks(received, "harness_complete")
			Expect(completeChunks).To(HaveLen(1))

			var payload map[string]any
			Expect(json.Unmarshal([]byte(completeChunks[0].Content), &payload)).To(Succeed())
			Expect(payload).To(HaveKey("valid"))
			Expect(payload).To(HaveKey("score"))
			Expect(payload).To(HaveKey("attemptCount"))

			doneIdx := -1
			completeIdx := -1
			for i, c := range received {
				if c.EventType == "harness_complete" {
					completeIdx = i
				}
				if c.Done {
					doneIdx = i
				}
			}
			Expect(completeIdx).To(BeNumerically("<", doneIdx))
		})
	})

	Context("when critic is wired and the plan passes validation", func() {
		It("emits harness_critic_feedback when critic runs", func() {
			projectRoot := projectRootFromWorkingDir()
			criticProv := &mockChatProvider{response: validCriticResponse()}
			critic := newTestCritic(true)
			harnessWithCritic := plan.NewHarness(projectRoot, plan.WithCritic(critic, criticProv))

			validPlan := loadValidPlan()
			chunks := []provider.StreamChunk{
				{Content: validPlan},
				{Done: true},
			}
			streamer := &chunkMockStreamer{attempts: [][]provider.StreamChunk{chunks}}

			outCh, err := harnessWithCritic.StreamEvaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())

			received := drainChunks(outCh)
			criticChunks := filterEventChunks(received, "harness_critic_feedback")
			Expect(criticChunks).To(HaveLen(1))
			Expect(criticChunks[0].Content).To(ContainSubstring(`"verdict"`))
		})

		It("emits review_verdict event when critic returns verdict", func() {
			projectRoot := projectRootFromWorkingDir()
			criticProv := &mockChatProvider{response: validCriticResponse()}
			critic := newTestCritic(true)
			harnessWithCritic := plan.NewHarness(projectRoot, plan.WithCritic(critic, criticProv))

			validPlan := loadValidPlan()
			chunks := []provider.StreamChunk{
				{Content: validPlan},
				{Done: true},
			}
			streamer := &chunkMockStreamer{attempts: [][]provider.StreamChunk{chunks}}

			outCh, err := harnessWithCritic.StreamEvaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())

			received := drainChunks(outCh)
			verdictChunks := filterEventChunks(received, "review_verdict")
			Expect(verdictChunks).To(HaveLen(1))
			Expect(verdictChunks[0].Content).To(ContainSubstring(`"verdict"`))
		})
	})

	Context("when the plan passes validation", func() {
		It("emits plan_artifact event with plan content", func() {
			validPlan := loadValidPlan()
			chunks := []provider.StreamChunk{
				{Content: validPlan},
				{Done: true},
			}
			streamer := &chunkMockStreamer{attempts: [][]provider.StreamChunk{chunks}}

			outCh, err := harness.StreamEvaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())

			received := drainChunks(outCh)
			artifactChunks := filterEventChunks(received, "plan_artifact")
			Expect(artifactChunks).To(HaveLen(1))
			Expect(artifactChunks[0].Content).NotTo(BeEmpty())
		})
	})
})

var _ = Describe("StreamEvaluate event matrix", func() {
	var projectRoot string

	BeforeEach(func() {
		projectRoot = projectRootFromWorkingDir()
	})

	type eventExpectation struct {
		attemptStarts    int
		completePresent  bool
		expectedValid    bool
		expectedAttempts []int
	}

	DescribeTable("harness events across retry scenarios",
		func(buildStreamer func() *chunkMockStreamer, expected eventExpectation) {
			harness := plan.NewHarness(projectRoot)
			streamer := buildStreamer()

			outCh, err := harness.StreamEvaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())

			received := drainChunks(outCh)
			attemptStartChunks := filterEventChunks(received, "harness_attempt_start")
			Expect(attemptStartChunks).To(HaveLen(expected.attemptStarts))

			for i, chunk := range attemptStartChunks {
				var payload map[string]any
				Expect(json.Unmarshal([]byte(chunk.Content), &payload)).To(Succeed())
				Expect(payload["attempt"]).To(BeEquivalentTo(expected.expectedAttempts[i]))
			}

			completeChunks := filterEventChunks(received, "harness_complete")
			if expected.completePresent {
				Expect(completeChunks).To(HaveLen(1))
				var payload map[string]any
				Expect(json.Unmarshal([]byte(completeChunks[0].Content), &payload)).To(Succeed())
				Expect(payload).To(HaveKey("valid"))
				Expect(payload).To(HaveKey("score"))
				Expect(payload).To(HaveKey("attemptCount"))
				Expect(payload["valid"]).To(Equal(expected.expectedValid))
			} else {
				Expect(completeChunks).To(BeEmpty())
			}
		},
		Entry("first attempt succeeds",
			func() *chunkMockStreamer {
				return &chunkMockStreamer{attempts: [][]provider.StreamChunk{
					{{Content: loadValidPlan()}, {Done: true}},
				}}
			},
			eventExpectation{
				attemptStarts:    1,
				completePresent:  true,
				expectedValid:    true,
				expectedAttempts: []int{1},
			},
		),
		Entry("retry then success",
			func() *chunkMockStreamer {
				return &chunkMockStreamer{attempts: [][]provider.StreamChunk{
					{{Content: invalidPlan()}, {Done: true}},
					{{Content: loadValidPlan()}, {Done: true}},
				}}
			},
			eventExpectation{
				attemptStarts:    2,
				completePresent:  true,
				expectedValid:    true,
				expectedAttempts: []int{1, 2},
			},
		),
		Entry("all retries exhausted",
			func() *chunkMockStreamer {
				return &chunkMockStreamer{attempts: [][]provider.StreamChunk{
					{{Content: invalidPlan()}, {Done: true}},
					{{Content: invalidPlan()}, {Done: true}},
					{{Content: invalidPlan()}, {Done: true}},
				}}
			},
			eventExpectation{
				attemptStarts:    3,
				completePresent:  true,
				expectedValid:    false,
				expectedAttempts: []int{1, 2, 3},
			},
		),
		Entry("interview phase (no YAML frontmatter)",
			func() *chunkMockStreamer {
				return &chunkMockStreamer{attempts: [][]provider.StreamChunk{
					{{Content: "Tell me more about the goal."}, {Done: true}},
				}}
			},
			eventExpectation{
				attemptStarts:    1,
				completePresent:  false,
				expectedValid:    false,
				expectedAttempts: []int{1},
			},
		),
	)
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

type incrementalPhase struct {
	phase   string
	content string
}

type incrementalMockStreamer struct {
	phaseOutputs     []incrementalPhase
	fullPlan         string
	fallbackResponse string
	fallbackChunks   []provider.StreamChunk
	callCount        int
	fallbackCalled   bool
}

func newIncrementalMockStreamer() *incrementalMockStreamer {
	return &incrementalMockStreamer{
		phaseOutputs:   []incrementalPhase{},
		fallbackCalled: false,
	}
}

func (m *incrementalMockStreamer) Stream(_ context.Context, _ string, message string) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)

	// Check if this is a fallback call (contains "Generate ONLY" but not from incremental)
	if m.fallbackCalled || len(m.fallbackChunks) > 0 {
		m.fallbackCalled = true
		go func() {
			defer close(ch)
			for _, c := range m.fallbackChunks {
				ch <- c
			}
		}()
		return ch, nil
	}

	// Incremental mode - return phase outputs
	m.callCount++

	phaseIndex := m.callCount - 1
	if phaseIndex >= len(m.phaseOutputs) {
		// Fallback to full plan if all phases returned
		go func() {
			defer close(ch)
			ch <- provider.StreamChunk{Content: m.fullPlan}
		}()
		return ch, nil
	}

	phase := m.phaseOutputs[phaseIndex]
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: phase.content}
	}()

	return ch, nil
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

func filterEventChunks(chunks []provider.StreamChunk, eventType string) []provider.StreamChunk {
	var filtered []provider.StreamChunk
	for _, c := range chunks {
		if c.EventType == eventType {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

var _ = Describe("Harness with ConsistencyVoter", func() {
	var (
		harness     *plan.Harness
		projectRoot string
	)

	BeforeEach(func() {
		projectRoot = projectRootFromWorkingDir()
	})

	Context("when voter is not configured (nil)", func() {
		It("behaves identically to current behavior in Evaluate", func() {
			harness = plan.NewHarness(projectRoot)
			streamer := &mockStreamer{responses: []string{loadValidPlan()}}
			result, err := harness.Evaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.ValidationResult.Valid).To(BeTrue())
		})

		It("behaves identically to current behavior in StreamEvaluate", func() {
			harness = plan.NewHarness(projectRoot)
			validPlan := loadValidPlan()
			chunks := []provider.StreamChunk{
				{Content: validPlan},
				{Done: true},
			}
			streamer := &chunkMockStreamer{attempts: [][]provider.StreamChunk{chunks}}
			outCh, err := harness.StreamEvaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())
			received := drainChunks(outCh)
			completeChunks := filterEventChunks(received, "harness_complete")
			Expect(completeChunks).To(HaveLen(1))
		})
	})

	Context("when voter is configured but score is above threshold", func() {
		It("does not trigger voting in Evaluate", func() {
			voterCfg := plan.VoterConfig{Enabled: true, Variants: 3, Threshold: 0.5}
			voter := plan.NewConsistencyVoter(voterCfg, projectRoot)
			harness = plan.NewHarness(projectRoot, plan.WithVoter(voter))
			streamer := &mockStreamer{responses: []string{loadValidPlan()}}
			result, err := harness.Evaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.FinalScore).To(BeNumerically(">=", 0.5))
		})

		It("does not trigger voting in StreamEvaluate", func() {
			voterCfg := plan.VoterConfig{Enabled: true, Variants: 3, Threshold: 0.5}
			voter := plan.NewConsistencyVoter(voterCfg, projectRoot)
			harness = plan.NewHarness(projectRoot, plan.WithVoter(voter))
			validPlan := loadValidPlan()
			chunks := []provider.StreamChunk{
				{Content: validPlan},
				{Done: true},
			}
			streamer := &chunkMockStreamer{attempts: [][]provider.StreamChunk{chunks}}
			outCh, err := harness.StreamEvaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())
			received := drainChunks(outCh)
			completeChunks := filterEventChunks(received, "harness_complete")
			Expect(completeChunks).To(HaveLen(1))
		})
	})

	Context("when voter is configured and enabled", func() {
		It("still produces valid result when voter is enabled but score above threshold", func() {
			voterCfg := plan.VoterConfig{Enabled: true, Variants: 2, Threshold: 0.95}
			voter := plan.NewConsistencyVoter(voterCfg, projectRoot)
			harness = plan.NewHarness(projectRoot, plan.WithVoter(voter))
			streamer := &mockStreamer{responses: []string{loadValidPlan()}}
			result, err := harness.Evaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.ValidationResult.Valid).To(BeTrue())
			Expect(result.FinalScore).To(BeNumerically(">", 0.95))
		})
	})
})

var _ = Describe("PlanHarness with IncrementalGenerator", func() {
	var (
		harness     *plan.PlanHarness
		projectRoot string
	)

	BeforeEach(func() {
		projectRoot = projectRootFromWorkingDir()
	})

	Context("when incremental is not configured (nil)", func() {
		It("uses normal streaming in Evaluate", func() {
			harness = plan.NewPlanHarness(projectRoot)
			streamer := &mockStreamer{responses: []string{loadValidPlan()}}
			result, err := harness.Evaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.ValidationResult.Valid).To(BeTrue())
		})

		It("uses normal streaming in StreamEvaluate", func() {
			harness = plan.NewPlanHarness(projectRoot)
			validPlan := loadValidPlan()
			chunks := []provider.StreamChunk{
				{Content: validPlan},
				{Done: true},
			}
			streamer := &chunkMockStreamer{attempts: [][]provider.StreamChunk{chunks}}
			outCh, err := harness.StreamEvaluate(context.Background(), streamer, "planner", "Generate a plan")
			Expect(err).NotTo(HaveOccurred())
			received := drainChunks(outCh)
			completeChunks := filterEventChunks(received, "harness_complete")
			Expect(completeChunks).To(HaveLen(1))
		})
	})

	Context("when incremental is configured", func() {
		var (
			incrementalStreamer *incrementalMockStreamer
			incrementalGen      *plan.IncrementalGenerator
		)

		BeforeEach(func() {
			incrementalStreamer = newIncrementalMockStreamer()
			incrementalGen = &plan.IncrementalGenerator{Streamer: incrementalStreamer, MaxRetries: 3}
		})

		Describe("Evaluate", func() {
			It("uses incremental generator on first attempt", func() {
				harness = plan.NewPlanHarness(projectRoot, plan.WithIncremental(incrementalGen))
				incrementalStreamer.phaseOutputs = []incrementalPhase{
					{phase: "Frontmatter", content: "---\nid: test\ntitle: Test\n---\n"},
					{phase: "Rationale", content: "Rationale: Test rationale"},
					{phase: "Tasks", content: "## Tasks\n- Task 1"},
					{phase: "Waves", content: "## Waves\n- Wave 1"},
					{phase: "SuccessCriteria", content: "## Success Criteria\n- Done"},
					{phase: "Risks", content: "## Risks\n- Risk 1"},
				}
				incrementalStreamer.fullPlan = "---\nid: test\ntitle: Test\n---\n\nRationale: Test rationale\n\n## Tasks\n- Task 1\n\n## Waves\n- Wave 1\n\n## Success Criteria\n- Done\n\n## Risks\n- Risk 1"

				result, err := harness.Evaluate(context.Background(), incrementalStreamer, "planner", "Generate a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				// First attempt uses incremental - Generate() calls Stream() for each phase (6 phases)
				Expect(incrementalStreamer.callCount).To(BeNumerically(">=", 6))
			})

			It("falls back to normal streaming on retry when validation fails", func() {
				harness = plan.NewPlanHarness(projectRoot, plan.WithIncremental(incrementalGen))
				// First attempt (incremental) returns invalid plan (missing tasks)
				incrementalStreamer.phaseOutputs = []incrementalPhase{
					{phase: "Frontmatter", content: "---\nid: test\ntitle: Test\n---\n"},
					{phase: "Rationale", content: "Rationale: Test"},
					{phase: "Tasks", content: "## Tasks\n- Task 1"},
					{phase: "Waves", content: "## Waves\n- Wave 1"},
					{phase: "SuccessCriteria", content: "## Success Criteria\n- Done"},
					{phase: "Risks", content: "## Risks\n- Risk 1"},
				}
				incrementalStreamer.fullPlan = "---\nid: test\ntitle: Test\n---\n\nRationale: Test\n\n## Tasks\n- Task 1\n\n## Waves\n- Wave 1\n\n## Success Criteria\n- Done\n\n## Risks\n- Risk 1"

				// Second attempt (fallback to normal) returns valid plan
				incrementalStreamer.fallbackResponse = loadValidPlan()

				result, err := harness.Evaluate(context.Background(), incrementalStreamer, "planner", "Generate a plan")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				// First attempt uses incremental (6 Stream calls), second uses fallback (1 Stream call)
				Expect(incrementalStreamer.callCount).To(BeNumerically(">=", 7))
			})
		})

		Describe("StreamEvaluate", func() {
			It("streams phases and emits harness_phase_complete events", func() {
				harness = plan.NewPlanHarness(projectRoot, plan.WithIncremental(incrementalGen))
				incrementalStreamer.phaseOutputs = []incrementalPhase{
					{phase: "Frontmatter", content: "---\nid: test\ntitle: Test\n---\n"},
					{phase: "Rationale", content: "Rationale: Test"},
					{phase: "Tasks", content: "## Tasks\n- Task 1"},
					{phase: "Waves", content: "## Waves\n- Wave 1"},
					{phase: "SuccessCriteria", content: "## Success Criteria\n- Done"},
					{phase: "Risks", content: "## Risks\n- Risk 1"},
				}
				incrementalStreamer.fullPlan = "---\nid: test\ntitle: Test\n---\n\nRationale: Test\n\n## Tasks\n- Task 1\n\n## Waves\n- Wave 1\n\n## Success Criteria\n- Done\n\n## Risks\n- Risk 1"

				outCh, err := harness.StreamEvaluate(context.Background(), incrementalStreamer, "planner", "Generate a plan")
				Expect(err).NotTo(HaveOccurred())

				received := drainChunks(outCh)
				phaseCompleteChunks := filterEventChunks(received, "harness_phase_complete")
				// Should have 6 phase complete events (Frontmatter, Rationale, Tasks, Waves, SuccessCriteria, Risks)
				Expect(phaseCompleteChunks).To(HaveLen(6))
			})

			It("falls back to normal streaming on retry", func() {
				harness = plan.NewPlanHarness(projectRoot, plan.WithIncremental(incrementalGen))
				// First attempt (incremental) - we'll use a simple invalid response
				incrementalStreamer.phaseOutputs = []incrementalPhase{
					{phase: "Frontmatter", content: "---\nid: test\ntitle: Test\n---\n"},
					{phase: "Rationale", content: "Rationale: Test"},
					{phase: "Tasks", content: "## Tasks\n- Task 1"},
					{phase: "Waves", content: "## Waves\n- Wave 1"},
					{phase: "SuccessCriteria", content: "## Success Criteria\n- Done"},
					{phase: "Risks", content: "## Risks\n- Risk 1"},
				}
				incrementalStreamer.fullPlan = "---\nid: test\ntitle: Test\n---\n"

				// Second attempt (fallback to normal streaming)
				incrementalStreamer.fallbackChunks = []provider.StreamChunk{
					{Content: loadValidPlan()},
					{Done: true},
				}

				outCh, err := harness.StreamEvaluate(context.Background(), incrementalStreamer, "planner", "Generate a plan")
				Expect(err).NotTo(HaveOccurred())

				received := drainChunks(outCh)
				completeChunks := filterEventChunks(received, "harness_complete")
				Expect(completeChunks).To(HaveLen(1))
			})
		})
	})
})
