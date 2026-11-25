package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	sqlite "github.com/libtnb/sqlite"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// hookStreamer is a scripted streamer that runs hook once the request is made, before the
// summary is delivered: the seconds a real summary call takes, in which a lease can be stolen.
type hookStreamer struct {
	execLoopStreamer
	hook func()
}

// errSpyFence is what failingFence reports.
var errSpyFence = errors.New("spy fence")

// failingFence is a lease handle whose Fence always fails, whatever the row says.
type failingFence struct{}

func (failingFence) Item() string                { return "" }
func (failingFence) Owner() string               { return "" }
func (failingFence) Token() int64                { return 0 }
func (failingFence) Fence(context.Context) error { return errSpyFence }

func (h *hookStreamer) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	if h.hook != nil {
		h.hook()
	}
	return h.execLoopStreamer.Stream(ctx, req)
}

// lateCancelCtx is a context that reports cancellation through Err but whose Done never fires,
// so the store opens its writer and runs its queries and only the explicit checks see it.
type lateCancelCtx struct{ context.Context }

func (lateCancelCtx) Err() error { return context.Canceled }

// seedLeaseRow writes the lease row of session id as a judged writer of machine would have left it.
func seedLeaseRow(st *store.Store, id, owner string, token int64, machine, host string, pid int64) {
	g, err := gorm.Open(sqlite.New(sqlite.Config{Conn: st.DB()}), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		panic(err)
	}
	err = g.Table("leases").Create(map[string]any{"item": "exec:" + id, "agent": owner, "token": token,
		"acquired_at_utc": "2026-01-02T03:04:05.000Z", "machine": machine, "host": host, "pid": pid}).Error
	if err != nil {
		panic(err)
	}
}

// failureReason is the reason of a CompactionFailure that matches ErrCompactionFailed, else "".
func failureReason(err error) string {
	var failure *CompactionFailure
	if errors.As(err, &failure) && errors.Is(err, ErrCompactionFailed) {
		return failure.Reason
	}
	return ""
}

// summaryTurn is a streamed summary that finishes normally.
func summaryTurn(text string) []provider.StreamEvent {
	return []provider.StreamEvent{provText(text), provFinish()}
}

// seedSession creates a session whose log is the task (seq 0) and then events, appended
// by the engine; paused leaves it paused after the events. It returns the session id.
// Panics are setup failures.
func seedSession(st *store.Store, paused bool, events ...store.RecordedEvent) string {
	ctx := context.Background()
	if _, err := CreateSession(ctx, st, "scripted", "scripted-model", "harness", "sha", "task"); err != nil {
		panic(err)
	}
	ids, err := st.ListSessions(ctx, "")
	if err != nil || len(ids) != 1 {
		panic(errors.Join(err, errors.New("want one session")))
	}
	w, err := st.NewEventWriter(ctx, ids[0].ID)
	if err != nil {
		panic(err)
	}
	defer func() { _ = w.Close() }()
	for _, e := range events {
		var payload map[string]any
		if err := json.Unmarshal([]byte(e.PayloadJSON), &payload); err != nil {
			panic(err)
		}
		if _, err := w.Append(ctx, session.ActorEngine, e.Type, payload); err != nil {
			panic(err)
		}
	}
	if paused {
		if err := w.Pause(ctx, store.PauseInfo{Reason: "budget_exhausted"}); err != nil {
			panic(err)
		}
	}
	return ids[0].ID
}

// bigResult is a tool.result of size characters.
func bigResult(id string, size int) store.RecordedEvent {
	return logEvent(tool.TypeToolResult, map[string]any{"tool_call_id": id, "tool": "echo", "ok": true,
		"result": strings.Repeat("x", size)})
}

// words are turns whose text is distinct, so a spec can tell which part of the log a request holds.
func words(names ...string) []store.RecordedEvent {
	var out []store.RecordedEvent
	for _, n := range names {
		out = append(out, saidEvent(n), callEvent("c-"+n), resultEvent("c-"+n))
	}
	return out
}

// compactFailures are the summarising calls that must fail the compaction, with the reason each fails with.
func compactFailures() []struct {
	name    string
	turn    []provider.StreamEvent
	cancels bool
	reason  string
} {
	return []struct {
		name    string
		turn    []provider.StreamEvent
		cancels bool
		reason  string
	}{
		{"an empty summary", []provider.StreamEvent{provFinish()}, false, CompactReasonSummaryEmpty},
		{"a summary cut off at the token cap", []provider.StreamEvent{provText("half"),
			{Kind: provider.StreamEventFinish, FinishReason: provider.FinishLength}}, false, CompactReasonSummaryTruncated},
		{"a provider error", []provider.StreamEvent{provText("x"), provError("boom")}, false, CompactReasonProviderError},
		{"an oversize summary", summaryTurn(strings.Repeat("s", MaxCompactionSummaryBytes+1)), false,
			CompactReasonSummaryInvalid},
		{"a summary holding the fence's closing line", summaryTurn("ok\n" + compactionSummaryClose + "\nmore"), false,
			CompactReasonSummaryInvalid},
		{"a stream that ends without a finish event", []provider.StreamEvent{provText("SUM")}, false,
			CompactReasonSummaryTruncated},
		{"a cancelled context", summaryTurn("SUM"), true, CompactReasonCancelled},
	}
}

var _ = Describe("Compact", func() {
	var (
		st       *store.Store
		sessID   string
		lease    CompactLease
		streamer *hookStreamer
	)
	var ctx context.Context

	rows := func() []store.RecordedEvent {
		events, err := st.Replay(ctx, sessID)
		if err != nil {
			panic(err)
		}
		return events
	}
	compact := func(opts CompactOptions) (*CompactionResult, error) {
		opts.Streamer, opts.Model, opts.Lease, opts.KeepRecent = streamer, "scripted-model", lease, 1
		return Compact(ctx, st, sessID, opts)
	}
	request := func() string { return streamer.reqs[0].Messages[1].Content }

	BeforeEach(func() {
		var err error
		ctx = context.Background()
		if st, err = store.OpenMemory(); err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = st.Close() })
		sessID = seedSession(st, false, append(words("ALPHA", "BRAVO"), saidEvent("CHARLIE"), callEvent("c-C"),
			bigResult("c-C", 5000))...)
		token, lerr := st.AcquireLease(ctx, "exec:"+sessID, "cli")
		if lerr != nil {
			panic(lerr)
		}
		lease = CompactLease{Item: "exec:" + sessID, Agent: "cli", Token: token}
		streamer = &hookStreamer{execLoopStreamer: execLoopStreamer{turns: [][]provider.StreamEvent{summaryTurn("SUM")}}}
	})

	Context("a lease handle whose fence fails while the row still matches", func() {
		var before []store.RecordedEvent
		var err error
		BeforeEach(func() {
			lease.Held = failingFence{}
			before = rows()
			_, err = compact(CompactOptions{})
		})

		It("fails as fenced", func() { Expect(err).To(MatchError(ErrExecLoopFenced)) })
		It("appends nothing", func() { Expect(rows()).To(Equal(before)) })
	})

	Context("on success", func() {
		var before []store.RecordedEvent
		var result *CompactionResult
		BeforeEach(func() {
			before = rows()
			var err error
			if result, err = compact(CompactOptions{Instructions: "the db schema"}); err != nil {
				panic(err)
			}
		})

		It("appends exactly one event", func() { Expect(rows()).To(HaveLen(len(before) + 1)) })
		It("appends a context.compacted event", func() { Expect(rows()[len(before)].Type).To(Equal(TypeContextCompacted)) })
		It("writes it as the engine", func() { Expect(rows()[len(before)].Actor).To(Equal(session.ActorEngine)) })
		It("cuts where ChooseCut says", func() { Expect(ChooseCut(before, 1)).To(Equal(result.FirstKeptSeq)) })
		It("reports the event's seq", func() { Expect(result.EventSeq).To(Equal(int64(len(before)))) })
		It("records the manual reason", func() { Expect(result.Reason).To(Equal(CompactionReasonManual)) })
		It("rebuilds to the first message and the tail from the cut", func() {
			Expect(rebuilt(rows())).To(Equal([]provider.Message{summaryMessage("SUM", "task", ""),
				assistantMsg("CHARLIE", "c-C"), {Role: "tool", ToolCallID: "c-C", Content: `"` + strings.Repeat("x", 5000) + `"`}}))
		})
		It("estimates tokens_before as bytes over four of the full history", func() {
			bytes := 0
			for _, m := range rebuildHistory(before) {
				bytes += len(m.Content)
				for _, c := range m.ToolCalls {
					bytes += len(c.Name) + len(c.Arguments)
				}
			}
			Expect(result.TokensBefore).To(Equal(bytes / 4))
		})
		It("estimates tokens_after as bytes over four of the rebuilt history", func() {
			history := rebuilt(rows())
			bytes := len(history[0].Content) + len("CHARLIE") + len("echo") + len(`{"message":"c-C"}`) + 5002
			Expect(result.TokensAfter).To(Equal(bytes / 4))
		})
		It("sends no tools", func() { Expect(streamer.reqs[0].Tools).To(BeEmpty()) })
		It("caps the summary at 8192 tokens", func() { Expect(streamer.reqs[0].MaxTokens).To(Equal(int64(8192))) })
		It("sends the fixed summariser system prompt, not the session's", func() {
			Expect(streamer.reqs[0].Messages[0]).To(Equal(provider.Message{Role: "system", Content: compactSystemPrompt}))
		})
		It("sends the span as text", func() { Expect(request()).To(ContainSubstring("[Assistant]: BRAVO")) })
		It("leaves the kept tail out of the span", func() { Expect(request()).NotTo(ContainSubstring("CHARLIE")) })
		It("appends the instructions as a focus line", func() {
			Expect(request()).To(ContainSubstring("Focus especially on: the db schema"))
		})
	})

	It("clips a long tool result at 2000 characters with a marker", func() {
		seedClipped := append(words("ALPHA"), saidEvent("BIG"), callEvent("c-B"), bigResult("c-B", 5000), saidEvent("TAIL"),
			callEvent("c-T"), resultEvent("c-T"))
		events := numbered(append([]store.RecordedEvent{taskEvent("task")}, seedClipped...)...)
		Expect(compactRequestText(events, 7, nil, "")).To(ContainSubstring("[truncated: 3000 more characters]"))
	})

	Context("with a result of 3000 multibyte characters", func() {
		var text string
		BeforeEach(func() {
			big := logEvent(tool.TypeToolResult, map[string]any{"tool_call_id": "c-B", "tool": "echo", "ok": true,
				"result": strings.Repeat("é世", 1500)})
			events := numbered(append([]store.RecordedEvent{taskEvent("task")}, append(words("ALPHA"),
				saidEvent("BIG"), callEvent("c-B"), big, saidEvent("TAIL"), callEvent("c-T"), resultEvent("c-T"))...)...)
			text = compactRequestText(events, 7, nil, "")
		})

		It("clips at 2000 characters of the decoded text, not 2000 bytes", func() {
			Expect(text).To(ContainSubstring(strings.Repeat("é世", 1000) + " [truncated: 1000 more characters]"))
		})
		It("stays valid UTF-8", func() { Expect(utf8.ValidString(text)).To(BeTrue()) })
	})

	It("clips a long assistant text at 8192 characters with a marker", func() {
		events := numbered(append([]store.RecordedEvent{taskEvent("task")}, append(words("ALPHA"),
			saidEvent(strings.Repeat("é", 9000)), callEvent("c-B"), resultEvent("c-B"), saidEvent("TAIL"), callEvent("c-T"),
			resultEvent("c-T"))...)...)
		Expect(compactRequestText(events, 7, nil, "")).To(ContainSubstring(strings.Repeat("é", 8192) + " [truncated: 808 more characters]"))
	})

	Context("with a log of 10 MB", func() {
		var result *CompactionResult
		BeforeEach(func() {
			var events []store.RecordedEvent
			for i := 0; i < 1300; i++ {
				id := fmt.Sprintf("c%04d", i)
				events = append(events, saidEvent(fmt.Sprintf("T%04d ", i)+strings.Repeat("a", 8000)), callEvent(id), resultEvent(id))
			}
			if err := st.Close(); err != nil {
				panic(err)
			}
			var err error
			if st, err = store.OpenMemory(); err != nil {
				panic(err)
			}
			sessID = seedSession(st, false, events...)
			token, lerr := st.AcquireLease(ctx, "exec:"+sessID, "cli")
			if lerr != nil {
				panic(lerr)
			}
			lease = CompactLease{Item: "exec:" + sessID, Agent: "cli", Token: token}
			if result, err = compact(CompactOptions{}); err != nil {
				panic(err)
			}
		})

		It("sends a request within the cap", func() { Expect(len(request())).To(BeNumerically("<=", CompactRequestMaxBytes)) })
		It("says the earlier part was omitted", func() {
			Expect(request()).To(ContainSubstring("<conversation>\n[earlier part of the conversation omitted]\n"))
		})
		It("keeps the newest part of the span", func() { Expect(request()).To(ContainSubstring("T1298 ")) })
		It("leaves out the oldest part of the span", func() { Expect(request()).NotTo(ContainSubstring("T0001 ")) })
		It("still cuts where ChooseCut says, so the omitted events are dropped from the rebuild", func() {
			Expect(result.FirstKeptSeq).To(Equal(int64(3898)))
		})
	})

	It("fails as cancelled when the context is cancelled before the append", func() {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := appendCompaction(cctx, st, sessID, CompactOptions{Lease: lease}, int64(len(rows())),
			CompactionPayload{Summary: "SUM", FirstKeptSeq: 4, Reason: CompactionReasonManual, Model: "m"})
		Expect(failureReason(err)).To(Equal(CompactReasonCancelled))
	})

	Context("with a context that reports cancellation only after the writer opened", func() {
		var appendCtx context.Context
		var payload CompactionPayload
		BeforeEach(func() {
			appendCtx = lateCancelCtx{Context: ctx}
			payload = CompactionPayload{Summary: "SUM", FirstKeptSeq: 4, Reason: CompactionReasonManual, Model: "m"}
		})

		It("fails as cancelled when the append notices it", func() {
			_, err := appendCompaction(appendCtx, st, sessID, CompactOptions{Lease: lease}, int64(len(rows())), payload)
			Expect(failureReason(err)).To(Equal(CompactReasonCancelled))
		})
		It("fails as cancelled when the fence notices it", func() {
			if err := st.ReleaseLease(ctx, lease.Item, lease.Agent, lease.Token); err != nil {
				panic(err)
			}
			_, err := appendCompaction(appendCtx, st, sessID, CompactOptions{Lease: lease}, int64(len(rows())), payload)
			Expect(failureReason(err)).To(Equal(CompactReasonCancelled))
		})
	})

	It("reports the summarising call's usage", func() {
		usage := &provider.Usage{InputTokens: 7, OutputTokens: 3}
		streamer.turns = [][]provider.StreamEvent{{provText("SUM"), {Kind: provider.StreamEventFinish, FinishReason: "stop", Usage: usage}}}
		result, err := compact(CompactOptions{})
		if err != nil {
			panic(err)
		}
		Expect(result.Usage).To(Equal(usage))
	})

	It("takes tokens_before from the last reported context", func() {
		w, err := st.NewEventWriter(ctx, sessID)
		if err != nil {
			panic(err)
		}
		_, aerr := w.Append(ctx, session.ActorEngine, TypeAssistantMessage, assistantMessagePayload("USED",
			&provider.Usage{InputTokens: 90, OutputTokens: 10}))
		_ = w.Close()
		if aerr != nil {
			panic(aerr)
		}
		result, cerr := compact(CompactOptions{})
		if cerr != nil {
			panic(cerr)
		}
		Expect(result.TokensBefore).To(Equal(100))
	})

	for _, f := range compactFailures() {
		Context("with "+f.name, func() {
			var before []store.RecordedEvent
			var err error
			BeforeEach(func() {
				streamer.turns = [][]provider.StreamEvent{f.turn}
				cctx, cancel := context.WithCancel(ctx)
				DeferCleanup(cancel)
				if f.cancels {
					streamer.hook = cancel
				}
				before = rows()
				_, err = Compact(cctx, st, sessID, CompactOptions{Streamer: streamer, Model: "m", Lease: lease, KeepRecent: 1})
			})

			It("fails with its reason", func() { Expect(failureReason(err)).To(Equal(f.reason)) })
			It("leaves the rows as they were", func() { Expect(rows()).To(Equal(before)) })
		})
	}

	It("fails with no_cut when there is nothing to compact", func() {
		other, err := store.OpenMemory()
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = other.Close() })
		onlyID := seedSession(other, false, words("ONLY")...)
		_, cerr := Compact(ctx, other, onlyID, CompactOptions{Streamer: streamer, Lease: lease, KeepRecent: 1})
		Expect(cerr).To(MatchError(ErrNoCut))
	})

	It("classifies no_cut as a usage error", func() {
		Expect(&CompactionFailure{Reason: CompactReasonNoCut}).To(MatchError(session.ErrUsage))
	})

	It("classifies a failed summary as a provider error", func() {
		Expect(&CompactionFailure{Reason: CompactReasonSummaryEmpty}).To(MatchError(ErrProvider))
	})

	Context("when the lease was stolen during the summary call", func() {
		var before []store.RecordedEvent
		var err error
		BeforeEach(func() {
			streamer.hook = func() {
				if rerr := st.ReleaseLease(ctx, lease.Item, lease.Agent, lease.Token); rerr != nil {
					panic(rerr)
				}
				if _, aerr := st.AcquireLease(ctx, lease.Item, "thief"); aerr != nil {
					panic(aerr)
				}
			}
			before = rows()
			_, err = compact(CompactOptions{})
		})

		It("fails as fenced", func() { Expect(err).To(MatchError(ErrExecLoopFenced)) })
		It("appends nothing", func() { Expect(rows()).To(Equal(before)) })
	})

	It("accepts a finish event with an empty reason as a complete summary", func() {
		streamer.turns = [][]provider.StreamEvent{{provText("SUM"), {Kind: provider.StreamEventFinish}}}
		result, err := compact(CompactOptions{})
		if err != nil {
			panic(err)
		}
		Expect(rows()[result.EventSeq].Type).To(Equal(TypeContextCompacted))
	})

	It("refuses a call without a lease", func() {
		_, err := Compact(ctx, st, sessID, CompactOptions{Streamer: streamer})
		Expect(err).To(MatchError(ErrExecLoopConfig))
	})

	Context("with instructions holding a control character", func() {
		var before []store.RecordedEvent
		var err error
		BeforeEach(func() {
			before = rows()
			_, err = compact(CompactOptions{Instructions: "a\nb"})
		})

		It("is a usage error", func() { Expect(err).To(MatchError(session.ErrUsage)) })
		It("writes nothing", func() { Expect(rows()).To(Equal(before)) })
	})

	It("fails with log_changed when another writer appended during the summary call", func() {
		streamer.hook = func() {
			w, err := st.NewEventWriter(ctx, sessID)
			if err != nil {
				panic(err)
			}
			defer func() { _ = w.Close() }()
			if _, err := w.Append(ctx, session.ActorEngine, TypeUserMessage, map[string]any{"content": "late"}); err != nil {
				panic(err)
			}
		}
		_, err := compact(CompactOptions{})
		Expect(failureReason(err)).To(Equal(CompactReasonLogChanged))
	})

	Context("a second compaction", func() {
		var first, second *CompactionResult
		BeforeEach(func() {
			var err error
			if first, err = compact(CompactOptions{}); err != nil {
				panic(err)
			}
			w, werr := st.NewEventWriter(ctx, sessID)
			if werr != nil {
				panic(werr)
			}
			for _, e := range words("DELTA", "ECHO") {
				var payload map[string]any
				if jerr := json.Unmarshal([]byte(e.PayloadJSON), &payload); jerr != nil {
					panic(jerr)
				}
				if _, aerr := w.Append(ctx, session.ActorEngine, e.Type, payload); aerr != nil {
					panic(aerr)
				}
			}
			_ = w.Close()
			streamer.reqs, streamer.turns = nil, [][]provider.StreamEvent{summaryTurn("SECOND")}
			if second, err = compact(CompactOptions{}); err != nil {
				panic(err)
			}
		})

		It("cuts strictly after the first", func() { Expect(second.FirstKeptSeq).To(BeNumerically(">", first.FirstKeptSeq)) })
		It("starts its span at the first's first_kept_seq", func() {
			Expect(request()).To(ContainSubstring("[Assistant]: CHARLIE"))
		})
		It("leaves what the first summarised out of the span", func() { Expect(request()).NotTo(ContainSubstring("BRAVO")) })
		It("passes the previous summary as iterative context", func() {
			Expect(request()).To(ContainSubstring("<previous-summary>\nSUM\n</previous-summary>"))
		})
	})
})

var _ = Describe("CompactSession", func() {
	var (
		st       *store.Store
		sessID   string
		streamer *execLoopStreamer
		ctx      context.Context
	)

	rows := func() []store.RecordedEvent {
		events, err := st.Replay(ctx, sessID)
		if err != nil {
			panic(err)
		}
		return events
	}
	seed := func(paused bool, events ...store.RecordedEvent) { sessID = seedSession(st, paused, events...) }
	status := func() string {
		sess, err := st.GetSession(ctx, sessID)
		if err != nil {
			panic(err)
		}
		return sess.Status
	}
	run := func() (*CompactionResult, error) {
		return CompactSession(ctx, st, sessID, CompactOptions{Streamer: streamer, KeepRecent: 1})
	}

	BeforeEach(func() {
		var err error
		ctx = context.Background()
		if st, err = store.OpenMemory(); err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = st.Close() })
		streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{summaryTurn("SUM")}}
	})

	Context("a paused session", func() {
		BeforeEach(func() {
			seed(true, words("ALPHA", "BRAVO", "CHARLIE")...)
			if _, err := run(); err != nil {
				panic(err)
			}
		})

		It("stays paused", func() { Expect(status()).To(Equal("paused")) })
		It("compacts with the session's own model", func() { Expect(streamer.reqs[0].Model).To(Equal("scripted-model")) })
		It("releases the lease", func() { Expect(st.LeaseToken(ctx, "exec:"+sessID)).To(Equal(int64(0))) })
	})

	It("recovers a crashed session before it cuts", func() {
		seed(false, saidEvent("go"), cutDelegate("d1"), saidEvent("ALPHA"), callEvent("c1"), resultEvent("c1"), saidEvent("BRAVO"),
			callEvent("c2"), resultEvent("c2"))
		if _, err := run(); err != nil {
			panic(err)
		}
		types := []string{}
		for _, e := range rows() {
			types = append(types, e.Type)
		}
		Expect(types[len(types)-2:]).To(Equal([]string{"session.interrupted", TypeContextCompacted}))
	})

	Context("a terminal session", func() {
		var before []store.RecordedEvent
		var err error
		BeforeEach(func() {
			seed(true, words("ALPHA", "BRAVO")...)
			if uerr := st.UpdateSessionStatus(ctx, sessID, "completed"); uerr != nil {
				panic(uerr)
			}
			before = rows()
			_, err = run()
		})

		It("is refused as terminal", func() { Expect(err).To(MatchError(session.ErrTerminal)) })
		It("writes nothing", func() { Expect(rows()).To(Equal(before)) })
	})

	It("refuses an unknown session", func() {
		_, err := CompactSession(ctx, st, "nope", CompactOptions{Streamer: streamer})
		Expect(err).To(MatchError(store.ErrUnknownSession))
	})

	Context("while an exec lease row exists", func() {
		for _, owner := range []string{"cli", "another-run", "delegate:x"} {
			Context("held by "+owner, func() {
				var before []store.RecordedEvent
				var token int64
				var err error
				BeforeEach(func() {
					seed(true, words("ALPHA", "BRAVO")...)
					var aerr error
					if token, aerr = st.AcquireLease(ctx, "exec:"+sessID, owner); aerr != nil {
						panic(aerr)
					}
					before = rows()
					_, err = run()
				})

				It("is refused as fenced", func() { Expect(err).To(MatchError(ErrExecLoopFenced)) })
				It("writes nothing", func() { Expect(rows()).To(Equal(before)) })
				It("leaves the row", func() { Expect(st.FenceLease(ctx, "exec:"+sessID, owner, token)).To(Succeed()) })
			})
		}
	})

	Context("a leftover compact row written by the legacy acquire", func() {
		var before []store.RecordedEvent
		var token int64
		var err error
		BeforeEach(func() {
			seed(true, words("ALPHA", "BRAVO", "CHARLIE")...)
			token = must(st.AcquireLease(ctx, "exec:"+sessID, compactLeasePrefix+"dead"))
			before = rows()
			_, err = run()
		})

		// ADR-011 packet 12: the row used to be cleared silently; it records no machine, so it cannot be judged.
		It("is refused as unjudgeable", func() { Expect(err).To(MatchError(ContainSubstring("(a row that cannot be judged"))) })
		It("names the unlock command", func() {
			Expect(err).To(MatchError(ContainSubstring("`flowstate session unlock " + sessID + " --force`")))
		})
		It("names the owner", func() { Expect(err).To(MatchError(ContainSubstring(compactLeasePrefix + "dead"))) })
		It("is refused as fenced, as every held lease", func() { Expect(err).To(MatchError(ErrExecLoopFenced)) })
		It("writes nothing", func() { Expect(rows()).To(Equal(before)) })
		It("leaves the row in place", func() { Expect(st.LeaseToken(ctx, "exec:"+sessID)).To(Equal(token)) })
	})

	Context("a live holder of the lease", func() {
		var before []store.RecordedEvent
		var held *store.RunLease
		var err error
		BeforeEach(func() {
			seed(true, words("ALPHA", "BRAVO", "CHARLIE")...)
			held = must(st.AcquireRunLease(ctx, "exec:"+sessID, "run:live"))
			DeferCleanup(func() { _ = held.Release(ctx) })
			before = rows()
			_, err = run()
		})

		It("is refused, naming the holder", func() { Expect(err).To(MatchError(ContainSubstring("run:live"))) })
		It("is not called unjudgeable", func() { Expect(err).NotTo(MatchError(ContainSubstring("a row that cannot be judged"))) })
		It("does not suggest unlock --force", func() { Expect(err).NotTo(MatchError(ContainSubstring("--force"))) })
		It("says the holder is running", func() { Expect(err).To(MatchError(ContainSubstring("the holder is running"))) })
		It("leaves the log byte-identical", func() { Expect(rows()).To(Equal(before)) })
		It("leaves the holder's lease valid", func() { Expect(held.Fence(ctx)).To(Succeed()) })
	})

	Context("a row of another machine", func() {
		var err error
		BeforeEach(func() {
			seed(true, words("ALPHA", "BRAVO")...)
			seedLeaseRow(st, sessID, "run:away", 3, "other-machine", "otherhost", 4321)
			_, err = run()
		})

		It("names the host of the holder", func() { Expect(err).To(MatchError(ContainSubstring("host otherhost"))) })
		It("names the pid of the holder", func() { Expect(err).To(MatchError(ContainSubstring("pid 4321"))) })
		It("names when the holder took it", func() {
			Expect(err).To(MatchError(ContainSubstring("since 2026-01-02T03:04:05.000Z")))
		})
	})

	for _, owner := range []string{compactLeasePrefix + "dead", "run:dead"} {
		Context("a crashed judged row of this machine, held by "+owner, func() {
			var types []string
			var duringToken int64
			BeforeEach(func() {
				seed(true, words("ALPHA", "BRAVO", "CHARLIE")...)
				seedLeaseRow(st, sessID, owner, 4, store.MachineIdentity(), "deadhost", 1)
				hooked := &hookStreamer{execLoopStreamer: execLoopStreamer{turns: [][]provider.StreamEvent{summaryTurn("SUM")}}}
				hooked.hook = func() { duringToken = must(st.LeaseToken(ctx, "exec:"+sessID)) }
				must(CompactSession(ctx, st, sessID, CompactOptions{Streamer: hooked, KeepRecent: 1}))
				types = nil
				for _, e := range rows() {
					types = append(types, e.Type)
				}
			})

			It("is taken over and the compaction is written", func() { Expect(types).To(ContainElement(TypeContextCompacted)) })
			It("is taken over with the next token", func() { Expect(duringToken).To(Equal(int64(5))) })
			It("leaves no lease row after", func() { Expect(st.LeaseToken(ctx, "exec:"+sessID)).To(Equal(int64(0))) })
		})
	}

	Context("while it summarises", func() {
		var probe error
		BeforeEach(func() {
			seed(true, words("ALPHA", "BRAVO", "CHARLIE")...)
			hooked := &hookStreamer{execLoopStreamer: execLoopStreamer{turns: [][]provider.StreamEvent{summaryTurn("SUM")}}}
			hooked.hook = func() {
				if other, err := st.AcquireRunLease(ctx, "exec:"+sessID, "run:other"); err != nil {
					probe = err
				} else {
					_ = other.Release(ctx)
				}
			}
			must(CompactSession(ctx, st, sessID, CompactOptions{Streamer: hooked, KeepRecent: 1}))
		})

		It("holds the lease, so a second run is refused", func() { Expect(probe).To(MatchError(store.ErrSessionBusy)) })
	})

	Context("a compaction whose row was taken from it during the summary", func() {
		var before []store.RecordedEvent
		var err error
		BeforeEach(func() {
			seed(true, words("ALPHA", "BRAVO", "CHARLIE")...)
			hooked := &hookStreamer{execLoopStreamer: execLoopStreamer{turns: [][]provider.StreamEvent{summaryTurn("SUM")}}}
			hooked.hook = func() {
				check(st.ClearLeaseOwnedBy(ctx, "exec:"+sessID, compactLeasePrefix))
				must(st.AcquireLease(ctx, "exec:"+sessID, "cli"))
			}
			before = rows()
			_, err = CompactSession(ctx, st, sessID, CompactOptions{Streamer: hooked, KeepRecent: 1})
		})

		It("fails as fenced", func() { Expect(err).To(MatchError(ErrExecLoopFenced)) })
		It("appends nothing", func() { Expect(rows()).To(Equal(before)) })
	})

	Context("a session held by a foreign run whose log shows a crash", func() {
		var types []string
		BeforeEach(func() {
			seed(false, words("ALPHA", "BRAVO")...)
			must(st.AcquireLease(ctx, "exec:"+sessID, "cli"))
			_, _ = run()
			types = nil
			for _, e := range rows() {
				types = append(types, e.Type)
			}
		})

		It("is refused before recovery writes its marker", func() { Expect(types).NotTo(ContainElement("session.interrupted")) })
	})

	Context("the refusal of a legacy row", func() {
		var err error
		BeforeEach(func() {
			seed(true, words("ALPHA", "BRAVO")...)
			must(st.AcquireLease(ctx, "exec:"+sessID, "cli"))
			_, err = run()
		})

		It("says which session is in use", func() { Expect(err).To(MatchError(ContainSubstring("session " + sessID + " is in use"))) })
		It("says who holds it", func() { Expect(err).To(MatchError(ContainSubstring("lease held by cli"))) })
		It("says what takes over a crashed holder", func() {
			Expect(err).To(MatchError(ContainSubstring("`session run`, `session compact` or `tool run`")))
		})
		It("names the unlock command", func() {
			Expect(err).To(MatchError(ContainSubstring("`flowstate session unlock " + sessID + " --force`")))
		})
	})

	Context("a failed compaction", func() {
		BeforeEach(func() {
			seed(true, words("ALPHA", "BRAVO", "CHARLIE")...)
			streamer.turns = [][]provider.StreamEvent{{provFinish()}}
			_, _ = run()
		})

		It("releases its own lease", func() { Expect(st.LeaseToken(ctx, "exec:"+sessID)).To(Equal(int64(0))) })
	})

	Context("a crashed session whose lease row is gone, when the summary fails", func() {
		var types []string
		BeforeEach(func() {
			seed(false, saidEvent("go"), cutDelegate("d1"), saidEvent("ALPHA"), callEvent("c1"), resultEvent("c1"),
				saidEvent("BRAVO"), callEvent("c2"), resultEvent("c2"))
			streamer.turns = [][]provider.StreamEvent{{provFinish()}}
			_, _ = run()
			types = nil
			for _, e := range rows() {
				types = append(types, e.Type)
			}
		})

		It("keeps the recovery marker", func() { Expect(types).To(ContainElement("session.interrupted")) })
		It("writes no compaction event", func() { Expect(types).NotTo(ContainElement(TypeContextCompacted)) })
	})
})

var _ = DescribeTable("a compaction failure's error code and message",
	func(reason string, cause error, code string) {
		Expect(ErrorCode(failCompaction(reason, cause))).To(Equal(code))
	},
	Entry("summary_invalid is a provider-class failure, not a corrupt log", CompactReasonSummaryInvalid,
		ErrInvalidCompaction, CodeProvider),
	Entry("provider_error", CompactReasonProviderError, errors.New("boom"), CodeProvider),
	Entry("log_changed is internal", CompactReasonLogChanged, errors.New("grew"), CodeInternal),
	Entry("no_cut is usage", CompactReasonNoCut, ErrNoCut, CodeUsage),
)

var _ = Describe("a log_changed failure's message", func() {
	It("says nothing was written and what to do", func() {
		Expect(failCompaction(CompactReasonLogChanged, errors.New("the log grew from 3 to 4 events"))).To(
			MatchError("the log changed during the summary; nothing was written; run compact again"))
	})
})

var _ = Describe("a no_cut failure's message", func() {
	It("names the reason once", func() {
		Expect(failCompaction(CompactReasonNoCut, ErrNoCut).Error()).To(Equal("compaction failed: no_cut: nothing new to compact"))
	})
})

var _ = Describe("crash recovery of a leftover lease row", func() {
	var ctx context.Context
	var st *store.Store
	var item, sessionID string
	BeforeEach(func() {
		ctx = context.Background()
		st = mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		item = "exec:" + sess.ID
		sessionID = sess.ID
	})

	// ADR-011 packet 12: recovery no longer touches lease rows; a legacy compact: row records no machine.
	It("leaves the legacy row of a crashed compaction in place", func() {
		token := must(st.AcquireLease(ctx, item, compactLeasePrefix+"dead"))
		check(RecoverCrashedSession(ctx, st, sessionID))
		Expect(st.LeaseToken(ctx, item)).To(Equal(token))
	})

	It("leaves a run that wants the lease refused by that row, as unjudgeable", func() {
		must(st.AcquireLease(ctx, item, compactLeasePrefix+"dead"))
		check(RecoverCrashedSession(ctx, st, sessionID))
		_, err := st.AcquireRunLease(ctx, item, "run:next")
		Expect(err).To(MatchError(store.ErrSessionBusy))
	})

	for _, owner := range []string{"cli", "run:other"} {
		It("keeps the row of "+owner+", which may be a live run", func() {
			token := must(st.AcquireLease(ctx, item, owner))
			check(RecoverCrashedSession(ctx, st, sessionID))
			Expect(st.LeaseToken(ctx, item)).To(Equal(token))
		})
	}
})
