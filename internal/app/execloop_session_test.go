package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gtypes "github.com/onsi/gomega/types"

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

var _ = Describe("Delegation wiring of a session run", func() {
	var (
		st      *store.Store
		sess    *store.Session
		dbPath  string
		workdir string
		userDir string
	)
	var delegateTurn []provider.StreamEvent

	run := func(streamer provider.Streamer, req SessionExecLoopRequest) error {
		req.Workdir, req.MaxTurns = workdir, 4
		_, err := RunSessionExecLoop(context.Background(), st, dbPath, sess, streamer, req)
		return err
	}

	BeforeEach(func() {
		delegateTurn = callTurn(delegateCall(`{"agent":"reviewer","task":"look"}`))
		st, sess = execLoopSeed()
		DeferCleanup(func() { _ = st.Close() })
		config := GinkgoT().TempDir()
		for _, env := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA"} {
			GinkgoT().Setenv(env, config)
		}
		base, err := os.UserConfigDir()
		if err != nil {
			panic(err)
		}
		userDir = filepath.Join(base, "flowstate", "agents")
		dbPath = filepath.Join(GinkgoT().TempDir(), "wire.db")
		workdir = GinkgoT().TempDir()
	})

	DescribeTable("refuses a scope and an allowlist that disagree before anything runs",
		func(req SessionExecLoopRequest, want gtypes.GomegaMatcher) {
			putAgent(userDir, "dup.md", agentText("dup"))
			putAgent(filepath.Join(workdir, ".flowstate", "agents"), "dup.md", agentText("dup"))
			Expect(run(&execLoopStreamer{}, req)).To(want)
		},
		Entry("delegate allowlisted without --agents",
			SessionExecLoopRequest{Allow: []string{"delegate"}}, MatchError(ContainSubstring("delegate needs --agents"))),
		Entry("--agents without delegate on the allowlist",
			SessionExecLoopRequest{Allow: []string{"echo"}, Agents: "user"},
			MatchError(ContainSubstring("--agents loads profiles but delegate is not allowlisted"))),
		Entry("--agents with no allowlist at all",
			SessionExecLoopRequest{Agents: "user", RequireAllowlist: true},
			MatchError(ContainSubstring("delegate is not allowlisted"))),
		Entry("an invalid scope",
			SessionExecLoopRequest{Allow: []string{"delegate"}, Agents: "bogus"}, MatchError(ErrUsage)),
		Entry("a profile name in both directories under both",
			SessionExecLoopRequest{Allow: []string{"delegate"}, Agents: "both"}, MatchError(ErrConfigInvalid)),
	)

	It("sends no provider request when the profiles conflict", func() {
		putAgent(userDir, "dup.md", agentText("dup"))
		putAgent(filepath.Join(workdir, ".flowstate", "agents"), "dup.md", agentText("dup"))
		streamer := &execLoopStreamer{}
		_ = run(streamer, SessionExecLoopRequest{Allow: []string{"delegate"}, Agents: "both"})
		Expect(streamer.reqs).To(BeEmpty())
	})

	Context("with a loaded profile and the delegate tool allowlisted", func() {
		var streamer *execLoopStreamer
		var err error
		BeforeEach(func() {
			putAgent(userDir, "reviewer.md", agentText("reviewer"))
			streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{delegateTurn, finishText("child says hi"), finishText("parent done")}}
			err = run(streamer, SessionExecLoopRequest{Allow: []string{"delegate"}, Agents: "user", Budget: 100})
		})
		It("completes the run", func() { Expect(err).To(Succeed()) })
		It("marks the parent completed", func() { Expect(statusOf(st, sess.ID)).To(Equal("completed")) })
		It("runs one child", func() {
			all, lerr := st.ListSessions(context.Background(), "")
			if lerr != nil {
				panic(lerr)
			}
			Expect(all).To(HaveLen(2))
		})
		It("describes the loaded profile to the model", func() {
			Expect(streamer.reqs[0].Tools[0].Description).To(ContainSubstring("- reviewer: d"))
		})
	})

	It("tells the model no agent is available when no profile is loaded", func() {
		streamer := &execLoopStreamer{turns: [][]provider.StreamEvent{finishText("ok")}}
		if err := run(streamer, SessionExecLoopRequest{Allow: []string{"delegate"}, Agents: "user"}); err != nil {
			panic(err)
		}
		Expect(streamer.reqs[0].Tools[0].Description).To(ContainSubstring("No agents are available"))
	})

	Context("without --workdir", func() {
		var streamer *execLoopStreamer
		var projectDir string
		runDefault := func(req SessionExecLoopRequest) error {
			req.Allow, req.MaxTurns = []string{"delegate"}, 4
			_, err := RunSessionExecLoop(context.Background(), st, dbPath, sess, streamer, req)
			return err
		}
		BeforeEach(func() {
			streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{delegateTurn, finishText("child says hi"), finishText("parent done")}}
			projectDir = GinkgoT().TempDir()
		})
		It("delegates although the default workdir did not exist", func() {
			putAgent(userDir, "reviewer.md", agentText("reviewer"))
			if err := runDefault(SessionExecLoopRequest{Agents: "user"}); err != nil {
				panic(err)
			}
			all, lerr := st.ListSessions(context.Background(), "")
			if lerr != nil {
				panic(lerr)
			}
			Expect(all).To(HaveLen(2))
		})
		It("finds a project profile in the project root, not beside the database", func() {
			putAgent(filepath.Join(projectDir, ".flowstate", "agents"), "reviewer.md", agentText("reviewer"))
			if err := runDefault(SessionExecLoopRequest{Agents: "project", ProjectRoot: projectDir}); err != nil {
				panic(err)
			}
			Expect(streamer.reqs[0].Tools[0].Description).To(ContainSubstring("- reviewer: d"))
		})
	})

	It("finds a project profile under --workdir", func() {
		putAgent(filepath.Join(workdir, ".flowstate", "agents"), "reviewer.md", agentText("reviewer"))
		streamer := &execLoopStreamer{turns: [][]provider.StreamEvent{finishText("ok")}}
		if err := run(streamer, SessionExecLoopRequest{Allow: []string{"delegate"}, Agents: "project"}); err != nil {
			panic(err)
		}
		Expect(streamer.reqs[0].Tools[0].Description).To(ContainSubstring("- reviewer: d"))
	})

	Context("when the run is cancelled while its child runs", func() {
		var childStatus string
		var err error
		BeforeEach(func() {
			putAgent(userDir, "reviewer.md", agentText("reviewer"))
			blocker := &blockAfterFirst{started: make(chan struct{})}
			blocker.turns = [][]provider.StreamEvent{delegateTurn}
			cctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			done := make(chan error, 1)
			go func() {
				_, e := RunSessionExecLoop(cctx, st, dbPath, sess, blocker, SessionExecLoopRequest{
					Workdir: workdir, MaxTurns: 4, Allow: []string{"delegate"}, Agents: "user",
				})
				done <- e
			}()
			select {
			case <-blocker.started:
			case <-time.After(10 * time.Second):
				panic("the child never reached the provider")
			}
			cancel(&CancelCause{Reason: "sigterm"})
			select {
			case err = <-done:
			case <-time.After(10 * time.Second):
				panic("the run did not return after cancellation")
			}
			all, lerr := st.ListSessions(context.Background(), "cancelled")
			if lerr != nil {
				panic(lerr)
			}
			for _, s := range all {
				if s.ID != sess.ID {
					childStatus = s.Status
				}
			}
		})
		It("ends the run as cancelled", func() { Expect(err).To(MatchError(ErrSessionCancelled)) })
		It("marks the parent cancelled", func() { Expect(statusOf(st, sess.ID)).To(Equal("cancelled")) })
		It("marks the child cancelled", func() { Expect(childStatus).To(Equal("cancelled")) })
	})
})

var _ = Describe("Loop turn limit of a session run", func() {
	var (
		st     *store.Store
		sess   *store.Session
		dbPath string
	)

	// run drives RunSessionExecLoop over echo with maxTurns, the model asking
	// for the tool on its first toolTurns turns and answering on the next.
	run := func(maxTurns int64, toolTurns int) error {
		turns := make([][]provider.StreamEvent, 0, toolTurns+1)
		for i := 0; i < toolTurns; i++ {
			turns = append(turns, callTurn(echoCall(fmt.Sprintf("c%d", i))))
		}
		turns = append(turns, []provider.StreamEvent{provText("answer"), provFinish()})
		_, err := RunSessionExecLoop(context.Background(), st, dbPath, sess, &execLoopStreamer{turns: turns},
			SessionExecLoopRequest{Allow: []string{"echo"}, MaxTurns: maxTurns})
		return err
	}

	BeforeEach(func() {
		st, sess = execLoopSeed()
		DeferCleanup(func() { _ = st.Close() })
		dbPath = filepath.Join(GinkgoT().TempDir(), "turns.db")
	})

	It("completes a run that answers on its third turn under a limit of 3", func() {
		Expect(run(3, 2)).To(Succeed())
	})

	It("fails with max_turns a run still asking for tools at a limit of 2", func() {
		Expect(run(2, 2)).To(MatchError(ErrMaxTurns))
	})

	It("completes a run that answers on turn 25 when no limit is given", func() {
		Expect(run(0, 24)).To(Succeed())
	})

	It("fails with max_turns a run still asking for tools at turn 25 when no limit is given", func() {
		Expect(run(0, 25)).To(MatchError(ErrMaxTurns))
	})
})

var _ = Describe("Loop turn limit of a delegating parent", func() {
	var (
		st      *store.Store
		sess    *store.Session
		workdir string
		reqs    int
		err     error
	)

	// run drives a parent that delegates on every turn to a child that
	// reads childReads times before answering, under the parent limit.
	run := func(parentTurns int64, childReads int) {
		child := make([][]provider.StreamEvent, 0, childReads+1)
		for i := 0; i < childReads; i++ {
			read := provider.ToolCall{ID: fmt.Sprintf("r%d", i), Name: "fs.read", Arguments: json.RawMessage(`{"path":"none.txt"}`)}
			child = append(child, callTurn(read))
		}
		child = append(child, []provider.StreamEvent{provText("child done"), provFinish()})
		var turns [][]provider.StreamEvent
		for i := int64(0); i <= parentTurns; i++ {
			turns = append(turns, callTurn(delegateCall(`{"agent":"reviewer","task":"look"}`)))
			turns = append(turns, child...)
		}
		streamer := &execLoopStreamer{turns: turns}
		_, err = RunSessionExecLoop(context.Background(), st, filepath.Join(workdir, "run.db"), sess, streamer,
			SessionExecLoopRequest{Allow: []string{"delegate"}, Agents: "user", Workdir: workdir, MaxTurns: parentTurns})
		reqs = len(streamer.reqs)
	}

	BeforeEach(func() {
		st, sess = execLoopSeed()
		DeferCleanup(func() { _ = st.Close() })
		config := GinkgoT().TempDir()
		for _, env := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA"} {
			GinkgoT().Setenv(env, config)
		}
		base, uerr := os.UserConfigDir()
		if uerr != nil {
			panic(uerr)
		}
		putAgent(filepath.Join(base, "flowstate", "agents"), "reviewer.md", agentText("reviewer"))
		workdir = GinkgoT().TempDir()
	})

	Context("with a limit of 3 and one-turn children", func() {
		BeforeEach(func() { run(3, 0) })
		It("fails the parent with max_turns", func() { Expect(err).To(MatchError(ErrMaxTurns)) })
		It("makes three parent turns and three child turns", func() { Expect(reqs).To(Equal(6)) })
	})

	Context("with a limit of 2 and a child that needs four turns", func() {
		BeforeEach(func() { run(2, 3) })
		It("leaves the child to its own profile limit", func() { Expect(reqs).To(Equal(2 + 2*4)) })
	})
})

var _ = Describe("RunSessionExecLoop given a held lease", func() {
	var (
		st     *store.Store
		sess   *store.Session
		handle *store.RunLease
		dbPath string
	)
	BeforeEach(func() {
		st, sess = execLoopSeed()
		DeferCleanup(func() { _ = st.Close() })
		var err error
		if handle, err = st.AcquireRunLease(context.Background(), "exec:"+sess.ID, "run:held-1"); err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = handle.Release(context.Background()) })
		dbPath = filepath.Join(GinkgoT().TempDir(), "held.db")
	})
	run := func(ctx context.Context, streamer provider.Streamer, req SessionExecLoopRequest) (*ExecLoopResult, error) {
		req.Lease = handle
		return RunSessionExecLoop(ctx, st, dbPath, sess, streamer, req)
	}
	// stillHeld is the row check: another owner cannot take the item while it exists.
	stillHeld := func() error {
		_, err := st.AcquireRunLease(context.Background(), "exec:"+sess.ID, "run:second")
		return err
	}

	It("forwards the handle to the loop, which then takes no lease of its own", func() {
		res, err := run(context.Background(), &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("done"), provFinish()}}}, SessionExecLoopRequest{})
		if err != nil {
			panic(err)
		}
		Expect(res.FinalContent).To(Equal("done"))
	})

	DescribeTable("keeps the handle held after the post-loop state writers have returned", func(
		streamer func(cancel context.CancelFunc) provider.Streamer, req SessionExecLoopRequest, status string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, _ = run(ctx, streamer(cancel), req)
		got, err := st.GetSession(context.Background(), sess.ID)
		if err != nil {
			panic(err)
		}
		if got.Status != status {
			panic("the post-loop writer did not run: status " + got.Status)
		}
		Expect(handle.Fence(context.Background())).To(Succeed())
	},
		Entry("after completeExecLoopRun", func(context.CancelFunc) provider.Streamer {
			return &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("done"), provFinish()}}}
		}, SessionExecLoopRequest{}, "completed"),
		Entry("after pauseExecLoopRun", func(context.CancelFunc) provider.Streamer {
			return &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("x"), provCall(echoCall("p1")), provFinish()}, {provText("never")}}}
		}, SessionExecLoopRequest{Allow: []string{"echo"}, Budget: 1}, "paused"),
		Entry("after CancelSession", func(cancel context.CancelFunc) provider.Streamer {
			return &cancelOnStream{n: 1, cancel: cancel}
		}, SessionExecLoopRequest{}, "cancelled"),
	)

	It("refuses a handle for another session's item", func() {
		other, err := st.AcquireRunLease(context.Background(), "exec:another-session", "run:other")
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = other.Release(context.Background()) })
		_, err = RunSessionExecLoop(context.Background(), st, dbPath, sess, &execLoopStreamer{}, SessionExecLoopRequest{Lease: other})
		Expect(err).To(MatchError(ErrExecLoopConfig))
	})

	It("keeps the row so a second run stays out while the caller holds the handle", func() {
		_, _ = run(context.Background(), &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("done"), provFinish()}}}, SessionExecLoopRequest{})
		Expect(stillHeld()).To(MatchError(store.ErrSessionBusy))
	})

	It("lets a second run in once the caller releases the handle", func() {
		_, _ = run(context.Background(), &execLoopStreamer{turns: [][]provider.StreamEvent{{provText("done"), provFinish()}}}, SessionExecLoopRequest{})
		if err := handle.Release(context.Background()); err != nil {
			panic(err)
		}
		Expect(stillHeld()).To(Succeed())
	})
})
