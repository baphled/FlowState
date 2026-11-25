package app

import (
	"context"
	"errors"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// probeStreamer runs probe just before it serves its first request, so a spec
// can read the state a resumed child starts from.
type probeStreamer struct {
	execLoopStreamer
	probe func()
}

func (p *probeStreamer) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	if len(p.reqs) == 0 {
		p.probe()
	}
	return p.execLoopStreamer.Stream(ctx, req)
}

// seedChild creates a child of parent that a crash left interrupted: active, its
// first turns in its log (an echo call and its result each) and its reservation
// still reserved with nothing charged, as a run that never settled leaves it.
func (f *delegateFixture) seedChild(parent *store.Session, turns int64) *store.Session {
	return f.seedChildID(parent, must(session.NewID()), turns)
}

// seedChildID is seedChild with the child's id given.
func (f *delegateFixture) seedChildID(parent *store.Session, id string, turns int64) *store.Session {
	ctx := context.Background()
	check(f.st.SetBudgetPool(ctx, parent.ID, delegateParentPool))
	check(ReserveChildBudget(ctx, f.st, parent.ID, id, int64(f.profile.MaxTurns)))
	child := must(openChildSession(ctx, f.st, childSpec{
		id: id, parentID: parent.ID, provider: "scripted", modelID: "parent-model", harness: "harness",
		configSHA: "sha", task: "review the change", profile: profileRef(f.profile),
	}))
	script := make([][]provider.StreamEvent, turns)
	for i := range script {
		script[i] = []provider.StreamEvent{provText("t"), provCall(echoCall("c" + strconv.Itoa(i))), provFinish()}
	}
	lease, release := acquireTestLease(f.st, "exec:"+id, delegateLeasePrefix+"seed")
	_, err := RunExecLoop(ctx, ExecLoopConfig{
		Store: f.st, Session: child, Streamer: &execLoopStreamer{turns: script}, Registry: must(NewSessionToolRegistry("")),
		Allowlist: []string{"echo"}, Pool: NewBudgetPool(turns), TurnBudget: 1, Lease: lease,
		MaxTurns: turns + 1,
	})
	release() // the child is resumed under its own lease
	if !errors.Is(err, ErrExecLoopBudget) {
		panic("the seeded child must stop at its budget")
	}
	return child
}

// resumeRequest asks to resume id with the fixture's profile loaded.
func (f *delegateFixture) resumeRequest(id, task string) DelegateResumeRequest {
	req := f.request()
	req.Task = task
	return DelegateResumeRequest{DelegateRequest: req, ChildID: id, Profiles: []agent.Profile{f.profile}}
}

// roles lists the message roles of one provider request.
func roles(req *provider.ChatRequest) []string {
	out := []string{}
	for _, m := range req.Messages {
		out = append(out, m.Role)
	}
	return out
}

// refusalEntries are the requests a resume refuses before it claims anything.
func refusalEntries() []TableEntry {
	notChild := "is not a child of this session"
	return []TableEntry{
		Entry("a terminal child", func(f *delegateFixture) string {
			c := f.seedChild(f.parent, 2)
			check(f.st.UpdateSessionStatus(context.Background(), c.ID, "completed"))
			return c.ID
		}, "cannot be resumed; start a new one"),
		Entry("another parent's child", func(f *delegateFixture) string {
			other := must(f.st.CreateSession(context.Background(), "other-parent", "scripted", "m", "h", "sha"))
			return f.seedChild(other, 2).ID
		}, notChild),
		Entry("an unknown id", func(*delegateFixture) string { return "ghost" }, notChild),
		Entry("an id of spaces", func(*delegateFixture) string { return "   " }, notChild),
		Entry("a unicode id", func(*delegateFixture) string { return "é☃" }, notChild),
		Entry("an SQL-ish id", func(*delegateFixture) string { return "x' OR '1'='1" }, notChild),
		Entry("a 65-character id", func(*delegateFixture) string { return strings.Repeat("a", resumeIDMax+1) }, notChild),
		Entry("a child of this parent whose id is 65 characters", func(f *delegateFixture) string {
			return f.seedChildID(f.parent, strings.Repeat("a", resumeIDMax+1), 2).ID
		}, notChild),
		Entry("a child of this parent whose id has a space", func(f *delegateFixture) string {
			return f.seedChildID(f.parent, "a b", 2).ID
		}, notChild),
		Entry("a root session id", func(f *delegateFixture) string {
			return must(f.st.CreateSession(context.Background(), "root-2", "scripted", "m", "h", "sha")).ID
		}, notChild),
		Entry("a profile that changed", func(f *delegateFixture) string {
			c := f.seedChild(f.parent, 2)
			f.profile.SHA256 = "changed"
			return c.ID
		}, "changed since the child started"),
		Entry("a child with no turns left", func(f *delegateFixture) string {
			return f.seedChild(f.parent, 3).ID
		}, "has used all 3 of its turns"),
	}
}

var _ = Describe("ResumeDelegatedChild", func() {
	var ctx context.Context
	BeforeEach(func() { ctx = context.Background() })

	DescribeTable("refuses, starting nothing",
		func(prepare func(*delegateFixture) string, message string) {
			f := newDelegateFixture(finishText("never"))
			id := prepare(f)
			_, err := ResumeDelegatedChild(ctx, f.resumeRequest(id, ""))
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		append(refusalEntries(),
			Entry("a child leased under the old shared owner name", func(f *delegateFixture) string {
				c := f.seedChild(f.parent, 2)
				must(f.st.AcquireLease(context.Background(), "exec:"+c.ID, "delegate"))
				return c.ID
			}, "is running"),
			Entry("a child leased by another run", func(f *delegateFixture) string {
				c := f.seedChild(f.parent, 2)
				must(f.st.AcquireLease(context.Background(), "exec:"+c.ID, delegateLeasePrefix+"other-run"))
				return c.ID
			}, "is running"),
		),
	)

	// nextToken is the lease token the next claim of the item would get: it
	// reads lease_history, which a refused resume must not touch.
	nextToken := func(f *delegateFixture, item string) int64 {
		token := must(f.st.AcquireLease(ctx, item, "probe"))
		check(f.st.ReleaseLease(ctx, item, "probe", token))
		return token
	}

	DescribeTable("writes no lease or lease history when it refuses",
		func(prepare func(*delegateFixture) string, _ string) {
			f := newDelegateFixture(finishText("never"))
			id := prepare(f)
			before := nextToken(f, "exec:"+id)
			_, _ = ResumeDelegatedChild(ctx, f.resumeRequest(id, ""))
			Expect(nextToken(f, "exec:"+id)).To(Equal(before + 1))
		},
		refusalEntries(),
	)

	It("leaves another parent's child's lease token as it was", func() {
		f := newDelegateFixture(finishText("never"))
		other := must(f.st.CreateSession(ctx, "other-parent", "scripted", "m", "h", "sha"))
		c := f.seedChild(other, 2)
		held := must(f.st.AcquireLease(ctx, "exec:"+c.ID, delegateLeasePrefix+"their-run"))
		_, _ = ResumeDelegatedChild(ctx, f.resumeRequest(c.ID, ""))
		Expect(f.st.LeaseToken(ctx, "exec:"+c.ID)).To(Equal(held))
	})

	It("leaves no lease row behind a refused resume", func() {
		f := newDelegateFixture(finishText("never"))
		c := f.seedChild(f.parent, 2)
		check(f.st.UpdateSessionStatus(ctx, c.ID, "completed"))
		_, _ = ResumeDelegatedChild(ctx, f.resumeRequest(c.ID, ""))
		Expect(f.st.LeaseToken(ctx, "exec:"+c.ID)).To(BeZero())
	})

	Describe("two resumes of one child, the second started inside the first", func() {
		var f *delegateFixture
		var id string
		var loserErr error
		var rowHeld error
		BeforeEach(func() {
			f = newDelegateFixture(finishText("loser never runs"))
			id = f.seedChild(f.parent, 2).ID
			winner := &probeStreamer{execLoopStreamer: execLoopStreamer{turns: [][]provider.StreamEvent{finishText("won")}}}
			winner.probe = func() {
				_, loserErr = ResumeDelegatedChild(ctx, f.resumeRequest(id, ""))
				_, rowHeld = f.st.AcquireLease(ctx, "exec:"+id, "probe")
			}
			rr := f.resumeRequest(id, "")
			rr.Streamer = winner
			must(ResumeDelegatedChild(ctx, rr))
		})
		It("refuses the second as running", func() { Expect(loserErr).To(MatchError(ContainSubstring("is running"))) })
		It("leaves the winner's lease row in place after the loser returns", func() {
			Expect(rowHeld).To(MatchError(store.ErrLeaseHeld))
		})
		It("lets the loser write no delegation.started", func() {
			Expect(f.events(f.parent.ID, TypeDelegationStarted)).To(HaveLen(1))
		})
		It("lets the loser write no receipt", func() {
			Expect(f.events(f.parent.ID, TypeDelegationCompleted)).To(HaveLen(1))
		})
	})

	// crashedCall records a delegate call of the parent that started child and
	// never got its result, with the lease the killed runner left on the child.
	crashedCall := func(f *delegateFixture, callID string, c *store.Session, owner string) {
		mustAppendEvent(ctx, f.st, f.parent.ID, "tool:delegate", "tool.call", map[string]any{
			"tool": "delegate", "tool_call_id": callID, "args": map[string]any{},
		})
		mustAppendEvent(ctx, f.st, f.parent.ID, "engine", TypeDelegationStarted, map[string]any{"child_id": c.ID})
		must(f.st.AcquireLease(ctx, "exec:"+c.ID, owner))
		check(RecoverCrashedSession(ctx, f.st, f.parent.ID))
	}

	It("resumes a child whose stale lease the parent's crash recovery cleared", func() {
		f := newDelegateFixture(finishText("ok"))
		c := f.seedChild(f.parent, 2)
		crashedCall(f, "d1", c, delegateLeasePrefix+"dead-run")
		res := must(ResumeDelegatedChild(ctx, f.resumeRequest(c.ID, "")))
		Expect(res.Status).To(Equal(DelegateStatusCompleted))
	})

	It("resumes a child left under the legacy lease owner after recovery", func() {
		f := newDelegateFixture(finishText("ok"))
		c := f.seedChild(f.parent, 2)
		crashedCall(f, "d1", c, delegateLegacyOwner)
		res := must(ResumeDelegatedChild(ctx, f.resumeRequest(c.ID, "")))
		Expect(res.Status).To(Equal(DelegateStatusCompleted))
	})

	It("resumes the child of a second crash in the same parent", func() {
		f := newDelegateFixture(finishText("ok"))
		first := f.seedChild(f.parent, 2)
		crashedCall(f, "d1", first, delegateLeasePrefix+"dead-run-1")
		second := f.seedChild(f.parent, 2)
		crashedCall(f, "d2", second, delegateLeasePrefix+"dead-run-2")
		res := must(ResumeDelegatedChild(ctx, f.resumeRequest(second.ID, "")))
		Expect(res.Status).To(Equal(DelegateStatusCompleted))
	})

	It("keeps refusing a child whose lease belongs to a live run when no crash was answered", func() {
		f := newDelegateFixture(finishText("never"))
		c := f.seedChild(f.parent, 2)
		must(f.st.AcquireLease(ctx, "exec:"+c.ID, delegateLeasePrefix+"live-run"))
		check(RecoverCrashedSession(ctx, f.st, f.parent.ID))
		_, err := ResumeDelegatedChild(ctx, f.resumeRequest(c.ID, ""))
		Expect(err).To(MatchError(ContainSubstring("is running")))
	})

	It("starts no run for a refused child", func() {
		f := newDelegateFixture(finishText("never"))
		_, _ = ResumeDelegatedChild(ctx, f.resumeRequest("ghost", ""))
		Expect(f.streamer.reqs).To(BeEmpty())
	})

	It("refuses a profile that is no longer loaded", func() {
		f := newDelegateFixture(finishText("never"))
		rr := f.resumeRequest(f.seedChild(f.parent, 2).ID, "")
		rr.Profiles = nil
		_, err := ResumeDelegatedChild(ctx, rr)
		Expect(err).To(MatchError(ContainSubstring("profile reviewer is not loaded")))
	})

	Describe("a pool that cannot cover the remaining turns", func() {
		var f *delegateFixture
		var id string
		var res DelegateResult
		BeforeEach(func() {
			f = newDelegateFixture(finishText("never"))
			f.pool = NewBudgetPool(1)
			id = f.seedChild(f.parent, 1).ID
			res = must(ResumeDelegatedChild(ctx, f.resumeRequest(id, "")))
		})
		It("fails with budget_exhausted", func() { Expect(res.Reason).To(HavePrefix("budget_exhausted")) })
		It("names the child", func() { Expect(res.ChildID).To(Equal(id)) })
		It("leaves the old reservation uncharged", func() { Expect(f.reservation(id).Consumed).To(BeZero()) })
		It("runs nothing", func() { Expect(f.streamer.reqs).To(BeEmpty()) })
	})

	It("fails with budget_exhausted when the reservation is no longer live", func() {
		f := newDelegateFixture(finishText("never"))
		c := f.seedChild(f.parent, 2)
		check(f.st.SetReservationCharged(ctx, c.ID))
		res := must(ResumeDelegatedChild(ctx, f.resumeRequest(c.ID, "")))
		Expect(res.Reason).To(ContainSubstring("reservation is not live"))
	})

	Describe("a crashed child with two turns in its log", func() {
		var f *delegateFixture
		var child *store.Session
		var probe *probeStreamer
		var held store.BudgetReservation
		var pool int64
		var res DelegateResult
		BeforeEach(func() {
			f = newDelegateFixture()
			child = f.seedChild(f.parent, 2)
			probe = &probeStreamer{execLoopStreamer: execLoopStreamer{turns: [][]provider.StreamEvent{finishText("resumed ok")}}}
			probe.probe = func() { held, pool = f.reservation(child.ID), f.pool.Remaining() }
			rr := f.resumeRequest(child.ID, "")
			rr.Streamer = probe
			res = must(ResumeDelegatedChild(ctx, rr))
		})
		It("completes", func() { Expect(res.Status).To(Equal(DelegateStatusCompleted)) })
		It("marks the child completed", func() { Expect(f.status(child.ID)).To(Equal("completed")) })
		It("charges the old reservation for the two turns before it continues", func() {
			Expect(held.Consumed).To(Equal(int64(2)))
		})
		It("draws only the remaining turn from the parent's pool", func() {
			Expect(pool).To(Equal(delegateParentPool - 1))
		})
		It("charges the turns of both runs in the end", func() {
			Expect(f.reservation(child.ID).Consumed).To(Equal(int64(3)))
		})
		It("settles the reservation", func() {
			Expect(f.reservation(child.ID).Status).To(Equal(store.ReservationStatusCharged))
		})
		It("returns the unspent grant to the parent's pool", func() { Expect(f.pool.Remaining()).To(Equal(delegateParentPool - 1)) })
		It("sends the earlier turns in the first request", func() {
			Expect(roles(probe.reqs[0])).To(Equal([]string{"system", "user", "assistant", "tool", "assistant", "tool"}))
		})
		It("sends the assistant text of the earlier turns in the first request", func() {
			Expect(assistantTexts(probe.reqs[0].Messages)).To(Equal([]string{"t", "t"}))
		})
		It("records the receipt as resumed", func() { Expect(f.receiptPayload()).To(HaveKeyWithValue("resumed", true)) })
		It("records delegation.started before the receipt, as resumed", func() {
			Expect(f.events(f.parent.ID, TypeDelegationStarted)[0].PayloadJSON).To(ContainSubstring(`"resumed":true`))
		})
	})

	It("ends a resumed child that still asks for tools after its remaining turn with max_turns", func() {
		f := newDelegateFixture(callTurn(provCallToolRead()), finishText("never"))
		res := must(ResumeDelegatedChild(ctx, f.resumeRequest(f.seedChild(f.parent, 2).ID, "")))
		Expect(res.Reason).To(Equal(CodeMaxTurns))
	})

	It("appends a follow-up task as the next user message before the loop", func() {
		f := newDelegateFixture(finishText("ok"))
		must(ResumeDelegatedChild(ctx, f.resumeRequest(f.seedChild(f.parent, 2).ID, "now check the tests")))
		last := f.streamer.reqs[0].Messages
		Expect(last[len(last)-1]).To(Equal(provider.Message{Role: "user", Content: "now check the tests"}))
	})

	It("resumes a child paused by a budget stop", func() {
		f := newDelegateFixture(finishText("ok"))
		c := f.seedChild(f.parent, 2)
		check(pauseExecLoopRun(ctx, f.st, c.ID))
		res := must(ResumeDelegatedChild(ctx, f.resumeRequest(c.ID, "")))
		Expect(res.Status).To(Equal(DelegateStatusCompleted))
	})

	It("records session.resumed for a paused child", func() {
		f := newDelegateFixture(finishText("ok"))
		c := f.seedChild(f.parent, 2)
		check(pauseExecLoopRun(ctx, f.st, c.ID))
		must(ResumeDelegatedChild(ctx, f.resumeRequest(c.ID, "")))
		Expect(f.events(c.ID, "session.resumed")).To(HaveLen(1))
	})

	It("marks a child that crashed mid-turn as interrupted before it continues", func() {
		f := newDelegateFixture(finishText("ok"))
		c := f.seedChild(f.parent, 1)
		w := must(f.st.NewEventWriter(ctx, c.ID))
		must(w.Append(ctx, session.ActorUser, session.TypeTurnStart, map[string]any{"task": "again"}))
		check(w.Close())
		must(ResumeDelegatedChild(ctx, f.resumeRequest(c.ID, "")))
		Expect(f.events(c.ID, "session.interrupted")).To(HaveLen(1))
	})

	It("records session.interrupted for a turn cut off after a delta", func() {
		f := newDelegateFixture(finishText("ok"))
		c := f.seedChild(f.parent, 1)
		mustAppendEvent(ctx, f.st, c.ID, "engine", TypeAssistantDelta, map[string]any{"delta": "x"})
		must(ResumeDelegatedChild(ctx, f.resumeRequest(c.ID, "")))
		Expect(f.events(c.ID, "session.interrupted")).To(HaveLen(1))
	})

	It("runs without a pool or a reservation when the parent is unlimited", func() {
		f := newDelegateFixture(finishText("ok"))
		c := f.seedChild(f.parent, 2)
		f.pool = nil
		res := must(ResumeDelegatedChild(ctx, f.resumeRequest(c.ID, "")))
		Expect(res.Status).To(Equal(DelegateStatusCompleted))
	})
})

var _ = DescribeTable("countTurns",
	func(types []string, want int64) {
		events := make([]store.RecordedEvent, len(types))
		for i, t := range types {
			events[i] = store.RecordedEvent{Type: t}
		}
		got, _ := countTurns(events)
		Expect(got).To(Equal(want))
	},
	Entry("a delta without a message is one turn", []string{"turn.start", "assistant.delta"}, int64(1)),
	Entry("a tool result as the last event charges no turn that never began",
		[]string{"turn.start", "assistant.delta", "assistant.message", "tool.call", "tool.result"}, int64(1)),
	Entry("an opening turn.start alone charges nothing", []string{"turn.start"}, int64(0)),
	Entry("a delta after the last message is a second turn",
		[]string{"turn.start", "assistant.message", "tool.call", "tool.result", "assistant.delta"}, int64(2)),
	Entry("a call without a result, in a turn that wrote no message, is one turn",
		[]string{"turn.start", "tool.call"}, int64(1)),
	Entry("a call without a result after its message charges nothing more",
		[]string{"turn.start", "assistant.delta", "assistant.message", "tool.call"}, int64(1)),
	Entry("a completed answer charges nothing extra",
		[]string{"turn.start", "assistant.delta", "assistant.message", "turn.end"}, int64(1)),
	Entry("a budget stop after a tool turn charges nothing extra",
		[]string{"assistant.message", "tool.call", "tool.result", "turn.end"}, int64(1)),
	Entry("three crashes mid-turn charge three turns for good, under max_turns 5",
		[]string{"turn.start", "assistant.delta", "session.interrupted", "assistant.delta", "session.interrupted",
			"assistant.delta", "session.interrupted"}, int64(3)),
)

// provCallToolRead is a call the resumed child's profile allows.
func provCallToolRead() provider.ToolCall { return provCallToolReadID("r1") }

// provCallToolReadID is provCallToolRead with the call's id given.
func provCallToolReadID(id string) provider.ToolCall {
	return provider.ToolCall{ID: id, Name: "fs.read", Arguments: []byte(`{"path":"absent.txt"}`)}
}

// bigReadTurn is a call the resumed child's profile allows, after a message that holds more than the
// default keep_recent (20000 tokens), reporting a context of 90000: over the default line of a
// window of 100000, with a cut before the turn.
func bigReadTurn(id string) []provider.StreamEvent {
	turn := append([]provider.StreamEvent{provText(strings.Repeat("a", 200000))}, usedCall(provCallToolReadID(id), 90000)...)
	return turn
}

var _ = Describe("ResumeDelegatedChild under a judged handle", func() {
	var ctx context.Context
	BeforeEach(func() { ctx = context.Background() })

	It("runs: the loop takes no lease of its own, which would conflict with the claim", func() {
		f := newDelegateFixture(finishText("ok"))
		res := must(ResumeDelegatedChild(ctx, f.resumeRequest(f.seedChild(f.parent, 2).ID, "")))
		Expect(res.Status).To(Equal(DelegateStatusCompleted))
	})

	Describe("while the resumed child runs", func() {
		var busy bool
		var owner string
		BeforeEach(func() {
			f := newDelegateFixture()
			id := f.seedChild(f.parent, 2).ID
			ps := &probeStreamer{execLoopStreamer: execLoopStreamer{turns: [][]provider.StreamEvent{finishText("ok")}}}
			ps.probe = func() { busy, owner = leaseBusy(f.st, id), leaseBusyOwner(f.st, id) }
			rr := f.resumeRequest(id, "")
			rr.Streamer = ps
			must(ResumeDelegatedChild(ctx, rr))
		})
		It("refuses a second acquire of the child's item", func() { Expect(busy).To(BeTrue()) })
		It("names a fresh delegate owner as the holder", func() { Expect(owner).To(HavePrefix(delegateLeasePrefix)) })
		It("names an owner other than the one that seeded the child", func() {
			Expect(owner).NotTo(Equal(delegateLeasePrefix + "seed"))
		})
	})

	DescribeTable("is still held when the resumed child's settlement writes its status",
		func(status string, turns ...[]provider.StreamEvent) {
			f := newDelegateFileFixture(turns...)
			id := f.seedChild(f.parent, 2).ID
			installLeaseProbe(f.dbPath, status)
			must(ResumeDelegatedChild(ctx, f.resumeRequest(id, "")))
			Expect(probedLeaseRows(f.dbPath, id)).To(Equal([]int{1}))
		},
		Entry("completed", "completed", finishText("ok")),
		Entry("failed", "failed", []provider.StreamEvent{provError("upstream down")}),
	)

	It("leaves no lease row once the resumed child has finished", func() {
		f := newDelegateFixture(finishText("ok"))
		id := f.seedChild(f.parent, 2).ID
		must(ResumeDelegatedChild(ctx, f.resumeRequest(id, "")))
		Expect(f.st.LeaseToken(ctx, "exec:"+id)).To(BeZero())
	})

	It("lets a following resume of a finished-active child claim the item again", func() {
		f := newDelegateFixture([]provider.StreamEvent{provError("upstream down")})
		id := f.seedChild(f.parent, 2).ID
		must(ResumeDelegatedChild(ctx, f.resumeRequest(id, "")))
		Expect(leaseBusy(f.st, id)).To(BeFalse())
	})

	It("refuses a live judged holder as running", func() {
		f := newDelegateFixture(finishText("never"))
		id := f.seedChild(f.parent, 2).ID
		held := must(f.st.AcquireRunLease(ctx, "exec:"+id, delegateLeasePrefix+"live"))
		DeferCleanup(func() { _ = held.Release(context.Background()) })
		_, err := ResumeDelegatedChild(ctx, f.resumeRequest(id, ""))
		Expect(err).To(MatchError(ContainSubstring("is running")))
	})

	It("leaves a live judged holder's row in place when it refuses", func() {
		f := newDelegateFixture(finishText("never"))
		id := f.seedChild(f.parent, 2).ID
		held := must(f.st.AcquireRunLease(ctx, "exec:"+id, delegateLeasePrefix+"live"))
		DeferCleanup(func() { _ = held.Release(context.Background()) })
		_, _ = ResumeDelegatedChild(ctx, f.resumeRequest(id, ""))
		Expect(held.Fence(ctx)).To(Succeed())
	})
})
