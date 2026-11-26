//go:build !windows

package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
)

// stopJSON is the stdout of a turn.after hook that asks the run to stop.
const stopJSON = `echo '{"stop":true}'`

// turnHook is a loaded turn.after command hook running script; turn.after fails open.
func turnHook(id, script string) Hook {
	h := gateHook(id, script)
	h.Point, h.OnFailure = HookTurnAfter, HookOnFailureAllow
	return h
}

// pausedReason is the reason of the session's last session.paused event.
func pausedReason(st *store.Store, id string) string {
	paused := gateOfType(gateEvents(st, id), "session.paused")
	if len(paused) == 0 {
		return ""
	}
	return payloadFieldText(paused[len(paused)-1].PayloadJSON, "reason")
}

var _ = Describe("turn.after in the exec loop (ADR-012 packet 4a2)", func() {
	var (
		dir string
		ctx context.Context
	)
	BeforeEach(func() {
		ctx = context.Background()
		dir = GinkgoT().TempDir()
	})

	// loop runs the loop under set over turns, with a limit of maxTurns.
	loop := func(set LoadedHooks, maxTurns int64, turns ...[]provider.StreamEvent) (ExecLoopConfig, *ExecLoopResult, error) {
		cfg := execLoopFixture(&execLoopStreamer{turns: turns})
		cfg.Hooks, cfg.Workdir, cfg.MaxTurns = set, dir, maxTurns
		res, err := RunExecLoop(ctx, cfg)
		return cfg, res, err
	}
	askTurn := func(id string) []provider.StreamEvent { return callTurn(echoCall(id)) }

	Describe("a hook that does not stop", func() {
		var (
			cfg  ExecLoopConfig
			seen string
			err  error
		)
		BeforeEach(func() {
			seen = filepath.Join(dir, "seen")
			hook := gateScript(dir, "obs", "cat >> "+seen+"\necho >> "+seen)
			cfg, _, err = loop(gateSet(turnHook("obs", hook)), 4, askTurn("c1"), finishText("done"))
		})
		It("lets the run complete", func() { Expect(err).To(Succeed()) })
		It("fires after every provider turn, the final one included", func() {
			Expect(gateLines(seen)).To(HaveLen(2))
		})
		It("records hook.invoked for the point turn.after on each turn", func() {
			invoked := gateOfType(gateEvents(cfg.Store, cfg.Session.ID), TypeHookInvoked)
			Expect(payloadFieldText(invoked[1].PayloadJSON, "point")).To(Equal("turn.after"))
		})
		It("fires after the turn's tool result is recorded", func() {
			Expect(gateTypes(gateOfType(gateEvents(cfg.Store, cfg.Session.ID), "tool.result", TypeHookInvoked))).
				To(Equal([]string{"tool.result", TypeHookInvoked, TypeHookInvoked}))
		})
	})

	Describe("the document a turn.after hook reads", func() {
		var doc map[string]any
		BeforeEach(func() {
			seen := filepath.Join(dir, "doc.json")
			hook := gateScript(dir, "obs", "cat > "+seen)
			_, _, _ = loop(gateSet(turnHook("obs", hook)), 4, finishText("done"))
			doc = gateReadDoc(seen)
		})
		It("names the point", func() { Expect(doc).To(HaveKeyWithValue("point", "turn.after")) })
		It("says the turn was the final one", func() {
			Expect(doc["turn"]).To(HaveKeyWithValue("final", true))
		})
		It("counts the turn", func() {
			Expect(doc["turn"]).To(HaveKeyWithValue("index", BeNumerically("==", 1)))
		})
	})

	Describe("a hook that stops after a turn with a tool call", func() {
		var (
			cfg ExecLoopConfig
			res *ExecLoopResult
			err error
		)
		BeforeEach(func() {
			cfg, res, err = loop(gateSet(turnHook("halt", gateScript(dir, "halt", "cat >/dev/null\n"+stopJSON))), 4,
				askTurn("c1"), finishText("never"))
		})
		It("ends the run with ErrHookStop", func() { Expect(err).To(MatchError(ErrHookStop)) })
		It("names the hook", func() { Expect(err).To(MatchError(ContainSubstring("halt"))) })
		It("maps onto the hook_stop code", func() { Expect(ErrorCode(err)).To(Equal(CodeHookStop)) })
		It("is not a provider error", func() { Expect(errors.Is(err, ErrProvider)).To(BeFalse()) })
		It("is not a cancellation", func() { Expect(errors.Is(err, ErrSessionCancelled)).To(BeFalse()) })
		It("requests no further turn", func() { Expect(res.Turns).To(Equal(int64(1))) })
		It("records stop in hook.result", func() {
			result := gateOfType(gateEvents(cfg.Store, cfg.Session.ID), TypeHookResult)[0]
			Expect(payloadFieldRaw(result.PayloadJSON, "stop")).To(Equal("true"))
		})
	})

	It("stops a run on its final no-call turn instead of completing", func() {
		_, _, err := loop(gateSet(turnHook("halt", gateScript(dir, "halt", "cat >/dev/null\n"+stopJSON))), 4, finishText("done"))
		Expect(err).To(MatchError(ErrHookStop))
	})

	Describe("a stop on the last allowed turn", func() {
		var err error
		BeforeEach(func() {
			_, _, err = loop(gateSet(turnHook("halt", gateScript(dir, "halt", "cat >/dev/null\n"+stopJSON))), 1,
				askTurn("c1"))
		})
		It("wins over the turn limit", func() { Expect(err).To(MatchError(ErrHookStop)) })
		It("is not max_turns", func() { Expect(errors.Is(err, ErrMaxTurns)).To(BeFalse()) })
	})

	It("still fails with max_turns on the last allowed turn when no hook stops", func() {
		hook := gateScript(dir, "obs", "cat >/dev/null")
		_, _, err := loop(gateSet(turnHook("obs", hook)), 1, askTurn("c1"))
		Expect(err).To(MatchError(ErrMaxTurns))
	})

	It("does not run a later hook once one stops", func() {
		marker := filepath.Join(dir, "later.out")
		set := gateSet(turnHook("halt", gateScript(dir, "halt", "cat >/dev/null\n"+stopJSON)),
			turnHook("later", gateScript(dir, "later", "echo ran >> "+marker)))
		_, _, _ = loop(set, 4, finishText("done"))
		Expect(gateLines(marker)).To(BeEmpty())
	})

	DescribeTable("lets the run complete when the hook",
		func(script string) {
			_, _, err := loop(gateSet(turnHook("obs", gateScript(dir, "obs", script))), 4, finishText("done"))
			Expect(err).To(Succeed())
		},
		Entry("says stop false", `echo '{"stop":false}'`),
		Entry("fails", "exit 1"),
		Entry("denies", "echo deny >&2\nexit 2"),
		Entry("returns a field turn.after does not permit", `echo '{"args":{"a":1}}'`),
	)

	It("does not stop on a stop that a deny came with", func() {
		hook := gateScript(dir, "obs", `echo '{"decision":"deny","reason":"no","stop":true}'`)
		_, _, err := loop(gateSet(turnHook("obs", hook)), 4, finishText("done"))
		Expect(err).To(Succeed())
	})

	It("refuses stop from a tool.after hook: the field is not permitted there", func() {
		cfg, _, _ := loop(gateSet(afterHook("obs", gateScript(dir, "obs", "cat >/dev/null\n"+stopJSON))), 4, askTurn("c1"), finishText("done"))
		result := gateOfType(gateEvents(cfg.Store, cfg.Session.ID), TypeHookResult)[0]
		Expect(payloadFieldText(result.PayloadJSON, "failure")).To(Equal("field_not_permitted"))
	})
})

var _ = Describe("a turn.after stop on a root session run (ADR-012 packet 4a2)", func() {
	var (
		st   *store.Store
		sess *store.Session
		err  error
	)
	BeforeEach(func() {
		st, sess = execLoopSeed()
		DeferCleanup(func() { _ = st.Close() })
		dir := GinkgoT().TempDir()
		halt := gateScript(dir, "halt", "cat >/dev/null\n"+stopJSON)
		_, err = RunSessionExecLoop(context.Background(), st, filepath.Join(dir, "run.db"), sess,
			&execLoopStreamer{turns: [][]provider.StreamEvent{callTurn(echoCall("c1")), finishText("never")}},
			SessionExecLoopRequest{Allow: []string{"echo"}, MaxTurns: 4, Lease: heldSessionLease(st, sess),
				Hooks: gateSet(turnHook("halt", halt)), Workdir: dir})
	})
	It("reports hook_stop", func() { Expect(err).To(MatchError(ErrHookStop)) })
	It("exits as hook_stop and not as a cancellation or a provider error", func() {
		Expect(ErrorCode(err)).To(Equal(CodeHookStop))
	})
	It("pauses the session", func() {
		Expect(must(st.GetSession(context.Background(), sess.ID)).Status).To(Equal("paused"))
	})
	It("records the pause reason hook_stop", func() { Expect(pausedReason(st, sess.ID)).To(Equal("hook_stop")) })
	It("is not failed", func() {
		Expect(must(st.GetSession(context.Background(), sess.ID)).Status).NotTo(Equal("failed"))
	})
})

var _ = Describe("a loop budget stop beside hook_stop (ADR-012 packet 4a2)", func() {
	It("still pauses with budget_exhausted and reports budget_exceeded", func() {
		st, sess := execLoopSeed()
		DeferCleanup(func() { _ = st.Close() })
		err := finishExecLoopState(context.Background(), st, sess, ErrExecLoopBudget)
		Expect(fmt.Sprint(ErrorCode(err), " ", pausedReason(st, sess.ID))).To(Equal("budget_exceeded budget_exhausted"))
	})
})

var _ = Describe("a turn.after stop in a delegated child (ADR-012 packet 4a2)", func() {
	var (
		f    *delegateFixture
		res  DelegateResult
		rerr error
	)
	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		f = newDelegateFixture(callTurn(echoCall("c1")), finishText("never"))
		f.profile.Tools = []string{"echo"}
		req := f.request()
		req.Hooks = gateSet(turnHook("halt", gateScript(dir, "halt", "cat >/dev/null\n"+stopJSON)))
		res, rerr = RunDelegatedChild(context.Background(), req)
	})
	It("ends the child failed", func() { Expect(res.Status).To(Equal(DelegateStatusFailed)) })
	It("gives hook_stop as the reason", func() { Expect(res.Reason).To(Equal("hook_stop")) })
	It("marks the child session failed, not paused", func() { Expect(f.status(res.ChildID)).To(Equal("failed")) })
	It("is the child's own failure: the caller gets no error", func() { Expect(rerr).To(Succeed()) })
	It("records hook_stop in the receipt", func() {
		Expect(f.receiptPayload()).To(HaveKeyWithValue("reason", "hook_stop"))
	})
	It("counts the stop as the child's failure", func() {
		Expect(delegateFailureIsChild(context.Background(), fmt.Errorf("%w: h", ErrHookStop))).To(BeTrue())
	})
})

var _ = Describe("a turn.after stop in a swarm coordinator (ADR-012 packet 4a2)", func() {
	var res ExecuteSwarmResult
	BeforeEach(func() {
		f := newSwarmExecFixture()
		f.both("done")
		f.cfg.Hooks = gateSet(turnHook("halt", gateScript(GinkgoT().TempDir(), "halt", "cat >/dev/null\n"+stopJSON)))
		res = f.execute(context.Background())
	})
	It("ends the swarm failed", func() { Expect(res.Status).To(Equal(SwarmExecFailed)) })
	It("gives hook_stop as the reason", func() { Expect(res.Reason).To(Equal("hook_stop")) })
	It("carries ErrHookStop as the cause", func() { Expect(res.Cause).To(MatchError(ErrHookStop)) })
})

var _ = DescribeTable("classifyCoordinatorStop keeps a budget stop apart from a hook stop",
	func(err error, want string) {
		_, reason, _ := classifyCoordinatorStop(context.Background(), err)
		Expect(reason).To(Equal(want))
	},
	Entry("a hook stop", fmt.Errorf("%w: h", ErrHookStop), "hook_stop"),
	Entry("a budget stop", fmt.Errorf("%w: loop budget exhausted", ErrBudgetExceeded), "budget_exhausted"),
)
