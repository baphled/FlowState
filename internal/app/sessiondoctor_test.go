package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// mustCreateCrashChild composes a delegated child session under
// parent, panicking on errors, so steering scope fixtures have a
// chain-eligible sender.
func mustCreateCrashChild(ctx context.Context, st *store.Store, parentID string) *store.Session {
	sess, err := CreateChildSession(ctx, st, parentID, "probe.ai", "probe-model", DefaultHarness, "", "child task")
	if err != nil {
		panic(err)
	}
	return sess
}

var _ = Describe("DiagnoseSession", func() {
	var ctx context.Context
	BeforeEach(func() {
		ctx = context.Background()
	})

	It("flags a crashed session with a dangling turn and run activity", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "user", "turn.start", map[string]any{"task": "x"})
		mustAppendEvent(ctx, st, sess.ID, "engine", "provider.request", map[string]any{})
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Crashed).To(BeTrue())
	})

	It("flags a crashed session whose only run activity is a content delta", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "user", "turn.start", map[string]any{"task": "x"})
		mustAppendEvent(ctx, st, sess.ID, "user", TypeUserMessage, map[string]any{"content": "x"})
		mustAppendEvent(ctx, st, sess.ID, "engine", TypeAssistantDelta, map[string]any{"delta": "par"})
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Crashed).To(BeTrue())
	})

	It("reports a session with a non-terminal provider.error as active, not failed", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "engine", TypeProviderError,
			map[string]any{"error": "x", "reason": ReasonContextOverflow, "terminal": false})
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Status).To(Equal("active"))
	})

	It("keeps a fresh never-run session un-crashed", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Crashed).To(BeFalse())
	})

	It("reports run activity for a session with provider traffic", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "engine", "provider.request", map[string]any{})
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.RunActivity).To(BeTrue())
	})

	It("keeps run activity false for a fresh session", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.RunActivity).To(BeFalse())
	})

	It("keeps a completed turn un-crashed", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "engine", "provider.request", map[string]any{})
		mustAppendEvent(ctx, st, sess.ID, "engine", "turn.end", map[string]any{"status": "completed"})
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Crashed).To(BeFalse())
	})

	It("keeps a paused session un-crashed", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "engine", "provider.request", map[string]any{})
		mustAppendEvent(ctx, st, sess.ID, "engine", "session.paused", map[string]any{"reason": "budget_exceeded"})
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Crashed).To(BeFalse())
	})

	It("surfaces the raw active row status", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Status).To(Equal("active"))
	})

	It("surfaces the raw cancelled row status for a terminal session", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		if _, cerr := CancelSession(ctx, st, sess.ID); cerr != nil {
			panic(cerr)
		}
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Status).To(Equal("cancelled"))
	})

	It("echoes the session id", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.SessionID).To(Equal(sess.ID))
	})

	It("passes the unknown-session sentinel through unchanged", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		_, err := DiagnoseSession(ctx, st, "no-such-session")
		Expect(errors.Is(err, store.ErrUnknownSession)).To(BeTrue())
	})

	It("carries the reserved total of the budget snapshot", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		if perr := st.SetBudgetPool(ctx, sess.ID, 100); perr != nil {
			panic(perr)
		}
		if _, rerr := st.ReserveBudget(ctx, sess.ID, "doctor-child", 30); rerr != nil {
			panic(rerr)
		}
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Reserved).To(Equal(int64(30)))
	})

	It("carries the pool anchor of the budget snapshot", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		if perr := st.SetBudgetPool(ctx, sess.ID, 100); perr != nil {
			panic(perr)
		}
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		pool := int64(100)
		Expect(d.Pool).To(Equal(&pool))
	})

	It("carries the available remainder of the budget snapshot", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		if perr := st.SetBudgetPool(ctx, sess.ID, 100); perr != nil {
			panic(perr)
		}
		if _, rerr := st.ReserveBudget(ctx, sess.ID, "doctor-child", 30); rerr != nil {
			panic(rerr)
		}
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		available := int64(70)
		Expect(d.Available).To(Equal(&available))
	})

	It("keeps the pool anchor null without a declared budget pool", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Pool).To(BeNil())
	})

	It("keeps the available remainder null without a declared budget pool", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess := mustCreateCrashSession(ctx, st)
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.Available).To(BeNil())
	})

	It("counts pending steering messages before the drain", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		parent := mustCreateCrashSession(ctx, st)
		child := mustCreateCrashChild(ctx, st, parent.ID)
		if _, serr := st.SendMessage(ctx, child.ID, parent.ID, "steer left"); serr != nil {
			panic(serr)
		}
		d, err := DiagnoseSession(ctx, st, child.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.PendingMessages).To(Equal(1))
	})

	It("counts zero pending steering messages after the drain", func() {
		st := mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		parent := mustCreateCrashSession(ctx, st)
		child := mustCreateCrashChild(ctx, st, parent.ID)
		if _, serr := st.SendMessage(ctx, child.ID, parent.ID, "steer left"); serr != nil {
			panic(serr)
		}
		if _, derr := DrainSteeringMessages(ctx, st, child.ID); derr != nil {
			panic(derr)
		}
		d, err := DiagnoseSession(ctx, st, child.ID)
		if err != nil {
			panic(err)
		}
		Expect(d.PendingMessages).To(Equal(0))
	})
})

var _ = Describe("session doctor run activity", func() {
	It("counts a content delta as run activity", func() {
		ctx := context.Background()
		st := mustOpenCrashStore()
		defer st.Close()
		sess := mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "engine", TypeAssistantDelta, map[string]any{"delta": "c"})
		d, err := DiagnoseSession(ctx, st, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(d.RunActivity).To(BeTrue())
	})
})

// mustTurnEnd appends a completed turn.end carrying reason (omitted
// when empty), panicking on errors.
func mustTurnEnd(ctx context.Context, st *store.Store, id, reason string) {
	payload := map[string]any{"status": "completed"}
	if reason != "" {
		payload["finish_reason"] = reason
	}
	mustAppendEvent(ctx, st, id, "engine", "turn.end", payload)
}

// mustAppendRawTurnEnd appends a completed turn.end whose finish_reason
// is the given raw JSON value, panicking on errors.
func mustAppendRawTurnEnd(ctx context.Context, st *store.Store, id, raw string) {
	mustAppendEvent(ctx, st, id, "engine", "turn.end", map[string]any{
		"status":        "completed",
		"finish_reason": json.RawMessage(raw),
	})
}

var _ = Describe("session doctor truncated turn", func() {
	var ctx context.Context
	var st *store.Store
	var sess *store.Session
	BeforeEach(func() {
		ctx = context.Background()
		st = mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess = mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "engine", "provider.request", map[string]any{})
	})

	diagnose := func() SessionDiagnosis {
		d, err := DiagnoseSession(ctx, st, sess.ID)
		if err != nil {
			panic(err)
		}
		return d
	}

	It("reports the finish reason of the last turn.end", func() {
		mustTurnEnd(ctx, st, sess.ID, "length")
		Expect(diagnose().FinishReason).To(Equal("length"))
	})

	It("flags a length-terminated final turn as truncated", func() {
		mustTurnEnd(ctx, st, sess.ID, "length")
		Expect(diagnose().Truncated).To(BeTrue())
	})

	It("does not flag a stop-terminated turn as truncated", func() {
		mustTurnEnd(ctx, st, sess.ID, "stop")
		Expect(diagnose().Truncated).To(BeFalse())
	})

	It("does not flag a tool_calls-terminated turn as truncated", func() {
		mustTurnEnd(ctx, st, sess.ID, "tool_calls")
		Expect(diagnose().Truncated).To(BeFalse())
	})

	It("clears truncation when a later turn ends with stop", func() {
		mustTurnEnd(ctx, st, sess.ID, "length")
		mustTurnEnd(ctx, st, sess.ID, "stop")
		Expect(diagnose().Truncated).To(BeFalse())
	})

	It("reports the later finish reason when a later turn ends with stop", func() {
		mustTurnEnd(ctx, st, sess.ID, "length")
		mustTurnEnd(ctx, st, sess.ID, "stop")
		Expect(diagnose().FinishReason).To(Equal("stop"))
	})

	It("flags truncation from the last turn.end when an earlier one was stop", func() {
		mustTurnEnd(ctx, st, sess.ID, "stop")
		mustTurnEnd(ctx, st, sess.ID, "length")
		Expect(diagnose().Truncated).To(BeTrue())
	})

	It("clears truncation when a later turn.end carries no finish reason", func() {
		mustTurnEnd(ctx, st, sess.ID, "length")
		mustTurnEnd(ctx, st, sess.ID, "")
		Expect(diagnose().Truncated).To(BeFalse())
	})

	It("reports no finish reason without a turn.end", func() {
		Expect(diagnose().FinishReason).To(BeEmpty())
	})

	It("reports no finish reason for a turn.end without one", func() {
		mustTurnEnd(ctx, st, sess.ID, "")
		Expect(diagnose().FinishReason).To(BeEmpty())
	})

	It("ignores a non-object turn.end payload", func() {
		mustTurnEnd(ctx, st, sess.ID, "length")
		w, err := st.NewEventWriter(ctx, sess.ID)
		if err != nil {
			panic(err)
		}
		defer w.Close()
		if _, aerr := w.Append(ctx, "engine", "turn.end", "not-an-object"); aerr != nil {
			panic(aerr)
		}
		Expect(diagnose().Truncated).To(BeFalse())
	})

	It("does not fail on a non-object turn.end payload", func() {
		w, err := st.NewEventWriter(ctx, sess.ID)
		if err != nil {
			panic(err)
		}
		defer w.Close()
		if _, aerr := w.Append(ctx, "engine", "turn.end", []string{"x"}); aerr != nil {
			panic(aerr)
		}
		_, derr := DiagnoseSession(ctx, st, sess.ID)
		Expect(derr).NotTo(HaveOccurred())
	})

	DescribeTable("ignores a turn.end whose finish_reason is not a string",
		func(raw string) {
			mustAppendRawTurnEnd(ctx, st, sess.ID, raw)
			d, err := DiagnoseSession(ctx, st, sess.ID)
			if err != nil {
				panic(err)
			}
			Expect(d.FinishReason).To(BeEmpty())
		},
		Entry("number", "42"),
		Entry("null", "null"),
		Entry("object", `{"x":"length"}`),
	)

	DescribeTable("does not flag truncation for a turn.end whose finish_reason is not a string",
		func(raw string) {
			mustAppendRawTurnEnd(ctx, st, sess.ID, raw)
			Expect(diagnose().Truncated).To(BeFalse())
		},
		Entry("number", "42"),
		Entry("null", "null"),
		Entry("object", `{"x":"length"}`),
	)

	DescribeTable("does not fail on a turn.end whose finish_reason is not a string",
		func(raw string) {
			mustAppendRawTurnEnd(ctx, st, sess.ID, raw)
			_, derr := DiagnoseSession(ctx, st, sess.ID)
			Expect(derr).NotTo(HaveOccurred())
		},
		Entry("number", "42"),
		Entry("null", "null"),
		Entry("object", `{"x":"length"}`),
	)

	It("does not flag a budget_exhausted loop stop as truncated", func() {
		mustTurnEnd(ctx, st, sess.ID, "length")
		mustAppendEvent(ctx, st, sess.ID, "engine", "turn.end", map[string]any{"status": "budget_exhausted"})
		Expect(diagnose().Truncated).To(BeFalse())
	})

	It("reports no finish reason for a budget_exhausted loop stop", func() {
		mustAppendEvent(ctx, st, sess.ID, "engine", "turn.end", map[string]any{"status": "budget_exhausted"})
		Expect(diagnose().FinishReason).To(BeEmpty())
	})

	DescribeTable("does not flag a session whose row status is not resumable-or-done",
		func(status string) {
			mustTurnEnd(ctx, st, sess.ID, "length")
			if err := st.UpdateSessionStatus(ctx, sess.ID, status); err != nil {
				panic(err)
			}
			Expect(diagnose().Truncated).To(BeFalse())
		},
		Entry("failed", "failed"),
		Entry("cancelled", "cancelled"),
		Entry("paused", "paused"),
	)

	It("does not flag a session that crashed after a truncated turn", func() {
		mustTurnEnd(ctx, st, sess.ID, "length")
		mustAppendEvent(ctx, st, sess.ID, "user", "turn.start", map[string]any{"task": "again"})
		Expect(diagnose().Truncated).To(BeFalse())
	})

	It("flags a completed row status with a length turn as truncated", func() {
		mustTurnEnd(ctx, st, sess.ID, "length")
		if err := st.UpdateSessionStatus(ctx, sess.ID, "completed"); err != nil {
			panic(err)
		}
		Expect(diagnose().Truncated).To(BeTrue())
	})
})

var _ = Describe("session doctor compactions", func() {
	var (
		ctx      context.Context
		st       *store.Store
		sess     *store.Session
		diagnose func() SessionDiagnosis
	)
	BeforeEach(func() {
		ctx = context.Background()
		st = mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
		sess = mustCreateCrashSession(ctx, st)
		mustAppendEvent(ctx, st, sess.ID, "user", "turn.start", map[string]any{"task": "x"})
		diagnose = func() SessionDiagnosis {
			d, err := DiagnoseSession(ctx, st, sess.ID)
			if err != nil {
				panic(err)
			}
			return d
		}
	})
	compact := func(tokensAfter int, usage *provider.Usage) {
		p := validCompaction()
		p.FirstKeptSeq, p.TokensAfter, p.Usage = 1, tokensAfter, usage
		mustAppendCompaction(ctx, st, sess.ID, p)
	}

	It("reports no compactions for a log without one", func() {
		Expect(diagnose().Compactions).To(Equal(CompactionStats{}))
	})

	It("counts the compaction events", func() {
		mustAppendEvent(ctx, st, sess.ID, "engine", TypeAssistantMessage, map[string]any{"content": "a"})
		compact(300, nil)
		compact(200, nil)
		Expect(diagnose().Compactions.Count).To(Equal(int64(2)))
	})

	It("reports the tokens_after of the last compaction", func() {
		compact(300, nil)
		compact(200, nil)
		Expect(*diagnose().Compactions.LastTokensAfter).To(Equal(int64(200)))
	})

	It("totals the summarising calls' usage separately", func() {
		compact(300, &provider.Usage{InputTokens: 100, OutputTokens: 10})
		compact(200, &provider.Usage{InputTokens: 50, OutputTokens: 5})
		got := diagnose().Compactions
		Expect([]int64{got.SummariesReported, got.SummaryInputTokens, got.SummaryOutputTokens}).To(Equal([]int64{2, 150, 15}))
	})

	It("keeps a compaction's usage out of the turn usage", func() {
		compact(300, &provider.Usage{InputTokens: 100, OutputTokens: 10})
		Expect(diagnose().Usage).To(Equal(SessionUsage{}))
	})

	It("does not count a malformed compaction event as a compaction", func() {
		mustAppendEvent(ctx, st, sess.ID, "engine", TypeContextCompacted, map[string]any{"summary": ""})
		Expect(diagnose().Compactions.Count).To(Equal(int64(0)))
	})

	It("counts only the valid compactions in Count when an invalid one is present", func() {
		mustAppendEvent(ctx, st, sess.ID, "engine", TypeContextCompacted, map[string]any{"summary": ""})
		compact(300, nil)
		Expect(diagnose().Compactions.Count).To(Equal(int64(1)))
	})

	It("does not count a valid compaction as invalid", func() {
		compact(300, nil)
		Expect(diagnose().Compactions.Invalid).To(Equal(int64(0)))
	})

	DescribeTable("counts a malformed compaction event as invalid",
		func(payload map[string]any) {
			mustAppendEvent(ctx, st, sess.ID, "engine", TypeContextCompacted, payload)
			Expect(diagnose().Compactions.Invalid).To(Equal(int64(1)))
		},
		Entry("a payload that does not decode", map[string]any{"summary": 5}),
		Entry("an empty summary", map[string]any{"summary": "", "first_kept_seq": 1, "reason": "auto"}),
		Entry("a first_kept_seq at its own seq or later",
			map[string]any{"summary": "S", "first_kept_seq": 99, "reason": "auto"}),
	)

	It("counts a compaction whose first_kept_seq is below the previous one's as invalid", func() {
		mustAppendEvent(ctx, st, sess.ID, "engine", TypeAssistantMessage, map[string]any{"content": "a"})
		first := validCompaction()
		first.FirstKeptSeq = 2
		mustAppendCompaction(ctx, st, sess.ID, first)
		compact(300, nil) // first_kept_seq 1, below 2
		Expect(diagnose().Compactions.Invalid).To(Equal(int64(1)))
	})

	It("does not make a compaction run activity", func() {
		compact(300, nil)
		Expect(diagnose().RunActivity).To(BeFalse())
	})

	It("does not make a fresh session with a compaction crashed", func() {
		compact(300, nil)
		Expect(diagnose().Crashed).To(BeFalse())
	})

	It("does not flag a dangling turn after a compaction as crashed without run activity", func() {
		compact(300, nil)
		mustAppendEvent(ctx, st, sess.ID, "user", "turn.start", map[string]any{"task": "again"})
		Expect(crashedFromEvents(replayOrPanic(ctx, st, sess.ID))).To(BeFalse())
	})

	It("ignores a compaction event in hasRunActivity", func() {
		Expect(hasRunActivity([]store.RecordedEvent{{Type: TypeContextCompacted}})).To(BeFalse())
	})
})

// replayOrPanic replays the session's log, panicking on errors.
func replayOrPanic(ctx context.Context, st *store.Store, id string) []store.RecordedEvent {
	events, err := st.Replay(ctx, id)
	if err != nil {
		panic(err)
	}
	return events
}

// hookLogEvent is one event of a hookDoctorSession log.
type hookLogEvent struct {
	typ     string
	payload any
}

// hookEvent is one hookLogEvent.
func hookEvent(typ string, payload map[string]any) hookLogEvent {
	return hookLogEvent{typ: typ, payload: payload}
}

// hookDoctorSession creates a session whose log holds the given events (actor engine), so a doctor
// spec starts from a known hook log; it panics on setup errors. The second result diagnoses the session
// and returns its hook summary, panicking when the doctor reports none.
func hookDoctorSession(ctx context.Context, st *store.Store, events ...hookLogEvent) (*store.Session, func() HookStats) {
	sess := mustCreateCrashSession(ctx, st)
	w, err := st.NewEventWriter(ctx, sess.ID)
	if err != nil {
		panic(err)
	}
	for _, ev := range events {
		if _, aerr := w.Append(ctx, "engine", ev.typ, ev.payload); aerr != nil {
			panic(aerr)
		}
	}
	if cerr := w.Close(); cerr != nil {
		panic(cerr)
	}
	diagnose := func() HookStats {
		d, derr := DiagnoseSession(ctx, st, sess.ID)
		if derr != nil {
			panic(derr)
		}
		if d.Hooks == nil {
			panic("the doctor reported no hooks for a log with hook events")
		}
		return *d.Hooks
	}
	return sess, diagnose
}

// loadedPayload is a hooks.loaded payload with one user hook and one project hook.
func loadedPayload(skipped []map[string]any, inert []string) map[string]any {
	return map[string]any{"scope": "both", "set_sha256": "abc123", "ids": []string{"guard", "lint"},
		"hooks":   []map[string]any{{"id": "guard", "source": "user"}, {"id": "lint", "source": "project"}},
		"skipped": skipped, "inert": inert}
}

// resultPayload is a hook.result payload for hook id at point with the given decision.
func resultPayload(point, id, decision string) map[string]any {
	return map[string]any{"point": point, "id": id, "decision": decision, "duration_ms": 1, "exit_code": 0,
		"stdout": "", "stderr": ""}
}

var _ = Describe("session doctor hooks", func() {
	var (
		ctx context.Context
		st  *store.Store
	)
	BeforeEach(func() {
		ctx = context.Background()
		st = mustOpenCrashStore()
		DeferCleanup(func() { _ = st.Close() })
	})

	Describe("a log without hook events", func() {
		It("reports no hooks object", func() {
			sess := mustCreateCrashSession(ctx, st)
			mustAppendEvent(ctx, st, sess.ID, "engine", "provider.request", map[string]any{})
			d, err := DiagnoseSession(ctx, st, sess.ID)
			if err != nil {
				panic(err)
			}
			Expect(d.Hooks).To(BeNil())
		})

		It("still reports the rest of the diagnosis", func() {
			sess := mustCreateCrashSession(ctx, st)
			mustAppendEvent(ctx, st, sess.ID, "engine", "provider.request", map[string]any{})
			d, err := DiagnoseSession(ctx, st, sess.ID)
			if err != nil {
				panic(err)
			}
			Expect(d.RunActivity).To(BeTrue())
		})
	})

	Describe("the last hooks.loaded", func() {
		It("reports the scope, the set digest and the ids by source", func() {
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHooksLoaded, loadedPayload(nil, nil)))
			Expect(diagnose().Loaded).To(Equal(&HookLoaded{Scope: "both", SetSHA256: "abc123",
				IDs: map[string][]string{"user": {"guard"}, "project": {"lint"}}, Skipped: []HookSkipped{}, Inert: []string{}}))
		})

		It("reports the latest set when a resumed run loaded another", func() {
			second := loadedPayload(nil, nil)
			second["set_sha256"] = "def456"
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHooksLoaded, loadedPayload(nil, nil)),
				hookEvent(TypeHooksLoaded, second))
			Expect(diagnose().Loaded.SetSHA256).To(Equal("def456"))
		})

		It("lists the skipped ids with their reasons", func() {
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHooksLoaded,
				loadedPayload([]map[string]any{{"id": "prof", "reason": "scope"}}, nil)))
			Expect(diagnose().Loaded.Skipped).To(Equal([]HookSkipped{{ID: "prof", Reason: "scope"}}))
		})

		It("lists the inert ids", func() {
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHooksLoaded, loadedPayload(nil, []string{"late"})))
			Expect(diagnose().Loaded.Inert).To(Equal([]string{"late"}))
		})

		It("keeps loaded nil when the log records none, as for a child", func() {
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, resultPayload("tool.before", "guard", "allow")))
			Expect(diagnose().Loaded).To(BeNil())
		})
	})

	Describe("unmatched invocations", func() {
		It("lists an invocation no result followed, with its point, id and seq", func() {
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookInvoked,
				map[string]any{"point": "tool.before", "id": "guard", "kind": "command"}))
			Expect(diagnose().Unmatched).To(Equal([]HookInvocation{{Point: "tool.before", ID: "guard", Seq: 1}}))
		})

		It("does not list an invocation a result followed", func() {
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookInvoked, map[string]any{"point": "tool.before", "id": "guard"}),
				hookEvent(TypeHookResult, resultPayload("tool.before", "guard", "allow")))
			Expect(diagnose().Unmatched).To(BeEmpty())
		})

		It("does not pair a result with another hook's invocation", func() {
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookInvoked, map[string]any{"point": "tool.before", "id": "guard"}),
				hookEvent(TypeHookResult, resultPayload("tool.before", "other", "allow")))
			Expect(diagnose().Unmatched).To(HaveLen(1))
		})

		It("lists a superseded invocation and the open ones in log order", func() {
			_, diagnose := hookDoctorSession(ctx, st,
				hookEvent(TypeHookInvoked, map[string]any{"point": "tool.before", "id": "guard"}),
				hookEvent(TypeHookInvoked, map[string]any{"point": "tool.before", "id": "guard"}),
				hookEvent(TypeHookResult, resultPayload("tool.before", "guard", "allow")),
				hookEvent(TypeHookInvoked, map[string]any{"point": "turn.after", "id": "late"}),
				hookEvent(TypeHookInvoked, map[string]any{"point": "tool.after", "id": "other"}))
			Expect(diagnose().Unmatched).To(Equal([]HookInvocation{{Point: "tool.before", ID: "guard", Seq: 1},
				{Point: "turn.after", ID: "late", Seq: 4}, {Point: "tool.after", ID: "other", Seq: 5}}))
		})

		It("lists superseded invocations in log order whatever the order they were superseded in", func() {
			_, diagnose := hookDoctorSession(ctx, st,
				hookEvent(TypeHookInvoked, map[string]any{"point": "tool.before", "id": "a"}),
				hookEvent(TypeHookInvoked, map[string]any{"point": "tool.before", "id": "b"}),
				hookEvent(TypeHookInvoked, map[string]any{"point": "tool.before", "id": "b"}), // supersedes b
				hookEvent(TypeHookInvoked, map[string]any{"point": "tool.before", "id": "a"}), // supersedes a
				hookEvent(TypeHookResult, resultPayload("tool.before", "a", "allow")),
				hookEvent(TypeHookResult, resultPayload("tool.before", "b", "allow")))
			Expect(diagnose().Unmatched).To(Equal([]HookInvocation{{Point: "tool.before", ID: "a", Seq: 1},
				{Point: "tool.before", ID: "b", Seq: 2}}))
		})
	})

	Describe("denials and failures", func() {
		It("counts a hook's denials and keeps its last reason", func() {
			first, second := resultPayload("tool.before", "guard", "deny"), resultPayload("tool.before", "guard", "deny")
			first["reason"], second["reason"] = "nope", "still nope"
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, first), hookEvent(TypeHookResult, second))
			Expect(diagnose().Denials).To(Equal([]HookDenial{{Point: "tool.before", ID: "guard", Count: 2, Reason: "still nope"}}))
		})

		It("lists each denying hook once, in first-seen order", func() {
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, resultPayload("tool.before", "a", "deny")),
				hookEvent(TypeHookResult, resultPayload("tool.before", "b", "deny")),
				hookEvent(TypeHookResult, resultPayload("tool.before", "a", "deny")))
			Expect(diagnose().Denials).To(HaveLen(2))
		})

		It("counts the failed hook results", func() {
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, resultPayload("tool.before", "guard", "failed")),
				hookEvent(TypeHookResult, resultPayload("tool.after", "guard", "failed")),
				hookEvent(TypeHookResult, resultPayload("tool.after", "guard", "allow")))
			Expect(diagnose().Failed).To(Equal(int64(2)))
		})

		It("does not list a failed result as a denial", func() {
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, resultPayload("tool.before", "guard", "failed")))
			Expect(diagnose().Denials).To(BeEmpty())
		})

		It("lists a hook's denials at two points separately", func() {
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, resultPayload("tool.before", "evil", "deny")),
				hookEvent(TypeHookResult, resultPayload("user.prompt", "evil", "deny")))
			Expect(diagnose().Denials).To(Equal([]HookDenial{{Point: "tool.before", ID: "evil", Count: 1},
				{Point: "user.prompt", ID: "evil", Count: 1}}))
		})
	})

	Describe("the outcome", func() {
		It("reports a stop as hook_stop naming the hook", func() {
			stop := resultPayload("turn.after", "late", "allow")
			stop["stop"] = true
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, stop))
			Expect(diagnose().Outcome).To(Equal(&HookOutcome{Code: CodeHookStop, ID: "late"}))
		})

		It("reports a user.prompt deny as hook_refused with the id and the reason", func() {
			refused := resultPayload("user.prompt", "guard", "deny")
			refused["reason"] = "blocked"
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, refused))
			Expect(diagnose().Outcome).To(Equal(&HookOutcome{Code: CodeHookRefused, ID: "guard", Reason: "blocked"}))
		})

		It("reports a fail-closed user.prompt failure as hook_refused", func() {
			failed := resultPayload("user.prompt", "guard", "failed")
			failed["failure"], failed["on_failure"] = "exit_status", HookOnFailureDeny
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, failed))
			Expect(diagnose()).To(Equal(HookStats{Unmatched: []HookInvocation{}, Denials: []HookDenial{}, Failed: 1,
				Outcome: &HookOutcome{Code: CodeHookRefused, ID: "guard", Reason: "exit_status"}}))
		})

		DescribeTable("does not treat a failure as a refusal unless it is fail-closed at user.prompt",
			func(point, policy string) {
				failed := resultPayload(point, "guard", "failed")
				failed["failure"], failed["on_failure"] = "exit_status", policy
				_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, failed))
				Expect(diagnose().Outcome).To(BeNil())
			},
			Entry("advisory at user.prompt", "user.prompt", HookOnFailureAllow),
			Entry("fail-closed at tool.before", "tool.before", HookOnFailureDeny))

		It("does not treat a tool.before deny as a refusal", func() {
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, resultPayload("tool.before", "guard", "deny")))
			Expect(diagnose().Outcome).To(BeNil())
		})

		It("reports the latest outcome", func() {
			stop := resultPayload("turn.after", "late", "allow")
			stop["stop"] = true
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, stop),
				hookEvent(TypeHookResult, resultPayload("user.prompt", "guard", "deny")))
			Expect(diagnose().Outcome.Code).To(Equal(CodeHookRefused))
		})

		It("reports none without a stop or a refusal", func() {
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, resultPayload("tool.before", "guard", "allow")))
			Expect(diagnose().Outcome).To(BeNil())
		})
	})

	Describe("untrusted text", func() {
		It("strips control and format characters from a reason", func() {
			denied := resultPayload("user.prompt", "guard", "deny")
			denied["reason"] = "\x1b[31mred\x1b[0m \u202ever" + "sion"
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, denied))
			Expect(diagnose().Denials[0].Reason).To(Equal("[31mred[0m version"))
		})

		It("strips line breaks and tabs from a reason", func() {
			denied := resultPayload("tool.before", "guard", "deny")
			denied["reason"] = "a\nb\tc"
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, denied))
			Expect(diagnose().Denials[0].Reason).To(Equal("abc"))
		})

		It("bounds a reason to the hook reason cap at a character boundary", func() {
			denied := resultPayload("tool.before", "guard", "deny")
			denied["reason"] = strings.Repeat("€", tool.HookReasonCap) // 3 bytes each: the cut lands inside one
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookResult, denied))
			Expect(diagnose().Denials[0].Reason).To(HaveLen(tool.HookReasonCap - 1))
		})

		It("bounds a hook id", func() {
			_, diagnose := hookDoctorSession(ctx, st, hookEvent(TypeHookInvoked,
				map[string]any{"point": "tool.before", "id": strings.Repeat("a", 200)}))
			Expect(diagnose().Unmatched[0].ID).To(HaveLen(64))
		})

		It("does not fail on a hook payload that is not an object", func() {
			sess := mustCreateCrashSession(ctx, st)
			w, err := st.NewEventWriter(ctx, sess.ID)
			if err != nil {
				panic(err)
			}
			if _, aerr := w.Append(ctx, "engine", TypeHookResult, "not-an-object"); aerr != nil {
				panic(aerr)
			}
			if cerr := w.Close(); cerr != nil {
				panic(cerr)
			}
			d, derr := DiagnoseSession(ctx, st, sess.ID)
			if derr != nil {
				panic(derr)
			}
			Expect(d.Hooks).To(Equal(&HookStats{Unmatched: []HookInvocation{}, Denials: []HookDenial{}}))
		})
	})
})
