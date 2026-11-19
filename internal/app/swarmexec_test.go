package app_test

import (
	"context"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
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
			}{true, []string{"p1", "p2"}, []int64{40}, 40}))
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

// packetIDsOf flattens the result's children packet ids in order.
func packetIDsOf(children []app.SwarmChild) []string {
	out := make([]string, 0, len(children))
	for _, c := range children {
		out = append(out, c.PacketID)
	}
	return out
}
