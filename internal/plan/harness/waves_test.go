package harness_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan/harness"
)

// fakeWaveValidator is a deterministic stub for harness.WaveValidator.
// Records every MissingForChain call so specs can assert which waves
// the harness consulted and in what order. The next-call result is
// driven by the configured `responses` slice in FIFO order.
type fakeWaveValidator struct {
	responses []fakeWaveResponse
	calls     []fakeWaveCall
}

type fakeWaveResponse struct {
	missing []string
	err     error
}

type fakeWaveCall struct {
	agentID string
	wave    string
}

func (f *fakeWaveValidator) MissingForChain(_ context.Context, agentID string, wave harness.WaveStage) ([]string, error) {
	f.calls = append(f.calls, fakeWaveCall{agentID: agentID, wave: wave.Name})
	if len(f.responses) == 0 {
		return nil, nil
	}
	r := f.responses[0]
	f.responses = f.responses[1:]
	return r.missing, r.err
}

// Wave fan-in is the harness-level enforcement of multi-stage
// orchestrator loops: when an agent declares waves, the harness
// re-prompts it on yield-attempts that leave any wave's expected
// coordination_store keys missing. Closes the "planner stops three
// stages early" symptom. These specs lock the validator-call
// contract — actual harness integration (re-prompt feedback inside
// runStreamEvaluation) is exercised by the wider harness specs.
var _ = Describe("WithWaves + checkWavesIncomplete", func() {
	stages := []harness.WaveStage{
		{Name: "evidence", ExpectedKeys: []string{"{chainID}/codebase-findings", "{chainID}/external-refs"}},
		{Name: "analysis", ExpectedKeys: []string{"{chainID}/analysis"}},
	}

	It("returns the empty string when no validator is wired", func() {
		h := harness.NewHarness("/tmp")
		Expect(harness.CheckWavesIncompleteForTest(h, context.Background(), "planner")).To(Equal(""),
			"no waves configured = legacy behaviour = no re-prompt")
	})

	It("returns the empty string when every configured wave is complete", func() {
		v := &fakeWaveValidator{
			responses: []fakeWaveResponse{
				{missing: nil}, // evidence complete
				{missing: nil}, // analysis complete
			},
		}
		h := harness.NewHarness("/tmp", harness.WithWaves(stages, v))

		Expect(harness.CheckWavesIncompleteForTest(h, context.Background(), "planner")).To(Equal(""))
		Expect(v.calls).To(HaveLen(2),
			"both waves must be checked when both come back complete")
		Expect(v.calls[0].wave).To(Equal("evidence"))
		Expect(v.calls[1].wave).To(Equal("analysis"))
	})

	It("returns directive feedback naming the first incomplete wave", func() {
		v := &fakeWaveValidator{
			responses: []fakeWaveResponse{
				{missing: []string{"chain-1/codebase-findings"}}, // evidence stuck
			},
		}
		h := harness.NewHarness("/tmp", harness.WithWaves(stages, v))

		fb := harness.CheckWavesIncompleteForTest(h, context.Background(), "planner")
		Expect(fb).NotTo(BeEmpty())
		Expect(fb).To(ContainSubstring("evidence"),
			"the feedback must name the stuck wave so the planner knows what to fix")
		Expect(fb).To(ContainSubstring("chain-1/codebase-findings"),
			"the missing key must be in the feedback so the planner knows what to write")
		Expect(v.calls).To(HaveLen(1),
			"checking stops at the first incomplete wave — no point reporting downstream waves")
	})

	It("treats validator errors as wave-incomplete (defensive against transient store hiccups)", func() {
		v := &fakeWaveValidator{
			responses: []fakeWaveResponse{
				{err: errors.New("coord store unavailable")},
			},
		}
		h := harness.NewHarness("/tmp", harness.WithWaves(stages, v))

		fb := harness.CheckWavesIncompleteForTest(h, context.Background(), "planner")
		Expect(fb).NotTo(BeEmpty(),
			"an error MUST stop the planner from yielding past the gate; treat as incomplete")
		Expect(fb).To(ContainSubstring("coord store unavailable"),
			"the error message must surface so the planner has context for what's wrong")
	})

	It("forwards the agentID unchanged to the validator", func() {
		v := &fakeWaveValidator{
			responses: []fakeWaveResponse{{missing: nil}},
		}
		h := harness.NewHarness("/tmp", harness.WithWaves(stages[:1], v))

		_ = harness.CheckWavesIncompleteForTest(h, context.Background(), "specific-agent-id")
		Expect(v.calls[0].agentID).To(Equal("specific-agent-id"))
	})

	It("treats nil validator as no waves configured", func() {
		h := harness.NewHarness("/tmp", harness.WithWaves(stages, nil))
		Expect(harness.CheckWavesIncompleteForTest(h, context.Background(), "planner")).To(Equal(""),
			"WithWaves(stages, nil) is a deliberate no-op — preserves legacy behaviour")
	})
})

// Synthesis-hang directive feedback: when a write-critical member ends
// its turn narrating the write ("Let me now write the plan…") while
// emitting ZERO tool calls, the wave gate detects the missing key but a
// passive "stage incomplete" nudge does not convert the narration into
// an actual tool call. The directive variant of buildWaveFeedback names
// the missing keys AND instructs the agent to PERFORM the write now
// rather than narrate it. Forensic basis: KB-curator c09b1bc9 idx[-1]
// "…Let me now write the plan to the vault…" toolName=NONE (0 write
// calls, session marked failed).
var _ = Describe("buildWaveFeedback directive variant (narrated-but-no-tool-call)", func() {
	stage := harness.WaveStage{
		Name:         "writing",
		ExpectedKeys: []string{"{chainID}/plan"},
		Description:  "Plan writer produces the structured OMO plan from analysis.",
	}

	It("appends an actionable directive when the last turn had no tool call", func() {
		fb := harness.BuildWaveFeedbackForTest(stage, []string{"chain-1/plan"}, nil, true)
		Expect(fb).To(ContainSubstring("chain-1/plan"),
			"the missing key must still be named")
		Expect(fb).To(ContainSubstring("no tool call"),
			"the directive must call out that the previous turn emitted no tool call")
		Expect(fb).To(MatchRegexp(`(?i)do not narrate`),
			"the directive must tell the agent to perform the write, not describe it")
		Expect(fb).To(MatchRegexp(`(?i)coordination_store`),
			"the directive must name the concrete tool to call")
	})

	It("omits the directive when the last turn DID emit a tool call", func() {
		fb := harness.BuildWaveFeedbackForTest(stage, []string{"chain-1/plan"}, nil, false)
		Expect(fb).To(ContainSubstring("chain-1/plan"),
			"the missing key is still named on the passive nudge")
		Expect(fb).NotTo(MatchRegexp(`(?i)do not narrate`),
			"a turn that already called a tool should not be accused of narrating")
		Expect(fb).NotTo(ContainSubstring("no tool call"))
	})
})
