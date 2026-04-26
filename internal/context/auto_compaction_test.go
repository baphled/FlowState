package context_test

import (
	"context"
	"encoding/json"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// fakeSummariser is a test double implementing contextpkg.Summariser. It
// records the inputs it receives and returns a scripted response (or
// error) to the caller. Deliberately bare-bones — no sync primitives —
// because AutoCompactor is exercised serially from each spec.
type fakeSummariser struct {
	// recordedSystem and recordedUser capture the last call's prompts so
	// assertions can verify that AutoCompactor is threading the T8
	// SummaryPromptSystem and rendered user prompt through verbatim.
	recordedSystem string
	recordedUser   string
	// recordedMessages captures the msgs slice AutoCompactor was asked
	// to summarise, so specs can assert the slice reached the
	// summariser unchanged.
	recordedMessages []provider.Message

	// response is the text returned from Summarise when err is nil.
	response string
	// err is returned directly by Summarise when non-nil.
	err error

	// calls counts invocations; used to assert "no retries" by checking
	// that exactly one call happened even after the single attempt
	// errors.
	calls int
}

func (f *fakeSummariser) Summarise(_ context.Context, systemPrompt string, userPrompt string, msgs []provider.Message) (string, error) {
	f.calls++
	f.recordedSystem = systemPrompt
	f.recordedUser = userPrompt
	f.recordedMessages = msgs
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

// sampleSummaryJSON builds a JSON body the fake summariser can return.
// Failure to marshal aborts the spec via Gomega.
func sampleSummaryJSON(override func(*contextpkg.CompactionSummary)) string {
	summary := contextpkg.CompactionSummary{
		Intent:             "summarise the current compaction slice",
		KeyDecisions:       []string{"route via summariser", "never retry"},
		Errors:             []string{},
		NextSteps:          []string{"persist result"},
		FilesToRestore:     []string{"internal/context/auto_compaction.go"},
		OriginalTokenCount: 4200,
		SummaryTokenCount:  640,
	}
	if override != nil {
		override(&summary)
	}
	data, err := json.Marshal(summary)
	Expect(err).NotTo(HaveOccurred(), "build sample summary JSON")
	return string(data)
}

// sampleMessages is a small, stable slice of provider.Messages used as
// input to AutoCompactor.Compact across specs. Content is deliberately
// terse — we are exercising orchestration, not prompt fidelity.
func sampleMessages() []provider.Message {
	return []provider.Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "follow-up"},
	}
}

// Layer 2 AutoCompactor specification.
//
// These specs pin the T9b contract: AutoCompactor renders the T8 prompt,
// calls an injected Summariser, parses the JSON response into a
// CompactionSummary, and validates that Intent and NextSteps are
// non-empty. It never retries. It never imports provider or engine
// directly — the Summariser is a narrow local interface so the consumer
// package remains cycle-free.
var _ = Describe("AutoCompactor.Compact", func() {
	It("happy path: returns a populated CompactionSummary on a single summariser call", func() {
		summariser := &fakeSummariser{response: sampleSummaryJSON(nil)}
		compactor := contextpkg.NewAutoCompactor(summariser)

		summary, err := compactor.Compact(context.Background(), sampleMessages())
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.Intent).NotTo(BeEmpty())
		Expect(summary.NextSteps).NotTo(BeEmpty())
		Expect(summariser.calls).To(Equal(1))
	})

	It("threads SummaryPromptSystem and RenderSummaryPrompt output to the summariser verbatim", func() {
		summariser := &fakeSummariser{response: sampleSummaryJSON(nil)}
		compactor := contextpkg.NewAutoCompactor(summariser)

		msgs := sampleMessages()
		_, err := compactor.Compact(context.Background(), msgs)
		Expect(err).NotTo(HaveOccurred())

		Expect(summariser.recordedSystem).To(Equal(contextpkg.SummaryPromptSystem),
			"system drift detected")

		wantUser, err := contextpkg.RenderSummaryPrompt(msgs)
		Expect(err).NotTo(HaveOccurred())
		Expect(summariser.recordedUser).To(Equal(wantUser),
			"user prompt drift")

		Expect(summariser.recordedMessages).To(HaveLen(len(msgs)))
	})

	It("returns ErrEmptySummaryInput on empty input without calling the summariser", func() {
		summariser := &fakeSummariser{}
		compactor := contextpkg.NewAutoCompactor(summariser)

		_, err := compactor.Compact(context.Background(), nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, contextpkg.ErrEmptySummaryInput)).To(BeTrue(),
			"err = %v; want ErrEmptySummaryInput", err)
		Expect(summariser.calls).To(Equal(0))
	})

	It("propagates a wrapped summariser error and does not retry", func() {
		upstream := errors.New("summariser: simulated provider outage")
		summariser := &fakeSummariser{err: upstream}
		compactor := contextpkg.NewAutoCompactor(summariser)

		_, err := compactor.Compact(context.Background(), sampleMessages())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, upstream)).To(BeTrue())
		Expect(summariser.calls).To(Equal(1),
			"summariser must be called exactly once (no retries)")
	})

	It("returns a parse error on malformed JSON (not ErrEmptySummaryInput)", func() {
		summariser := &fakeSummariser{response: "not { valid json"}
		compactor := contextpkg.NewAutoCompactor(summariser)

		_, err := compactor.Compact(context.Background(), sampleMessages())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, contextpkg.ErrEmptySummaryInput)).To(BeFalse())
		Expect(err.Error()).To(Or(ContainSubstring("parse"), ContainSubstring("unmarshal")))
	})

	It("returns ErrInvalidSummary when Intent is empty (validation error mentions 'intent')", func() {
		summariser := &fakeSummariser{
			response: sampleSummaryJSON(func(s *contextpkg.CompactionSummary) {
				s.Intent = ""
			}),
		}
		compactor := contextpkg.NewAutoCompactor(summariser)

		_, err := compactor.Compact(context.Background(), sampleMessages())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, contextpkg.ErrInvalidSummary)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("intent"))
	})

	It("returns ErrInvalidSummary when NextSteps is empty (validation error mentions 'next_steps')", func() {
		summariser := &fakeSummariser{
			response: sampleSummaryJSON(func(s *contextpkg.CompactionSummary) {
				s.NextSteps = nil
			}),
		}
		compactor := contextpkg.NewAutoCompactor(summariser)

		_, err := compactor.Compact(context.Background(), sampleMessages())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, contextpkg.ErrInvalidSummary)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("next_steps"))
	})

	DescribeTable("M1 — rejects empty-after-trim NextSteps entries",
		func(steps []string) {
			summariser := &fakeSummariser{
				response: sampleSummaryJSON(func(s *contextpkg.CompactionSummary) {
					s.NextSteps = steps
				}),
			}
			compactor := contextpkg.NewAutoCompactor(summariser)

			_, err := compactor.Compact(context.Background(), sampleMessages())
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, contextpkg.ErrInvalidSummary)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("next_steps"))
		},
		Entry("single empty entry", []string{""}),
		Entry("whitespace only entry", []string{"   "}),
		Entry("tab only entry", []string{"\t"}),
		Entry("valid followed by empty", []string{"first concrete step", ""}),
		Entry("empty followed by valid", []string{"", "real continuation"}),
	)

	It("M1 positive path: surrounding whitespace does not trip the guard if non-empty after trim", func() {
		summariser := &fakeSummariser{
			response: sampleSummaryJSON(func(s *contextpkg.CompactionSummary) {
				s.NextSteps = []string{"  do X  ", "do Y"}
			}),
		}
		compactor := contextpkg.NewAutoCompactor(summariser)

		summary, err := compactor.Compact(context.Background(), sampleMessages())
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.NextSteps).To(HaveLen(2))
	})

	It("returns ErrNilSummariser when constructed with a nil summariser", func() {
		compactor := contextpkg.NewAutoCompactor(nil)

		_, err := compactor.Compact(context.Background(), sampleMessages())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, contextpkg.ErrNilSummariser)).To(BeTrue())
	})

	It("returns a parse error (not ErrInvalidSummary) for fenced response without a newline", func() {
		summariser := &fakeSummariser{response: "```json no-newline-ever"}
		compactor := contextpkg.NewAutoCompactor(summariser)

		_, err := compactor.Compact(context.Background(), sampleMessages())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, contextpkg.ErrInvalidSummary)).To(BeFalse())
	})

	It("parses fenced JSON wrappers despite the T8 prompt forbidding fences", func() {
		raw := sampleSummaryJSON(nil)
		fenced := "```json\n" + raw + "\n```"
		summariser := &fakeSummariser{response: fenced}
		compactor := contextpkg.NewAutoCompactor(summariser)

		summary, err := compactor.Compact(context.Background(), sampleMessages())
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.Intent).NotTo(BeEmpty())
	})

	It("rejects a summary that leaks a raw Anthropic 'toolu_' id (T10c)", func() {
		summariser := &fakeSummariser{response: sampleSummaryJSON(func(s *contextpkg.CompactionSummary) {
			s.Intent = "summary references toolu_abc1234567890xyz which should be scrubbed"
		})}
		compactor := contextpkg.NewAutoCompactor(summariser)

		_, err := compactor.Compact(context.Background(), sampleMessages())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, contextpkg.ErrInvalidSummary)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("tool"))
	})

	It("rejects a summary that leaks a raw OpenAI 'call_' id", func() {
		summariser := &fakeSummariser{response: sampleSummaryJSON(func(s *contextpkg.CompactionSummary) {
			s.NextSteps = []string{"re-run the call_9876543210ABCDEF tool"}
		})}
		compactor := contextpkg.NewAutoCompactor(summariser)

		_, err := compactor.Compact(context.Background(), sampleMessages())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, contextpkg.ErrInvalidSummary)).To(BeTrue())
	})

	DescribeTable("M4 — does not flag legitimate English snake_case phrases beginning with 'call_'",
		func(phrase string) {
			summariser := &fakeSummariser{
				response: sampleSummaryJSON(func(s *contextpkg.CompactionSummary) {
					s.Intent = phrase
				}),
			}
			compactor := contextpkg.NewAutoCompactor(summariser)
			_, err := compactor.Compact(context.Background(), sampleMessages())
			Expect(err).NotTo(HaveOccurred(),
				"legitimate English phrase tripped forbidden-id guard: %q", phrase)
		},
		Entry("personal callback", "call_me_back_for_review_team_leader"),
		Entry("contextual callback", "please call_me_back_for_review_team_leader soon"),
		Entry("technical hyphenated", "the call_handler_impl_function_name is documented"),
		Entry("listener name", "register the call_back_hook_when_the_user_asks listener"),
	)

	DescribeTable("M4 positive guard — still rejects every real-world tool-call id shape",
		func(phrase string) {
			summariser := &fakeSummariser{
				response: sampleSummaryJSON(func(s *contextpkg.CompactionSummary) {
					s.Intent = phrase
				}),
			}
			compactor := contextpkg.NewAutoCompactor(summariser)
			_, err := compactor.Compact(context.Background(), sampleMessages())
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, contextpkg.ErrInvalidSummary)).To(BeTrue())
		},
		Entry("anthropic-native", "the earlier toolu_01ABCDEF0123456789 call failed"),
		Entry("openai-native", "the earlier call_1234567890abcdef run"),
		Entry("translated-openai", "the failover produced call_abcdef1234567890abcdef01"),
		Entry("translated-anth", "the failover produced toolu_abcdef1234567890abcdef01"),
	)

	It("does not reject a summary that merely uses the literal word 'tool'", func() {
		summariser := &fakeSummariser{
			response: sampleSummaryJSON(func(s *contextpkg.CompactionSummary) {
				s.Intent = "the agent completed a tool call sequence successfully"
				s.NextSteps = []string{"continue with the next tool invocation"}
			}),
		}
		compactor := contextpkg.NewAutoCompactor(summariser)

		_, err := compactor.Compact(context.Background(), sampleMessages())
		Expect(err).NotTo(HaveOccurred())
	})

	It("inspects every string-bearing field, not just Intent (rejects forbidden id in Errors)", func() {
		summariser := &fakeSummariser{
			response: sampleSummaryJSON(func(s *contextpkg.CompactionSummary) {
				s.Errors = []string{"tool toolu_aaaaaaaaaaaaaaaaaa returned a timeout"}
			}),
		}
		compactor := contextpkg.NewAutoCompactor(summariser)

		_, err := compactor.Compact(context.Background(), sampleMessages())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, contextpkg.ErrInvalidSummary)).To(BeTrue())
	})

})
