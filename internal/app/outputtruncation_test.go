package app

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
)

// glmConfig is a loop on z.ai/glm-5.3, a model the output table knows (default cap 32768, ceiling 131072).
func glmConfig(maxTokens int64, turns ...[]provider.StreamEvent) (ExecLoopConfig, *execLoopStreamer) {
	streamer := &execLoopStreamer{turns: turns}
	cfg := execLoopFixture(streamer)
	known := *cfg.Session
	known.Provider, known.ModelID = "z.ai", "glm-5.3"
	cfg.Session, cfg.MaxTokens = &known, maxTokens
	return cfg, streamer
}

// lengthWithCall is a length-finished turn that carries one complete tool call.
func lengthWithCall(id string) []provider.StreamEvent {
	cut := lengthTurn("cut")
	return []provider.StreamEvent{cut[0], provCall(echoCall(id)), cut[1]}
}

// terminalErrors counts the terminal provider.error events of cfg's log.
func terminalErrors(cfg ExecLoopConfig) int {
	n := 0
	for _, e := range eventsOf(cfg) {
		if e.Type == TypeProviderError && payloadFieldRaw(e.PayloadJSON, "terminal") == "true" {
			n++
		}
	}
	return n
}

// rebuiltTexts is the assistant text of the history a rebuild of cfg's whole log yields.
func rebuiltTexts(cfg ExecLoopConfig) []string {
	msgs, err := rebuildCompacted(eventsOf(cfg))
	if err != nil {
		panic(err)
	}
	return assistantTexts(msgs)
}

var _ = Describe("The output truncation retry", func() {
	var (
		cfg      ExecLoopConfig
		streamer *execLoopStreamer
		res      *ExecLoopResult
		err      error
	)
	run := func() { res, err = RunExecLoop(context.Background(), cfg) }

	Context("when a length finish with no tool call is followed by a stop finish", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(lengthTurn("I"), doneTurn())
			cfg.Pool, cfg.TurnBudget = NewBudgetPool(5), 1
			run()
		})
		It("makes exactly one more request", func() { Expect(streamer.reqs).To(HaveLen(2)) })
		It("sends the first request the default cap", func() { Expect(streamer.reqs[0].MaxTokens).To(Equal(int64(8192))) })
		It("doubles the cap of the retry", func() { Expect(streamer.reqs[1].MaxTokens).To(Equal(int64(16384))) })
		It("completes the run", func() { Expect(err).To(Succeed()) })
		It("answers from the retry", func() { Expect(res.FinalContent).To(Equal("done")) })
		It("counts the retry as a second turn", func() { Expect(res.Turns).To(Equal(int64(2))) })
		It("reserves a second pool unit for the retry", func() { Expect(cfg.Pool.Remaining()).To(Equal(int64(3))) })
		It("writes the marker with the reason output_truncated", func() {
			Expect(payloadOfFirst(cfg, "provider.error", "reason")).To(Equal(ReasonOutputTruncated))
		})
		It("writes the marker as non-terminal", func() { Expect(terminalOfFirst(cfg, "provider.error")).To(Equal("false")) })
		It("leaves the session active", func() { Expect(statusOf(cfg.Store, cfg.Session.ID)).To(Equal("active")) })
		It("keeps the truncated assistant.message in the log", func() {
			Expect(countOf(cfg, TypeAssistantMessage)).To(Equal(2))
		})
		It("logs the marker between the truncated answer and the retry's", func() {
			Expect(typesOf(cfg, "provider.error", TypeAssistantMessage)).To(Equal([]string{
				TypeAssistantMessage, "provider.error", TypeAssistantMessage}))
		})
		It("leaves the truncated message out of the retry's history", func() {
			Expect(streamer.reqs[1].Messages).To(Equal(streamer.reqs[0].Messages))
		})
	})

	Context("when the retry is cut off too", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(lengthTurn("I"), lengthTurn("I"), doneTurn())
			cfg.Pool, cfg.TurnBudget = NewBudgetPool(5), 1
			run()
		})
		It("fails with ErrOutputTruncated", func() { Expect(err).To(MatchError(ErrOutputTruncated)) })
		It("maps onto the output_truncated code", func() { Expect(ErrorCode(err)).To(Equal(CodeOutputTruncated)) })
		It("is not a provider error", func() { Expect(err).NotTo(MatchError(ErrProvider)) })
		It("is not a max_turns failure", func() { Expect(err).NotTo(MatchError(ErrMaxTurns)) })
		It("retries only once", func() { Expect(streamer.reqs).To(HaveLen(2)) })
		It("marks the session failed", func() { Expect(statusOf(cfg.Store, cfg.Session.ID)).To(Equal("failed")) })
		It("ends the log on a terminal provider.error", func() {
			Expect(eventsOf(cfg)[len(eventsOf(cfg))-1].Type).To(Equal(TypeProviderError))
		})
		It("writes the final provider.error as terminal", func() { Expect(finalPayloadField(cfg, "terminal")).To(BeTrue()) })
		It("names the reason output_truncated on the final provider.error", func() {
			Expect(finalPayloadField(cfg, "reason")).To(Equal(ReasonOutputTruncated))
		})
		It("counts both turns", func() { Expect(res.Turns).To(Equal(int64(2))) })
	})

	Context("when the cap is already at the ceiling", func() {
		BeforeEach(func() {
			cfg, streamer = glmConfig(131072, lengthTurn("I"), doneTurn())
			run()
		})
		It("fails with ErrOutputTruncated", func() { Expect(err).To(MatchError(ErrOutputTruncated)) })
		It("makes no retry", func() { Expect(streamer.reqs).To(HaveLen(1)) })
		It("writes no non-terminal marker", func() { Expect(countOf(cfg, TypeProviderError)).To(Equal(1)) })
	})

	Context("when an unknown model's explicit cap is at its ceiling", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(lengthTurn("I"), doneTurn())
			cfg.MaxTokens = 32768
			run()
		})
		It("fails without a retry", func() { Expect(streamer.reqs).To(HaveLen(1)) })
	})

	Context("when an unknown model's cap would double past its ceiling", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(lengthTurn("I"), doneTurn())
			cfg.MaxTokens = 20000
			run()
		})
		It("limits the retry to the ceiling of 32768", func() { Expect(streamer.reqs[1].MaxTokens).To(Equal(int64(32768))) })
	})

	Context("when a table model runs on its default cap", func() {
		BeforeEach(func() {
			cfg, streamer = glmConfig(0, lengthTurn("I"), doneTurn())
			run()
		})
		It("doubles the default cap", func() { Expect(streamer.reqs[1].MaxTokens).To(Equal(int64(65536))) })
	})

	Context("when a table model's explicit cap would double past the table maximum", func() {
		BeforeEach(func() {
			cfg, streamer = glmConfig(100000, lengthTurn("I"), doneTurn())
			run()
		})
		It("limits the retry to the table maximum", func() { Expect(streamer.reqs[1].MaxTokens).To(Equal(int64(131072))) })
	})

	Context("when the compaction retry is cut off again", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1, 950), provFinishOnly(),
				lengthTurn("cut"), summaryTurn("the summary"), lengthTurn("cut again"), doneTurn())
			run()
		})
		It("fails with ErrOutputTruncated: one recovery per turn", func() { Expect(err).To(MatchError(ErrOutputTruncated)) })
		It("does not escalate after the compaction", func() { Expect(streamer.reqs).To(HaveLen(6)) })
	})

	Context("when a length finish carries a complete tool call", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(lengthWithCall("c1"), doneTurn())
			run()
		})
		It("runs the call and continues", func() { Expect(res.FinalContent).To(Equal("done")) })
		It("writes no provider.error", func() { Expect(countOf(cfg, TypeProviderError)).To(BeZero()) })
		It("keeps the cap of the next request", func() { Expect(streamer.reqs[1].MaxTokens).To(Equal(int64(8192))) })
	})

	Context("when the turn limit is reached while the model still asks for tools", func() {
		BeforeEach(func() {
			cfg, _ = autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1, 100))
			cfg.MaxTurns = 2
			run()
		})
		It("still fails with max_turns", func() { Expect(err).To(MatchError(ErrMaxTurns)) })
		It("is not output_truncated", func() { Expect(err).NotTo(MatchError(ErrOutputTruncated)) })
	})

	Context("when the pool cannot pay for the retry", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(lengthTurn("I"), doneTurn())
			cfg.Pool, cfg.TurnBudget = NewBudgetPool(1), 1
			run()
		})
		It("stops budget exhausted instead of retrying", func() { Expect(err).To(MatchError(ErrExecLoopBudget)) })
		It("makes no second request", func() { Expect(streamer.reqs).To(HaveLen(1)) })
		It("does not report output_truncated: a pool of one is a budget stop", func() {
			Expect(err).NotTo(MatchError(ErrOutputTruncated))
		})
		It("writes no marker for a retry that never ran", func() { Expect(countOf(cfg, TypeProviderError)).To(BeZero()) })
	})

	Context("when the retry makes a tool call, so the truncated message has a successor in the log", func() {
		BeforeEach(func() {
			cfg, _ = autoConfig(lengthTurn("I"), usedTurn("c1", 1, 100), doneTurn())
			cfg.MaxTurns = 8
			run()
		})
		It("completes the run", func() { Expect(err).To(Succeed()) })
		It("drops the truncated message from a rebuild of the log", func() {
			Expect(rebuiltTexts(cfg)).NotTo(ContainElement("I"))
		})
		It("keeps the retry's own assistant message in the rebuild", func() {
			Expect(rebuiltTexts(cfg)).To(ContainElement("turn c1"))
		})
	})

	Context("when a run resumes after a budget stop that followed an escalation retry", func() {
		var resume *execLoopStreamer
		BeforeEach(func() {
			cfg, _ = autoConfig(lengthTurn("I"), usedTurn("c1", 1, 100), usedTurn("c2", 1, 100), doneTurn())
			cfg.MaxTurns = 8
			cfg.Pool, cfg.TurnBudget = NewBudgetPool(3), 1
			run()
			if !res.BudgetExhausted {
				panic("setup: the pool did not stop the first run")
			}
			resume = &execLoopStreamer{turns: [][]provider.StreamEvent{doneTurn()}}
			cfg.Streamer, cfg.Pool = resume, nil
			run()
		})
		It("resumes", func() { Expect(err).To(Succeed()) })
		It("does not send the truncated message back to the model", func() {
			Expect(assistantTexts(resume.reqs[0].Messages)).NotTo(ContainElement("I"))
		})
		It("sends the retry's own assistant message", func() {
			Expect(assistantTexts(resume.reqs[0].Messages)).To(ContainElement("turn c1"))
		})
	})

	Context("when the turn limit is 1 and the retry completes", func() {
		BeforeEach(func() {
			cfg, _ = autoConfig(lengthTurn("I"), doneTurn())
			cfg.MaxTurns = 1
			run()
		})
		It("completes: the retry may overshoot the limit by one turn", func() { Expect(err).To(Succeed()) })
		It("counts the retry as a second turn", func() { Expect(res.Turns).To(Equal(int64(2))) })
	})

	Context("when the turn limit is 1 and the retry asks for a tool", func() {
		BeforeEach(func() {
			cfg, _ = autoConfig(lengthTurn("I"), usedTurn("c1", 1, 100), doneTurn())
			cfg.MaxTurns = 1
			run()
		})
		It("fails with max_turns after the retry's turn", func() { Expect(err).To(MatchError(ErrMaxTurns)) })
	})

	Context("when the escalated request does not fit the context window", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(lengthTurn("I"), overflowTurn(), doneTurn())
			run()
		})
		It("fails with ErrOutputTruncated", func() { Expect(err).To(MatchError(ErrOutputTruncated)) })
		It("is not a provider error", func() { Expect(err).NotTo(MatchError(ErrProvider)) })
		It("maps onto the output_truncated code, not provider_error", func() {
			Expect(ErrorCode(err)).To(Equal(CodeOutputTruncated))
		})
		It("makes no third request", func() { Expect(streamer.reqs).To(HaveLen(2)) })
		It("records one terminal failure only: the provider's rejection is not one", func() {
			Expect(terminalErrors(cfg)).To(Equal(1))
		})
		It("ends the log on a terminal output_truncated provider.error", func() {
			Expect(finalPayloadField(cfg, "reason")).To(Equal(ReasonOutputTruncated))
		})
	})

	Context("when an unknown model's explicit cap is above the heuristic ceiling", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(lengthTurn("I"), doneTurn())
			cfg.MaxTokens = 40000
			run()
		})
		It("makes no retry", func() { Expect(streamer.reqs).To(HaveLen(1)) })
		It("says the ceiling is the one of an unknown model", func() {
			Expect(err).To(MatchError(ContainSubstring("at the ceiling (32768 for an unknown model)")))
		})
	})

	Context("when a table model is at its ceiling", func() {
		BeforeEach(func() {
			cfg, _ = glmConfig(131072, lengthTurn("I"), doneTurn())
			run()
		})
		It("names the model's own ceiling", func() { Expect(err).To(MatchError(ContainSubstring("model's ceiling (131072)"))) })
	})
})

var _ = DescribeTable("MaxOutputCeiling",
	func(providerName, model string, want int64) {
		Expect(MaxOutputCeiling(providerName, model)).To(Equal(want))
	},
	Entry("is the table maximum of a z.ai model, not the default cap", "z.ai", "glm-5.3", int64(131072)),
	Entry("accepts the zai alias", "zai", "glm-5.3", int64(131072)),
	Entry("is the table maximum of an anthropic model", "anthropic", "claude-sonnet-5-5", int64(128000)),
	Entry("is 4 x the default for an unknown model", "ollama", "llama3", int64(32768)),
)
