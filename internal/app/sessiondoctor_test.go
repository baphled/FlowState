package app

import (
	"context"
	"encoding/json"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
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
