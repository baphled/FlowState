package app

import (
	"context"
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
)

func intPtr(n int) *int { return &n }

// autoConfig is a loop whose window is 1000 tokens and whose reserve is 100, so the line is 900;
// keep_recent is one token, so the cut falls at the last turn.
func autoConfig(turns ...[]provider.StreamEvent) (ExecLoopConfig, *execLoopStreamer) {
	streamer := &execLoopStreamer{turns: turns}
	cfg := execLoopFixture(streamer)
	cfg.MaxTurns = 8
	cfg.Compaction = CompactionConfig{ContextWindow: intPtr(1000), Reserve: intPtr(100), KeepRecent: intPtr(1)}
	return cfg, streamer
}

// usedTurn is a turn that calls echo with a message of size bytes and reports a context of ctxTokens.
func usedTurn(id string, size int, ctxTokens int64) []provider.StreamEvent {
	args, _ := json.Marshal(map[string]string{"message": strings.Repeat("m", size)})
	call := provider.ToolCall{ID: id, Name: "echo", Arguments: args}
	finish := provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: "stop",
		Usage: &provider.Usage{InputTokens: ctxTokens}}
	return []provider.StreamEvent{provText("turn " + id), provCall(call), finish}
}

func doneTurn() []provider.StreamEvent { return []provider.StreamEvent{provText("done"), provFinish()} }

func countOf(cfg ExecLoopConfig, typ string) int {
	n := 0
	for _, t := range eventTypes(cfg.Store, cfg.Session.ID) {
		if t == typ {
			n++
		}
	}
	return n
}

// summaryCalls counts the requests the summariser made.
func summaryCalls(reqs []*provider.ChatRequest) int {
	n := 0
	for _, r := range reqs {
		if r.Messages[0].Role == "system" && r.Messages[0].Content == compactSystemPrompt {
			n++
		}
	}
	return n
}

func indexOfType(cfg ExecLoopConfig, typ string, last bool) int {
	found := -1
	for i, t := range eventTypes(cfg.Store, cfg.Session.ID) {
		if t == typ && (last || found < 0) {
			found = i
		}
	}
	return found
}

var _ = Describe("The automatic compaction trigger", func() {
	var (
		cfg      ExecLoopConfig
		streamer *execLoopStreamer
		res      *ExecLoopResult
		err      error
	)
	run := func() { res, err = RunExecLoop(context.Background(), cfg) }

	Context("when the reported context crosses window - reserve", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1, 950), summaryTurn("the summary"), doneTurn())
			cfg.MaxTurns = 3
			run()
		})

		It("completes the run", func() { Expect(res.FinalContent).To(Equal("done")) })
		It("appends one auto compaction", func() { Expect(countOf(cfg, TypeContextCompacted)).To(Equal(1)) })
		It("records the reason auto", func() {
			events := eventsOf(cfg)
			Expect(payloadFieldText(events[indexOfType(cfg, TypeContextCompacted, false)].PayloadJSON, "reason")).
				To(Equal(CompactionReasonAuto))
		})
		It("compacts before the next assistant.message", func() {
			Expect(indexOfType(cfg, TypeContextCompacted, false)).To(BeNumerically("<", indexOfType(cfg, TypeAssistantMessage, true)))
		})
		It("sends the first message, the summary, in the next provider request", func() {
			Expect(streamer.reqs[3].Messages[0].Content).To(ContainSubstring("--- summary ---\nthe summary"))
		})
		It("does not count the compaction as a turn", func() { Expect(res.Turns).To(Equal(int64(3))) })
		It("writes no assistant.message for the compaction", func() {
			Expect(countOf(cfg, TypeAssistantMessage)).To(Equal(3))
		})
		It("rebuilds the history it continues with as the log rebuilds", func() {
			events := eventsOf(cfg)
			want, rerr := rebuildCompacted(events[:indexOfType(cfg, TypeContextCompacted, false)+1])
			if rerr != nil {
				panic(rerr)
			}
			Expect(streamer.reqs[3].Messages).To(Equal(want))
		})
	})

	Context("when the context stays under the line", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1, 200), doneTurn())
			run()
		})
		It("never compacts", func() { Expect(countOf(cfg, TypeContextCompacted)).To(BeZero()) })
	})

	Context("when the window is unknown", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1, 250000), doneTurn())
			cfg.Compaction.ContextWindow = nil
			run()
		})
		It("never calls the summariser", func() { Expect(summaryCalls(streamer.reqs)).To(BeZero()) })
		It("still finishes the run", func() { Expect(res.FinalContent).To(Equal("done")) })
	})

	Context("when compaction is disabled", func() {
		BeforeEach(func() {
			off := false
			cfg, streamer = autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1, 950), doneTurn())
			cfg.Compaction.Enabled = &off
			run()
		})
		It("never compacts", func() { Expect(summaryCalls(streamer.reqs)).To(BeZero()) })
	})

	Context("when MaxTokens is larger than the reserve", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1, 600), summaryTurn("s"), doneTurn())
			cfg.MaxTokens = 500
			run()
		})
		It("raises the reserve to it, so the line is 500", func() {
			Expect(countOf(cfg, TypeContextCompacted)).To(Equal(1))
		})
	})

	Context("when the window is not larger than the reserve", func() {
		BeforeEach(func() {
			cfg = seededTwoTurns()
			cfg.Compaction = CompactionConfig{ContextWindow: intPtr(8000), KeepRecent: intPtr(1)}
			streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{summaryTurn("s")}}
			cfg.Streamer = streamer
			history := []provider.Message{}
			meter := &compactRun{hasBase: true, base: 7000}
			if berr := meter.before(context.Background(), &cfg, leaseToken(cfg), nil, &history); berr != nil {
				panic(berr)
			}
		})
		It("never calls the summariser with the default reserve", func() {
			Expect(summaryCalls(streamer.reqs)).To(BeZero())
		})
	})

	Context("when MaxTokens is larger than the window", func() {
		BeforeEach(func() {
			cfg = seededTwoTurns()
			cfg.MaxTokens = 5000
			streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{summaryTurn("s")}}
			cfg.Streamer = streamer
			history := []provider.Message{}
			meter := &compactRun{hasBase: true, base: 900}
			if berr := meter.before(context.Background(), &cfg, leaseToken(cfg), nil, &history); berr != nil {
				panic(berr)
			}
		})
		It("never calls the summariser", func() { Expect(summaryCalls(streamer.reqs)).To(BeZero()) })
	})

	Context("when the window is just above the reserve", func() {
		var meter *compactRun
		BeforeEach(func() {
			cfg = seededTwoTurns()
			cfg.Compaction.ContextWindow = intPtr(101)
			streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{summaryTurn("s")}}
			cfg.Streamer = streamer
			meter = &compactRun{hasBase: true, base: 950}
		})
		It("holds a compaction that stays above the 1 token line to the ceiling and writes nothing", func() {
			history := []provider.Message{}
			if berr := meter.before(context.Background(), &cfg, leaseToken(cfg), nil, &history); berr != nil {
				panic(berr)
			}
			Expect(countOf(cfg, TypeContextCompacted)).To(BeZero())
		})
		It("makes the summary call when the context is over the line", func() {
			history := []provider.Message{}
			if berr := meter.before(context.Background(), &cfg, leaseToken(cfg), nil, &history); berr != nil {
				panic(berr)
			}
			Expect(summaryCalls(streamer.reqs)).To(Equal(1))
		})
		It("makes no summary call when the context is at the line", func() {
			meter.base = 1
			history := []provider.Message{}
			if berr := meter.before(context.Background(), &cfg, leaseToken(cfg), nil, &history); berr != nil {
				panic(berr)
			}
			Expect(summaryCalls(streamer.reqs)).To(BeZero())
		})
	})

	Context("when the first over-the-line turn has no eligible cut", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(usedTurn("c1", 1, 950), usedTurn("c2", 1, 960), doneTurn())
			cfg.Compaction.KeepRecent = intPtr(1000000)
			cfg.Pool, cfg.TurnBudget = NewBudgetPool(10), 1
			run()
		})
		It("gives the summary's pool unit back", func() {
			Expect(cfg.Pool.Remaining()).To(Equal(int64(10 - 3)))
		})
		It("never calls the summariser", func() { Expect(summaryCalls(streamer.reqs)).To(BeZero()) })
	})

	Context("when the lease is stolen during the summary call", func() {
		BeforeEach(func() {
			cfg, _ = autoConfig()
			hooked := &hookStreamer{execLoopStreamer: execLoopStreamer{turns: [][]provider.StreamEvent{
				usedTurn("c1", 1, 100), usedTurn("c2", 1, 950), summaryTurn("s"), doneTurn()}}}
			stolen := false
			hooked.hook = func() {
				if stolen || len(hooked.reqs) != 2 {
					return
				}
				stolen = true
				ctx := context.Background()
				token, terr := cfg.Store.LeaseToken(ctx, cfg.LeaseItem)
				if terr != nil {
					panic(terr)
				}
				if rerr := cfg.Store.ReleaseLease(ctx, cfg.LeaseItem, cfg.LeaseAgent, token); rerr != nil {
					panic(rerr)
				}
				if _, aerr := cfg.Store.AcquireLease(ctx, cfg.LeaseItem, "thief"); aerr != nil {
					panic(aerr)
				}
			}
			cfg.Streamer = hooked
			run()
		})
		It("ends the run fenced", func() { Expect(err).To(MatchError(ErrExecLoopFenced)) })
		It("appends no compaction", func() { Expect(countOf(cfg, TypeContextCompacted)).To(BeZero()) })
	})

	Context("when a turn reports a small context but its tool results are large", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1200, 700), summaryTurn("s"), doneTurn())
			run()
		})
		It("adds the estimate of what was appended since", func() {
			Expect(countOf(cfg, TypeContextCompacted)).To(Equal(1))
		})
	})

	Context("when no turn reports usage", func() {
		BeforeEach(func() {
			quiet := func(id string, size int) []provider.StreamEvent {
				turn := usedTurn(id, size, 0)
				turn[2] = provFinish()
				return turn
			}
			cfg, streamer = autoConfig(quiet("c1", 1100), quiet("c2", 200), summaryTurn("s"), doneTurn())
			cfg.Compaction.ContextWindow = intPtr(3000)
			cfg.SystemPrompt = strings.Repeat("p", 9000)
			run()
		})
		It("estimates the whole history with the system prompt and the tool definitions", func() {
			Expect(countOf(cfg, TypeContextCompacted)).To(Equal(1))
		})
	})

	Context("when a resumed session is already over the line", func() {
		BeforeEach(func() {
			cfg, _ = autoConfig(usedTurn("c1", 2000, 10), usedTurn("c2", 2000, 10), doneTurn())
			cfg.Compaction.ContextWindow = nil
			run()
			streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{summaryTurn("resumed"), doneTurn()}}
			cfg.Streamer = streamer
			cfg.Compaction.ContextWindow = intPtr(1000)
			run()
		})
		It("compacts at the start of the run, before its first turn", func() {
			Expect(summaryCalls(streamer.reqs[:1])).To(Equal(1))
		})
		It("replays from the compaction", func() {
			Expect(streamer.reqs[1].Messages[0].Content).To(ContainSubstring("--- summary ---\nresumed"))
		})
	})

	Context("when the summary comes back empty", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1, 950), provFinishOnly(),
				usedTurn("c3", 1, 960), usedTurn("c4", 1, 970), doneTurn())
			run()
		})
		It("writes no compaction", func() { Expect(countOf(cfg, TypeContextCompacted)).To(BeZero()) })
		It("does not retry it on the later turns", func() { Expect(summaryCalls(streamer.reqs)).To(Equal(1)) })
		It("continues the run to its end", func() { Expect(res.FinalContent).To(Equal("done")) })
	})

	Context("when the summary leaves tokens_after above the line", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1, 950),
				summaryTurn(strings.Repeat("x", 4000)), doneTurn())
			run()
		})
		It("fails the compaction and writes nothing", func() { Expect(countOf(cfg, TypeContextCompacted)).To(BeZero()) })
		It("continues the run", func() { Expect(res.FinalContent).To(Equal("done")) })
	})

	Context("when the pool holds exactly the units of two turns and the summary", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1, 950), summaryTurn("s"), doneTurn())
			cfg.Pool, cfg.TurnBudget = NewBudgetPool(3), 1
			run()
		})
		It("spends the last unit on the summary", func() { Expect(countOf(cfg, TypeContextCompacted)).To(Equal(1)) })
		It("ends the next turn budget_exhausted", func() { Expect(err).To(MatchError(ErrExecLoopBudget)) })
	})

	Context("when the pool cannot cover the summary", func() {
		BeforeEach(func() {
			cfg, streamer = autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1, 950), summaryTurn("s"), doneTurn())
			cfg.Pool, cfg.TurnBudget = NewBudgetPool(2), 1
			run()
		})
		It("makes no summary call", func() { Expect(summaryCalls(streamer.reqs)).To(BeZero()) })
		It("writes no compaction", func() { Expect(countOf(cfg, TypeContextCompacted)).To(BeZero()) })
	})

	Context("when the run is cancelled during the summary call", func() {
		BeforeEach(func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			cancelling := &cancelOnStream{execLoopStreamer: execLoopStreamer{turns: [][]provider.StreamEvent{
				usedTurn("c1", 1, 100), usedTurn("c2", 1, 950), summaryTurn("s"), doneTurn()}}, n: 3, cancel: cancel}
			cfg, _ = autoConfig()
			cfg.Streamer = cancelling
			res, err = RunExecLoop(ctx, cfg)
		})
		It("ends the run cancelled", func() { Expect(err).To(MatchError(context.Canceled)) })
		It("appends no compaction", func() { Expect(countOf(cfg, TypeContextCompacted)).To(BeZero()) })
	})

	Context("when a compaction made the baseline stale", func() {
		It("takes the compaction's tokens_after as the baseline", func() {
			cfg = seededTwoTurns()
			cfg.Streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{summaryTurn("s")}}
			meter := &compactRun{hasBase: true, base: 950}
			history := []provider.Message{}
			if berr := meter.before(context.Background(), &cfg, leaseToken(cfg), nil, &history); berr != nil {
				panic(berr)
			}
			events := eventsOf(cfg)
			Expect(float64(meter.size(history, 0))).To(Equal(payloadMap(events[len(events)-1].PayloadJSON)["tokens_after"]))
		})
	})
})

// seededTwoTurns is a loop whose log holds two finished tool turns and a final one, written with no
// window (so with no compaction), then given the window of autoConfig.
func seededTwoTurns() ExecLoopConfig {
	cfg, _ := autoConfig(usedTurn("c1", 1, 100), usedTurn("c2", 1, 100), doneTurn())
	window := cfg.Compaction.ContextWindow
	cfg.Compaction.ContextWindow = nil
	if _, err := RunExecLoop(context.Background(), cfg); err != nil {
		panic(err)
	}
	cfg.Compaction.ContextWindow = window
	return cfg
}

// leaseToken takes the exec lease of the loop's session for the spec, as the loop does.
func leaseToken(cfg ExecLoopConfig) int64 {
	token, err := cfg.Store.AcquireLease(context.Background(), cfg.LeaseItem, cfg.LeaseAgent)
	if err != nil {
		panic(err)
	}
	return token
}

func provFinishOnly() []provider.StreamEvent { return []provider.StreamEvent{provFinish()} }

var _ = Describe("Compact with a ceiling on tokens_after", func() {
	It("fails with context_too_large and writes nothing", func() {
		cfg := seededTwoTurns()
		_, cerr := Compact(context.Background(), cfg.Store, cfg.Session.ID, CompactOptions{
			Streamer: &execLoopStreamer{turns: [][]provider.StreamEvent{summaryTurn("s")}}, Model: "m", KeepRecent: 1,
			MaxTokensAfter: 1, Lease: CompactLease{Item: cfg.LeaseItem, Agent: cfg.LeaseAgent, Token: leaseToken(cfg)}})
		Expect(failureReason(cerr)).To(Equal(CompactReasonContextTooLarge))
	})
})

var _ = Describe("The automatic compaction of a delegated child", func() {
	var (
		f   *delegateFixture
		out DelegateResult
	)
	readTurn := func(id string, ctxTokens int64) []provider.StreamEvent {
		call := provider.ToolCall{ID: id, Name: "fs.read", Arguments: json.RawMessage(`{"path":"missing.txt"}`)}
		finish := provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: "stop",
			Usage: &provider.Usage{InputTokens: ctxTokens}}
		return []provider.StreamEvent{provCall(call), finish}
	}

	BeforeEach(func() {
		f = newDelegateFixture(readTurn("r1", 100), readTurn("r2", 950), summaryTurn("child summary"), finishText("child done"))
		f.profile.MaxTurns, f.profile.ContextWindow = 4, 1000
		req := f.request()
		// The configured window is far larger: only the profile's window puts the child over its line.
		req.Compaction = CompactionConfig{ContextWindow: intPtr(100000000), Reserve: intPtr(100), KeepRecent: intPtr(1)}
		var err error
		out, err = RunDelegatedChild(context.Background(), req)
		if err != nil {
			panic(err)
		}
	})

	It("compacts the child by its profile's window", func() {
		Expect(f.events(out.ChildID, TypeContextCompacted)).To(HaveLen(1))
	})
	It("leaves the parent's log without a compaction", func() {
		Expect(f.events(f.parent.ID, TypeContextCompacted)).To(BeEmpty())
	})
	It("reports the child's provider turns without the compaction", func() { Expect(out.Turns).To(Equal(int64(3))) })
	It("charges the summary's unit to the child at settlement", func() {
		Expect(f.reservation(out.ChildID).Consumed).To(Equal(int64(4)))
	})
})

var _ = Describe("The window of a delegated child whose profile has none", func() {
	var (
		f   *delegateFixture
		out DelegateResult
	)
	BeforeEach(func() {
		readTurn := func(id string, ctxTokens int64) []provider.StreamEvent {
			call := provider.ToolCall{ID: id, Name: "fs.read", Arguments: json.RawMessage(`{"path":"missing.txt"}`)}
			finish := provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: "stop",
				Usage: &provider.Usage{InputTokens: ctxTokens}}
			return []provider.StreamEvent{provCall(call), finish}
		}
		f = newDelegateFixture(readTurn("r1", 100), readTurn("r2", 950), summaryTurn("cfg summary"), finishText("done"))
		f.profile.MaxTurns = 4
		req := f.request()
		req.Compaction = CompactionConfig{ContextWindow: intPtr(1000), Reserve: intPtr(100), KeepRecent: intPtr(1)}
		var err error
		out, err = RunDelegatedChild(context.Background(), req)
		if err != nil {
			panic(err)
		}
	})
	It("compacts the child by compaction.context_window", func() {
		Expect(f.events(out.ChildID, TypeContextCompacted)).To(HaveLen(1))
	})
})
