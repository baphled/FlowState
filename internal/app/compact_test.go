package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

func (h *hookStreamer) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	if h.hook != nil {
		h.hook()
	}
	return h.execLoopStreamer.Stream(ctx, req)
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
		Expect(compactRequestText(events, 7, nil, "")).To(ContainSubstring("[truncated: 3002 more characters]"))
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
