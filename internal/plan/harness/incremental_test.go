package harness_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan/harness"
	"github.com/baphled/flowstate/internal/provider"
)

type incrementalStreamer struct {
	receivedMessages []string
	phaseOutputs     map[string]string
	emptyOnFirstCall map[string]bool
	callCounts       map[string]int
}

func newIncrementalStreamer() *incrementalStreamer {
	return &incrementalStreamer{
		phaseOutputs:     make(map[string]string),
		emptyOnFirstCall: make(map[string]bool),
		callCounts:       make(map[string]int),
	}
}

func (s *incrementalStreamer) Stream(_ context.Context, _ string, message string) (<-chan provider.StreamChunk, error) {
	s.receivedMessages = append(s.receivedMessages, message)
	ch := make(chan provider.StreamChunk, 1)

	var detectedPhase string
	for _, phase := range harness.AllPhases {
		phaseStr := string(phase)
		// Special case for frontmatter: check for "frontmatter" in message
		// since the prompt uses "YAML frontmatter section" instead of just "Frontmatter"
		if phase == harness.PhaseFrontmatter {
			if strings.Contains(strings.ToLower(message), "frontmatter") {
				detectedPhase = phaseStr
				break
			}
		} else if strings.Contains(message, phaseStr) {
			detectedPhase = phaseStr
			break
		}
	}

	s.callCounts[detectedPhase]++

	var content string
	if s.emptyOnFirstCall[detectedPhase] && s.callCounts[detectedPhase] == 1 {
		content = ""
	} else {
		content = s.phaseOutputs[detectedPhase]
	}

	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: content}
	}()

	return ch, nil
}

var _ = Describe("IncrementalGenerator", func() {
	var (
		gen      *harness.IncrementalGenerator
		streamer *incrementalStreamer
		ctx      context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		streamer = newIncrementalStreamer()
		streamer.phaseOutputs["Frontmatter"] = "---\nid: test-plan\ntitle: Test Plan\n---\n"
		streamer.phaseOutputs["Rationale"] = "Rationale output"
		streamer.phaseOutputs["Tasks"] = "Tasks output"
		streamer.phaseOutputs["Waves"] = "Waves output"
		streamer.phaseOutputs["SuccessCriteria"] = "SuccessCriteria output"
		streamer.phaseOutputs["Risks"] = "Risks output"
		gen = &harness.IncrementalGenerator{Streamer: streamer, MaxRetries: 3}
	})

	It("generates all phases in order", func() {
		result, err := gen.Generate(ctx, "agent-1", "base prompt")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(streamer.receivedMessages).To(HaveLen(6))
		for i, phase := range harness.AllPhases {
			var expected string
			if phase == harness.PhaseFrontmatter {
				expected = "base prompt\n\nGenerate ONLY the YAML frontmatter section of the plan (---\\nid: ...\\ntitle: ...\\n---)"
			} else {
				expected = "base prompt\n\nGenerate ONLY the " + string(phase) + " section of the plan."
			}
			Expect(streamer.receivedMessages[i]).To(Equal(expected))
		}
	})

	It("validates non-empty output per phase", func() {
		streamer.phaseOutputs["Tasks"] = ""
		_, err := gen.Generate(ctx, "agent-1", "base prompt")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Tasks"))
		Expect(err.Error()).To(ContainSubstring("empty output"))
	})

	It("retries on empty phase output", func() {
		streamer.emptyOnFirstCall["Rationale"] = true
		result, err := gen.Generate(ctx, "agent-1", "base prompt")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(streamer.callCounts["Rationale"]).To(Equal(2))
	})

	It("aggregates final plan correctly", func() {
		result, err := gen.Generate(ctx, "agent-1", "base prompt")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.PhaseResults).To(HaveLen(6))
		Expect(result.PhaseResults[0].Phase).To(Equal(harness.PhaseFrontmatter))
		Expect(result.PhaseResults[0].Output).To(ContainSubstring("---\nid: test-plan\ntitle: Test Plan\n---"))
		Expect(result.PhaseResults[5].Phase).To(Equal(harness.PhaseRisks))
		Expect(result.PhaseResults[5].Output).To(Equal("Risks output"))
		Expect(result.FullPlan).To(ContainSubstring("Rationale output"))
		Expect(result.FullPlan).To(ContainSubstring("Tasks output"))
		Expect(result.FullPlan).To(ContainSubstring("Risks output"))
	})

	It("handles context cancellation", func() {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := gen.Generate(cancelled, "agent-1", "base prompt")
		Expect(err).To(HaveOccurred())
		Expect(err).To(Equal(context.Canceled))
	})
})
