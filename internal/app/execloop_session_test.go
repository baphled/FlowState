package app

import (
	"context"
	"errors"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// seedWithReservation seeds a parent pool with one delegated child
// holding a live grant; panics are setup failures.
func seedWithReservation(grant int64, childID string) (*store.Store, *store.Session, *store.Session, string) {
	st, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = st.Close() })
	ctx := context.Background()
	parent, err := st.CreateSession(ctx, "parent-"+childID, "scripted", "scripted-model", "harness", "sha")
	if err != nil {
		panic(err)
	}
	child, err := st.CreateChildSession(ctx, childID, parent.ID, "scripted", "scripted-model", "harness", "sha")
	if err != nil {
		panic(err)
	}
	w, err := st.NewEventWriter(ctx, child.ID)
	if err != nil {
		panic(err)
	}
	if _, err := w.Append(ctx, session.ActorUser, session.TypeTurnStart, map[string]any{"task": "delegated"}); err != nil {
		panic(err)
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	if err := st.SetBudgetPool(ctx, parent.ID, grant); err != nil {
		panic(err)
	}
	if _, err := st.ReserveBudget(ctx, parent.ID, child.ID, grant); err != nil {
		panic(err)
	}
	dbPath := filepath.Join(GinkgoT().TempDir(), "binding-"+childID+".db")
	return st, parent, child, dbPath
}

var _ = Describe("Child run budget binding", func() {
	toolTurn := func(id string) []provider.StreamEvent {
		return []provider.StreamEvent{provText("need a tool"), provCall(echoCall(id)), provFinish()}
	}
	finishTurn := func(text string) []provider.StreamEvent {
		return []provider.StreamEvent{provText(text), provFinish()}
	}

	Context("with a grant capped below the turn supply", func() {
		var budgetStopped bool
		var childAfter *store.Session
		BeforeEach(func() {
			st, _, child, dbPath := seedWithReservation(2, "child-bind-1")
			streamer := &execLoopStreamer{turns: [][]provider.StreamEvent{
				toolTurn("c1"), toolTurn("c2"), toolTurn("c3"),
			}}
			_, err := RunSessionExecLoop(context.Background(), st, dbPath, child, streamer, SessionExecLoopRequest{MaxTurns: 8})
			budgetStopped = errors.Is(err, ErrBudgetExceeded)
			got, gerr := st.GetSession(context.Background(), child.ID)
			if gerr != nil {
				panic(gerr)
			}
			childAfter = got
		})

		It("stops the third turn with ErrBudgetExceeded", func() {
			Expect(budgetStopped).To(BeTrue())
		})

		It("pauses the child at the budget stop", func() {
			Expect(childAfter.Status).To(Equal("paused"))
		})
	})

	Context("with a grant covering the run", func() {
		var runErr error
		var reservation store.BudgetReservation
		var snapshot store.BudgetSnapshot
		BeforeEach(func() {
			st, parent, child, dbPath := seedWithReservation(5, "child-bind-2")
			streamer := &execLoopStreamer{turns: [][]provider.StreamEvent{
				finishTurn("done"),
			}}
			_, runErr = RunSessionExecLoop(context.Background(), st, dbPath, child, streamer, SessionExecLoopRequest{MaxTurns: 4})
			res, rerr := st.ReservationFor(context.Background(), child.ID)
			if rerr != nil {
				panic(rerr)
			}
			reservation = res
			snap, serr := st.BudgetSnapshot(context.Background(), parent.ID)
			if serr != nil {
				panic(serr)
			}
			snapshot = snap
		})

		It("completes the run and settles the reservation as charged", func() {
			Expect(runErr).NotTo(HaveOccurred())
			Expect(reservation.Status).To(Equal(store.ReservationStatusCharged))
		})

		It("releases the settled grant from the outstanding budget", func() {
			Expect(snapshot.Reserved).To(Equal(int64(0)))
		})
	})

	Context("across a budget stop and a rerun", func() {
		var firstStopped, rerunErrFree bool
		var statusAfterStop string
		var reservationAfter store.BudgetReservation
		var snapshotAfter store.BudgetSnapshot
		var childAfter *store.Session
		BeforeEach(func() {
			st, parent, child, dbPath := seedWithReservation(2, "child-bind-3")
			stopper := &execLoopStreamer{turns: [][]provider.StreamEvent{
				toolTurn("s1"), toolTurn("s2"), toolTurn("s3"),
			}}
			_, stopErr := RunSessionExecLoop(context.Background(), st, dbPath, child, stopper, SessionExecLoopRequest{MaxTurns: 8})
			firstStopped = errors.Is(stopErr, ErrBudgetExceeded)
			if stopErr != nil && !firstStopped {
				panic(stopErr)
			}
			var gerr error
			childAfter, gerr = st.GetSession(context.Background(), child.ID)
			if gerr != nil {
				panic(gerr)
			}
			statusAfterStop = childAfter.Status
			if rerr := ResumePausedSession(context.Background(), st, childAfter); rerr != nil {
				panic(rerr)
			}
			finisher := &execLoopStreamer{turns: [][]provider.StreamEvent{
				finishTurn("final answer"),
			}}
			_, rerunErr := RunSessionExecLoop(context.Background(), st, dbPath, childAfter, finisher, SessionExecLoopRequest{MaxTurns: 4})
			rerunErrFree = rerunErr == nil
			res, resErr := st.ReservationFor(context.Background(), child.ID)
			if resErr != nil {
				panic(resErr)
			}
			reservationAfter = res
			snap, snapErr := st.BudgetSnapshot(context.Background(), parent.ID)
			if snapErr != nil {
				panic(snapErr)
			}
			snapshotAfter = snap
		})

		It("budget-stops the first run at the grant boundary", func() {
			Expect(firstStopped).To(BeTrue())
		})

		It("pauses the child after the budget stop", func() {
			Expect(statusAfterStop).To(Equal("paused"))
		})

		It("completes the rerun within the full grant", func() {
			Expect(rerunErrFree).To(BeTrue())
		})

		It("settles the reservation as charged after the rerun", func() {
			Expect(reservationAfter.Status).To(Equal(store.ReservationStatusCharged))
		})

		It("releases the grant from the outstanding budget", func() {
			Expect(snapshotAfter.Reserved).To(Equal(int64(0)))
		})

		It("settles the reservation as charged after the rerun", func() {
			Expect(reservationAfter.Status).To(Equal(store.ReservationStatusCharged))
		})

		It("releases the grant from the outstanding budget", func() {
			Expect(snapshotAfter.Reserved).To(Equal(int64(0)))
		})
	})
})

var _ = Describe("Loop completion finish reason", func() {
	var (
		st   *store.Store
		sess *store.Session
	)

	BeforeEach(func() {
		st, sess = execLoopSeed()
		DeferCleanup(func() { _ = st.Close() })
	})

	DescribeTable("records the reason on the terminal turn.end", func(reason, want string) {
		if err := completeExecLoopRun(context.Background(), st, sess.ID, reason); err != nil {
			panic(err)
		}
		Expect(lastPayload(st, sess.ID)).To(MatchJSON(want))
	},
		Entry("a length finish", provider.FinishLength, `{"status":"completed","finish_reason":"length"}`),
		Entry("no finish event", "", `{"status":"completed"}`),
	)

	Context("when RunSessionExecLoop ends on a length finish", func() {
		BeforeEach(func() {
			cut := provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: provider.FinishLength}
			streamer := &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("cut"), cut}}}
			dbPath := filepath.Join(GinkgoT().TempDir(), "finish.db")
			if _, err := RunSessionExecLoop(context.Background(), st, dbPath, sess, streamer, SessionExecLoopRequest{MaxTurns: 2}); err != nil {
				panic(err)
			}
		})

		It("carries the reason onto turn.end", func() {
			Expect(lastPayload(st, sess.ID)).To(MatchJSON(`{"status":"completed","finish_reason":"length"}`))
		})

		It("keeps the session completed", func() {
			Expect(statusOf(st, sess.ID)).To(Equal("completed"))
		})
	})
})

var _ = Describe("Loop allowlist requirement", func() {
	var (
		st     *store.Store
		sess   *store.Session
		dbPath string
	)

	BeforeEach(func() {
		st, sess = execLoopSeed()
		DeferCleanup(func() { _ = st.Close() })
		dbPath = filepath.Join(GinkgoT().TempDir(), "allow.db")
	})

	DescribeTable("RequireAllowlist decides whether an empty allowlist is a usage error",
		func(require, wantUsage bool) {
			streamer := &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("ok"), provFinish()}}}
			_, err := RunSessionExecLoop(context.Background(), st, dbPath, sess, streamer,
				SessionExecLoopRequest{MaxTurns: 2, RequireAllowlist: require})
			Expect(errors.Is(err, ErrUsage)).To(Equal(wantUsage))
		},
		Entry("required and empty", true, true),
		Entry("not required and empty", false, false),
	)
})
