package app_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/swarm"
)

// admitAll is the always-accept admission stub for execution specs.
type admitAll struct{}

// Admit accepts every packet unconditionally.
func (admitAll) Admit(swarm.Packet, []byte) error { return nil }

// rejectAll refuses every packet (admission-failure fixture).
type rejectAll struct{}

// Admit rejects every packet with the fixture reason.
func (rejectAll) Admit(swarm.Packet, []byte) error {
	return errors.New("fixture rejection")
}

// openSwarmExecStore opens an in-memory store; panics are setup failures.
func openSwarmExecStore() *store.Store {
	st, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = st.Close() })
	return st
}

// swarmExecParent creates the parent session that anchors the swarm.
func swarmExecParent(ctx context.Context, st *store.Store) *store.Session {
	sess, err := app.CreateSession(ctx, st, "probe.ai", "probe-model",
		app.DefaultHarness, "probe-config-sha", "swarm parent task")
	if err != nil {
		panic(err)
	}
	return sess
}

// swarmExecManifest builds the two-packet, one-gate fixture; p2 depends
// on p1 while manifest order lists p2 first, so RunOrder must reorder.
func swarmExecManifest() swarm.Manifest {
	return swarm.Manifest{
		Name:  "demo.swarm",
		Scope: "demo.swarm",
		Packets: []swarm.Packet{
			{ID: "p2", Scope: "demo.swarm.build", Task: "build it",
				SourceSHA256:  swarm.SHA256HexForTest([]byte("build it")),
				Prerequisites: []string{"p1"}},
			{ID: "p1", Scope: "demo.swarm.plan", Task: "plan the work",
				SourceSHA256: swarm.SHA256HexForTest([]byte("plan the work"))},
		},
		Gates: []swarm.Gate{{Name: "go", Description: "go build", Command: "true"}},
	}
}

// execChildView flattens the created children for one Equal.
type execChildView struct {
	PacketID, ParentID, Provider, ModelID string
}

// execChildrenView maps the result's children onto the flat view.
func execChildrenView(parentID string, children []app.SwarmChild) []execChildView {
	out := make([]execChildView, 0, len(children))
	for _, c := range children {
		out = append(out, execChildView{
			PacketID: c.PacketID, ParentID: c.Session.ParentID,
			Provider: c.Session.Provider, ModelID: c.Session.ModelID,
		})
	}
	return out
}

var _ = Describe("RunSwarm", func() {
	var ctx context.Context
	BeforeEach(func() { ctx = context.Background() })

	It("creates one child session per packet in RunOrder", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		res, err := app.RunSwarm(ctx, st, parent, swarmExecManifest(), 0, admitAll{}, swarm.NewGateRunner())
		Expect(err).NotTo(HaveOccurred())
		Expect(execChildrenView(parent.ID, res.Children)).To(Equal([]execChildView{
			{PacketID: "p1", ParentID: parent.ID, Provider: "probe.ai", ModelID: "probe-model"},
			{PacketID: "p2", ParentID: parent.ID, Provider: "probe.ai", ModelID: "probe-model"},
		}))
	})

	It("records budget reservations against the parent pool", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		if err := st.SetBudgetPool(ctx, parent.ID, 100); err != nil {
			panic(err)
		}
		res, err := app.RunSwarm(ctx, st, parent, swarmExecManifest(), 40, admitAll{}, swarm.NewGateRunner())
		Expect(err).NotTo(HaveOccurred())
		snap, snapErr := st.BudgetSnapshot(ctx, parent.ID)
		Expect(snapErr).NotTo(HaveOccurred())
		Expect(struct {
			Granted          []int64
			Pool, Avail, Res int64
		}{grantedOf(res), *snap.Pool, *snap.Available, snap.Reserved}).
			To(Equal(struct {
				Granted          []int64
				Pool, Avail, Res int64
			}{[]int64{40, 40}, 100, 20, 80}))
	})

	It("skips reservations when the budget share is not positive", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		if err := st.SetBudgetPool(ctx, parent.ID, 100); err != nil {
			panic(err)
		}
		res, err := app.RunSwarm(ctx, st, parent, swarmExecManifest(), 0, admitAll{}, swarm.NewGateRunner())
		Expect(err).NotTo(HaveOccurred())
		Expect(grantedOf(res)).To(BeEmpty())
	})

	It("attributes each child to its packet with a steering message", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		res, err := app.RunSwarm(ctx, st, parent, swarmExecManifest(), 0, admitAll{}, swarm.NewGateRunner())
		Expect(err).NotTo(HaveOccurred())
		msgs, mErr := st.PendingSteeringMessages(ctx, res.Children[0].Session.ID)
		if mErr != nil || len(msgs) != 1 {
			panic("the child recorded no single attribution message")
		}
		Expect(struct {
			Sender, Body string
		}{msgs[0].Sender, msgs[0].Body}).To(Equal(struct {
			Sender, Body string
		}{parent.ID, "packet p1 admitted"}))
	})

	It("persists the durable gate run with its admission rows", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		res, err := app.RunSwarm(ctx, st, parent, swarmExecManifest(), 0, admitAll{}, swarm.NewGateRunner())
		Expect(err).NotTo(HaveOccurred())
		runs, rErr := st.SwarmRunsFor(ctx, "demo.swarm")
		if rErr != nil || len(runs) != 1 {
			panic("the run persisted no single record")
		}
		Expect(struct {
			Admitted []string
			Outcome  string
			Packets  []store.SwarmRunPacket
		}{res.Admitted, runs[0].Outcome, runs[0].Packets}).To(Equal(struct {
			Admitted []string
			Outcome  string
			Packets  []store.SwarmRunPacket
		}{[]string{"p1", "p2"}, store.SwarmRunOutcomePass,
			[]store.SwarmRunPacket{{PacketID: "p1", Admitted: true}, {PacketID: "p2", Admitted: true}}}))
	})

	It("pins the executed manifest digest on the durable run", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		if _, err := app.RunSwarm(ctx, st, parent, swarmExecManifest(), 0, admitAll{}, swarm.NewGateRunner()); err != nil {
			panic(err)
		}
		mutated := swarmExecManifest()
		mutated.Packets[1].Task = "plan the work again"
		if _, err := app.RunSwarm(ctx, st, parent, mutated, 0, admitAll{}, swarm.NewGateRunner()); err != nil {
			panic(err)
		}
		runs, rErr := st.SwarmRunsFor(ctx, "demo.swarm")
		Expect(rErr).NotTo(HaveOccurred())
		Expect(struct {
			Count      int
			SameDigest bool
		}{len(runs), runs[0].ManifestSHA256 == runs[1].ManifestSHA256}).
			To(Equal(struct {
				Count      int
				SameDigest bool
			}{2, false}))
	})

	// The aborted run's children are cancelled and their grants settled, so
	// nothing stays reserved against the pool.
	It("aborts with a usage error when the pool cannot fund a child", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		if err := st.SetBudgetPool(ctx, parent.ID, 50); err != nil {
			panic(err)
		}
		res, err := app.RunSwarm(ctx, st, parent, swarmExecManifest(), 40, admitAll{}, swarm.NewGateRunner())
		snap, snapErr := st.BudgetSnapshot(ctx, parent.ID)
		if snapErr != nil {
			panic(snapErr)
		}
		Expect(struct {
			UsageErr  bool
			PacketIDs []string
			Granted   []int64
			Reserved  int64
		}{errors.Is(err, app.ErrUsage), packetIDsOf(res.Children), grantedOf(res), snap.Reserved}).
			To(Equal(struct {
				UsageErr  bool
				PacketIDs []string
				Granted   []int64
				Reserved  int64
			}{true, []string{"p1", "p2"}, []int64{40}, 0}))
	})

	It("aborts before creating children on an invalid manifest", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		invalid := swarmExecManifest()
		invalid.Name = ""
		res, err := app.RunSwarm(ctx, st, parent, invalid, 40, admitAll{}, swarm.NewGateRunner())
		sessions, sErr := st.ListSessions(ctx, "")
		runs, rErr := st.SwarmRunsFor(ctx, "")
		if sErr != nil || rErr != nil {
			panic("listing must succeed after an aborted run")
		}
		Expect(struct {
			Errored  bool
			Children int
			Sessions int
			Runs     int
		}{err != nil, len(res.Children), len(sessions), len(runs)}).
			To(Equal(struct {
				Errored  bool
				Children int
				Sessions int
				Runs     int
			}{true, 0, 1, 0}))
	})

	It("keeps created children and reports failure when a gate fails", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		broken := swarmExecManifest()
		broken.Gates = []swarm.Gate{{Name: "broken", Description: "b", Command: "false"}}
		res, err := app.RunSwarm(ctx, st, parent, broken, 0, admitAll{}, swarm.NewGateRunner())
		runs, rErr := st.SwarmRunsFor(ctx, "demo.swarm")
		if rErr != nil || len(runs) != 1 {
			panic("the failed run persisted no single record")
		}
		Expect(struct {
			NotAccepted bool
			PacketIDs   []string
			Outcome     string
		}{strings.Contains(err.Error(), "not accepted"), packetIDsOf(res.Children), runs[0].Outcome}).
			To(Equal(struct {
				NotAccepted bool
				PacketIDs   []string
				Outcome     string
			}{true, []string{"p1", "p2"}, store.SwarmRunOutcomeFail}))
	})

	Context("when the caller cancels during a gate", func() {
		var (
			st  *store.Store
			err error
		)

		BeforeEach(func() {
			st = openSwarmExecStore()
			parent := swarmExecParent(ctx, st)
			cctx, cancel := context.WithCancel(ctx)
			defer cancel()
			gates := swarm.NewGateRunnerWith(func(context.Context, string, []string) error {
				cancel()
				return nil
			})
			m := swarmExecManifest()
			m.Gates = append(m.Gates, swarm.Gate{Name: "second", Command: "second"})
			_, err = app.RunSwarm(cctx, st, parent, m, 0, admitAll{}, gates)
		})

		It("maps the cancellation onto the interrupt error", func() {
			Expect(err).To(MatchError(app.ErrSessionCancelled))
		})

		It("records the run as failed", func() {
			runs, listErr := st.SwarmRunsFor(ctx, "demo.swarm")
			if listErr != nil || len(runs) != 1 {
				panic("the cancelled run persisted no single record")
			}
			Expect(runs[0].Outcome).To(Equal(store.SwarmRunOutcomeFail))
		})
	})

	Context("when the caller cancels a run whose children exist", func() {
		var (
			st       *store.Store
			parent   *store.Session
			res      app.SwarmExecResult
			finished string
			share    int64
		)

		BeforeEach(func() { share = 0 })

		// runCancelled cancels the run during its first gate, with the
		// sigint cause; finishFirst completes one child beforehand.
		runCancelled := func(finishFirst bool) {
			st = openSwarmExecStore()
			parent = swarmExecParent(ctx, st)
			if share > 0 {
				if err := st.SetBudgetPool(ctx, parent.ID, 100); err != nil {
					panic(err)
				}
			}
			cctx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)
			gates := swarm.NewGateRunnerWith(func(context.Context, string, []string) error {
				if finishFirst {
					finished = childIDsOf(ctx, st, parent.ID)[0]
					if err := st.UpdateSessionStatus(ctx, finished, "completed"); err != nil {
						panic(err)
					}
				}
				cancel(&app.CancelCause{Reason: "sigint"})
				return nil
			})
			m := swarmExecManifest()
			m.Gates = append(m.Gates, swarm.Gate{Name: "second", Command: "second"})
			var err error
			res, err = app.RunSwarm(cctx, st, parent, m, share, admitAll{}, gates)
			if !errors.Is(err, app.ErrSessionCancelled) || len(res.Children) != 2 {
				panic("the run did not cancel after creating its children")
			}
		}

		Context("and none had finished", func() {
			BeforeEach(func() { runCancelled(false) })

			It("leaves every child cancelled", func() {
				Expect(childStatuses(ctx, st, res.Children)).To(Equal([]string{"cancelled", "cancelled"}))
			})

			It("records each child's cancelled event no later than its cancelled status", func() {
				Expect(statusNotBeforeLastEvent(ctx, st, res.Children)).To(Equal([]bool{true, true}))
			})

			It("appends the cancelled event with the parent's reason", func() {
				Expect(lastEventPayloads(ctx, st, res.Children)).To(Equal([]string{
					session.TypeSessionCancelled + `:{"reason":"sigint","status":"cancelled"}`,
					session.TypeSessionCancelled + `:{"reason":"sigint","status":"cancelled"}`,
				}))
			})

			It("names the created children on the stored run", func() {
				runs, err := st.SwarmRunsFor(ctx, "demo.swarm")
				if err != nil || len(runs) != 1 {
					panic("the cancelled run persisted no single record")
				}
				Expect(runs[0].Children).To(Equal(storedChildren(res.Children)))
			})
		})

		Context("and each child holds a budget reservation", func() {
			BeforeEach(func() {
				share = 10
				runCancelled(false)
			})

			It("settles every cancelled child's reservation", func() {
				Expect(reservationStatuses(ctx, st, res.Children)).To(Equal([]string{"charged", "charged"}))
			})
		})

		Context("and the cancellation lands while the children are being created", func() {
			var runs []store.SwarmRun

			BeforeEach(func() {
				st = openSwarmExecStore()
				parent = swarmExecParent(ctx, st)
				// Cancelled once one child exists: the run is cut short with
				// exactly that child, whether the cancellation is first seen
				// while it is being attributed or as the second is refused.
				cctx := newCancelledWhen(hasChild(st, parent.ID))
				var err error
				res, err = app.RunSwarm(cctx, st, parent, swarmExecManifest(), 0, admitAll{}, swarm.NewGateRunner())
				if !errors.Is(err, app.ErrSessionCancelled) || len(res.Children) != 1 {
					panic(fmt.Sprintf("the run was not cut short after its first child: %v, %d children", err, len(res.Children)))
				}
				if runs, err = st.SwarmRunsFor(ctx, "demo.swarm"); err != nil {
					panic(err)
				}
			})

			It("cancels the child that had been created", func() {
				Expect(childStatuses(ctx, st, res.Children)).To(Equal([]string{"cancelled"}))
			})

			It("stores exactly one run", func() {
				Expect(runs).To(HaveLen(1))
			})

			It("names the created child on the stored run", func() {
				Expect(runs[0].Children).To(Equal(storedChildren(res.Children)))
			})

			It("stores the run as failed", func() {
				Expect(runs[0].Outcome).To(Equal("fail"))
			})
		})

		Context("and one had completed", func() {
			BeforeEach(func() { runCancelled(true) })

			It("keeps the completed child's status", func() {
				got, err := st.GetSession(ctx, finished)
				if err != nil {
					panic(err)
				}
				Expect(got.Status).To(Equal("completed"))
			})

			It("cancels the unfinished child", func() {
				Expect(unfinishedStatuses(ctx, st, res.Children, finished)).To(Equal([]string{"cancelled"}))
			})
		})
	})

	Context("when child creation fails part-way for a reason other than cancellation", func() {
		var (
			st   *store.Store
			res  app.SwarmExecResult
			err  error
			runs []store.SwarmRun
		)

		BeforeEach(func() {
			st = openSwarmExecStore()
			parent := swarmExecParent(ctx, st)
			// A pool of 15 holds one 10-unit share: the second reservation fails.
			if perr := st.SetBudgetPool(ctx, parent.ID, 15); perr != nil {
				panic(perr)
			}
			res, err = app.RunSwarm(ctx, st, parent, swarmExecManifest(), 10, admitAll{}, swarm.NewGateRunner())
			if !errors.Is(err, app.ErrUsage) || len(res.Children) != 2 || len(res.Reservations) != 1 {
				panic(fmt.Sprintf("the run did not fail at the second reservation: %v, %d children", err, len(res.Children)))
			}
			var rerr error
			if runs, rerr = st.SwarmRunsFor(ctx, "demo.swarm"); rerr != nil {
				panic(rerr)
			}
		})

		It("keeps the original error class", func() {
			Expect(app.ErrorCode(err)).To(Equal(app.CodeUsage))
		})

		It("cancels every child already created", func() {
			Expect(childStatuses(ctx, st, res.Children)).To(Equal([]string{"cancelled", "cancelled"}))
		})

		It("records why they were cancelled", func() {
			Expect(lastEventPayloads(ctx, st, res.Children)).To(Equal([]string{
				session.TypeSessionCancelled + `:{"reason":"swarm_aborted","status":"cancelled"}`,
				session.TypeSessionCancelled + `:{"reason":"swarm_aborted","status":"cancelled"}`,
			}))
		})

		It("settles the reservation that was made", func() {
			Expect(reservationStatuses(ctx, st, res.Children[:1])).To(Equal([]string{"charged"}))
		})

		It("stores a failed run naming the children", func() {
			Expect(runs).To(HaveLen(1))
		})

		It("stores the run as failed", func() {
			Expect(runs[0].Outcome).To(Equal("fail"))
		})

		It("names every child on the stored run", func() {
			Expect(runs[0].Children).To(Equal(storedChildren(res.Children)))
		})
	})

	It("maps a one-gate run cancelled mid-gate onto the interrupt error", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		cctx, cancel := context.WithCancel(ctx)
		defer cancel()
		gates := swarm.NewGateRunnerWith(func(context.Context, string, []string) error {
			cancel()
			return errors.New("signal: killed")
		})
		_, err := app.RunSwarm(cctx, st, parent, swarmExecManifest(), 0, admitAll{}, gates)
		Expect(err).To(MatchError(app.ErrSessionCancelled))
	})

	It("maps a cancellation during child creation onto the interrupt error", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := app.RunSwarm(cctx, st, parent, swarmExecManifest(), 0, admitAll{}, swarm.NewGateRunner())
		Expect(err).To(MatchError(app.ErrSessionCancelled))
	})

	It("matches ErrSwarmNotAccepted for a failed gate", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		broken := swarmExecManifest()
		broken.Gates = []swarm.Gate{{Name: "broken", Description: "b", Command: "false"}}
		_, err := app.RunSwarm(ctx, st, parent, broken, 0, admitAll{}, swarm.NewGateRunner())
		Expect(err).To(MatchError(app.ErrSwarmNotAccepted))
	})

	It("names the failed gate and classes it receipt_invalid", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		broken := swarmExecManifest()
		broken.Gates = []swarm.Gate{{Name: "broken", Description: "b", Command: "false"}}
		_, err := app.RunSwarm(ctx, st, parent, broken, 0, admitAll{}, swarm.NewGateRunner())
		Expect(struct {
			Code  string
			Named bool
		}{app.ErrorCode(err), strings.Contains(err.Error(), "failed gates [broken]")}).
			To(Equal(struct {
				Code  string
				Named bool
			}{app.CodeReceiptInvalid, true}))
	})

	It("matches ErrSwarmNotAccepted for a rejected packet", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		_, err := app.RunSwarm(ctx, st, parent, swarmExecManifest(), 0, rejectAll{}, swarm.NewGateRunner())
		Expect(err).To(MatchError(app.ErrSwarmNotAccepted))
	})

	It("names the rejected packets in the error", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		_, err := app.RunSwarm(ctx, st, parent, swarmExecManifest(), 0, rejectAll{}, swarm.NewGateRunner())
		Expect(err).To(MatchError(ContainSubstring("rejected packets [p1 p2]")))
	})

	It("reports admission rejection without discarding children", func() {
		st := openSwarmExecStore()
		parent := swarmExecParent(ctx, st)
		res, err := app.RunSwarm(ctx, st, parent, swarmExecManifest(), 0, rejectAll{}, swarm.NewGateRunner())
		Expect(struct {
			NotAccepted bool
			PacketIDs   []string
			Admitted    int
		}{strings.Contains(err.Error(), "not accepted"), packetIDsOf(res.Children), len(res.Admitted)}).
			To(Equal(struct {
				NotAccepted bool
				PacketIDs   []string
				Admitted    int
			}{true, []string{"p1", "p2"}, 0}))
	})
})

// grantedOf flattens the result's reservation grants in child order.
func grantedOf(res app.SwarmExecResult) []int64 {
	out := make([]int64, 0, len(res.Reservations))
	for _, r := range res.Reservations {
		out = append(out, r.Granted)
	}
	return out
}

var _ = Describe("child session creation under cancellation", func() {
	var (
		ctx    context.Context
		st     *store.Store
		parent *store.Session
	)

	BeforeEach(func() {
		ctx = context.Background()
		st = openSwarmExecStore()
		parent = swarmExecParent(ctx, st)
	})

	It("creates nothing when the context is already cancelled", func() {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, _ = app.CreateChildSession(cctx, st, parent.ID, "zai", "glm-5.3", "", "cfg", "the task")
		Expect(childIDsOf(ctx, st, parent.ID)).To(BeEmpty())
	})

	It("reports the cancellation, not a usage error, for a cancelled context", func() {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := app.CreateChildSession(cctx, st, parent.ID, "zai", "glm-5.3", "", "cfg", "the task")
		Expect(err).To(MatchError(context.Canceled))
	})

	It("gives the child its task even when the cancellation lands as its row is written", func() {
		cctx := newCancelledWhen(hasChild(st, parent.ID))
		_, _ = app.CreateChildSession(cctx, st, parent.ID, "zai", "glm-5.3", "", "cfg", "the task")
		ids := childIDsOf(ctx, st, parent.ID)
		if len(ids) != 1 {
			panic("no child was created")
		}
		evs, err := st.EventsAfter(ctx, ids[0], 0)
		if err != nil {
			panic(err)
		}
		Expect(evs).To(ContainElement(HaveField("Type", session.TypeTurnStart)))
	})
})

// hasChild reports whether the parent has a child session yet. It is the
// ready function of a cancelledWhen context, which database/sql also
// consults from its own goroutines, possibly after the spec has ended: it
// holds the store and id by value and treats a failed read as "not yet".
func hasChild(st *store.Store, parentID string) func() bool {
	return func() bool {
		all, err := st.ListSessions(context.Background(), "")
		if err != nil {
			return false
		}
		for _, s := range all {
			if s.ParentID == parentID {
				return true
			}
		}
		return false
	}
}

// cancelledWhen is a context that becomes cancelled the first time it is
// asked for its error after ready reports true, and stays cancelled: a
// cancellation that lands once a known state has been reached. Err is
// called from the code under test and from database/sql's goroutines, so
// ready must be safe to call from any goroutine at any time.
type cancelledWhen struct {
	context.Context
	ready func() bool
	once  sync.Once
	done  chan struct{}
}

func newCancelledWhen(ready func() bool) *cancelledWhen {
	return &cancelledWhen{Context: context.Background(), ready: ready, done: make(chan struct{})}
}

// Done is closed once the context has reported its cancellation.
func (c *cancelledWhen) Done() <-chan struct{} { return c.done }

// Err reports the cancellation from the first call after ready is true.
func (c *cancelledWhen) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
	}
	if !c.ready() {
		return nil
	}
	c.once.Do(func() { close(c.done) })
	return context.Canceled
}

// statusNotBeforeLastEvent reports, per child, whether its stored status
// was written no earlier than its final event.
func statusNotBeforeLastEvent(ctx context.Context, st *store.Store, children []app.SwarmChild) []bool {
	var out []bool
	for _, c := range children {
		evs, err := st.EventsAfter(ctx, c.Session.ID, 0)
		if err != nil || len(evs) == 0 {
			panic("child has no events")
		}
		got, err := st.GetSession(ctx, c.Session.ID)
		if err != nil {
			panic(err)
		}
		out = append(out, !got.UpdatedAt.Before(evs[len(evs)-1].CreatedAt))
	}
	return out
}

// reservationStatuses reads each child's reservation status.
func reservationStatuses(ctx context.Context, st *store.Store, children []app.SwarmChild) []string {
	var out []string
	for _, c := range children {
		r, err := st.ReservationFor(ctx, c.Session.ID)
		if err != nil {
			panic(err)
		}
		out = append(out, r.Status)
	}
	return out
}

// childIDsOf lists the ids of the parent's child sessions.
func childIDsOf(ctx context.Context, st *store.Store, parentID string) []string {
	all, err := st.ListSessions(ctx, "")
	if err != nil {
		panic(err)
	}
	var ids []string
	for _, s := range all {
		if s.ParentID == parentID {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// childStatuses reads each child's current stored status.
func childStatuses(ctx context.Context, st *store.Store, children []app.SwarmChild) []string {
	return unfinishedStatuses(ctx, st, children, "")
}

// unfinishedStatuses reads the stored status of every child except skip.
func unfinishedStatuses(ctx context.Context, st *store.Store, children []app.SwarmChild, skip string) []string {
	var out []string
	for _, c := range children {
		if c.Session.ID == skip {
			continue
		}
		got, err := st.GetSession(ctx, c.Session.ID)
		if err != nil {
			panic(err)
		}
		out = append(out, got.Status)
	}
	return out
}

// lastEventPayloads renders each child's final event as type:payload.
func lastEventPayloads(ctx context.Context, st *store.Store, children []app.SwarmChild) []string {
	var out []string
	for _, c := range children {
		evs, err := st.EventsAfter(ctx, c.Session.ID, 0)
		if err != nil || len(evs) == 0 {
			panic("child has no events")
		}
		last := evs[len(evs)-1]
		out = append(out, last.Type+":"+last.PayloadJSON)
	}
	return out
}

// storedChildren maps the result's children onto the stored shape.
func storedChildren(children []app.SwarmChild) []store.SwarmRunChild {
	var out []store.SwarmRunChild
	for _, c := range children {
		out = append(out, store.SwarmRunChild{PacketID: c.PacketID, SessionID: c.Session.ID})
	}
	return out
}

// packetIDsOf flattens the result's children packet ids in order.
func packetIDsOf(children []app.SwarmChild) []string {
	out := make([]string, 0, len(children))
	for _, c := range children {
		out = append(out, c.PacketID)
	}
	return out
}

var _ = Describe("ErrorCode for swarm outcomes", func() {
	DescribeTable("maps the error onto its class",
		func(err error, want string) {
			Expect(app.ErrorCode(err)).To(Equal(want))
		},
		Entry("not accepted is receipt_invalid",
			fmt.Errorf("run: %w", app.ErrSwarmNotAccepted), app.CodeReceiptInvalid),
		Entry("cancelled wins over not accepted",
			errors.Join(app.ErrSessionCancelled, app.ErrSwarmNotAccepted), app.CodeSessionCancelled),
		Entry("budget stop wins over not accepted",
			errors.Join(app.ErrBudgetExceeded, app.ErrSwarmNotAccepted), app.CodeBudgetExceeded),
		Entry("usage wins over not accepted",
			errors.Join(app.ErrUsage, app.ErrSwarmNotAccepted), app.CodeUsage),
		Entry("a plain storage failure stays internal",
			errors.New("disk I/O error"), app.CodeInternal),
	)
})
