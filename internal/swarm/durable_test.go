package swarm_test

import (
	"context"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/swarm"
)

// mustDurableStore opens an in-memory store; panics are setup failures.
func mustDurableStore() *store.Store {
	st, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = st.Close() })
	return st
}

// durableManifest builds the two-packet fixture manifest behind one
// passing gate (gates_test style).
func durableManifest() swarm.Manifest {
	return swarm.Manifest{
		Name:  "demo.swarm",
		Scope: "demo.swarm",
		Packets: []swarm.Packet{
			{ID: "p1", Scope: "demo.swarm.plan", Task: "plan the work",
				SourceSHA256: bodyHashOf("plan the work")},
			{ID: "p2", Scope: "demo.swarm.build", Task: "build it",
				SourceSHA256: bodyHashOf("build it"), Prerequisites: []string{"p1"}},
		},
		Gates: []swarm.Gate{{Name: "go", Description: "go build", Command: "true"}},
	}
}

// durableManifestSHA pins the fixture digest the receipts carry.
func durableManifestSHA() string {
	return swarm.SHA256HexForTest([]byte("demo.swarm"))
}

// runDurable drives one real run through the durable receipts; a run
// error is the setup failure.
func runDurable(st *store.Store, m swarm.Manifest) swarm.RunResult {
	receipts := swarm.NewDurableReceipts(st, durableManifestSHA())
	res, err := swarm.Run(context.Background(), m, hashFor(), swarm.NewGateRunner(), nil, receipts)
	if err != nil {
		panic(err)
	}
	return res
}

// durableRejectionView flattens a rejected run's durable outcome.
type durableRejectionView struct {
	Accepted       bool
	Outcome        string
	Admitted       [2]bool
	MismatchReason bool
	PrereqReason   bool
}

// viewDurableRejection flattens the result and its single recorded
// run; panics are setup failures.
func viewDurableRejection(res swarm.RunResult, runs []store.SwarmRun) durableRejectionView {
	if len(runs) != 1 || len(runs[0].Packets) != 2 {
		panic("viewDurableRejection wants one run with two packets")
	}
	p := runs[0].Packets
	return durableRejectionView{
		Accepted: res.Gates.Accepted, Outcome: runs[0].Outcome,
		Admitted:       [2]bool{p[0].Admitted, p[1].Admitted},
		MismatchReason: strings.HasPrefix(p[0].Reason, "swarm: source hash mismatch"),
		PrereqReason:   strings.HasPrefix(p[1].Reason, "swarm: prerequisite not admitted"),
	}
}

var _ = Describe("durable run receipts", func() {
	It("feeds a real run and lands its rows in the store", func() {
		st := mustDurableStore()
		res := runDurable(st, durableManifest())
		runs, err := st.SwarmRunsFor(context.Background(), "demo.swarm")
		if err != nil {
			panic(err)
		}
		if len(runs) != 1 {
			panic("the run persisted no single record")
		}
		Expect(struct {
			Accepted                      bool
			Name, ManifestSHA256, Outcome string
			Packets                       []store.SwarmRunPacket
		}{res.Gates.Accepted, runs[0].Name, runs[0].ManifestSHA256, runs[0].Outcome, runs[0].Packets}).
			To(Equal(struct {
				Accepted                      bool
				Name, ManifestSHA256, Outcome string
				Packets                       []store.SwarmRunPacket
			}{true, "demo.swarm", durableManifestSHA(), store.SwarmRunOutcomePass,
				[]store.SwarmRunPacket{
					{PacketID: "p1", Admitted: true},
					{PacketID: "p2", Admitted: true},
				}}))
	})

	It("records rejected admissions with their reasons and a fail outcome", func() {
		st := mustDurableStore()
		m := durableManifest()
		m.Packets[0].SourceSHA256 = bodyHashOf("incorrect-source")
		res := runDurable(st, m)
		runs, err := st.SwarmRunsFor(context.Background(), "demo.swarm")
		if err != nil {
			panic(err)
		}
		Expect(viewDurableRejection(res, runs)).To(Equal(durableRejectionView{
			Admitted:       [2]bool{false, false},
			Outcome:        store.SwarmRunOutcomeFail,
			MismatchReason: true, PrereqReason: true,
		}))
	})

	It("fails the run closed when the store is unavailable", func() {
		st := mustDurableStore()
		if err := st.Close(); err != nil {
			panic(err)
		}
		receipts := swarm.NewDurableReceipts(st, durableManifestSHA())
		res, err := swarm.Run(context.Background(), durableManifest(), hashFor(), swarm.NewGateRunner(), nil, receipts)
		if err == nil {
			panic("Run unexpectedly succeeded without a store")
		}
		Expect(struct {
			PersistGatesFailed bool
			Accepted           bool
		}{strings.Contains(err.Error(), "persist gates"), res.Gates.Accepted}).
			To(Equal(struct {
				PersistGatesFailed bool
				Accepted           bool
			}{true, false}))
	})

	It("rejects admission persistence when no store is configured", func() {
		receipts := swarm.NewDurableReceipts(nil, durableManifestSHA())
		_, err := swarm.Run(context.Background(), durableManifest(), hashFor(), swarm.NewGateRunner(), nil, receipts)
		if err == nil {
			panic("Run unexpectedly succeeded without a store")
		}
		Expect(strings.Contains(err.Error(), "persist admission")).To(BeTrue())
	})
})

var _ = Describe("durable run receipts under cancellation", func() {
	It("does not write the run through a cancelled context", func() {
		receipts := swarm.NewDurableReceipts(mustDurableStore(), durableManifestSHA())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(receipts.RecordGates(ctx, "demo.swarm", swarm.GatesDecision{})).To(MatchError(context.Canceled))
	})

	It("lands a cancelled run as one fail run", func() {
		st := mustDurableStore()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		gates := swarm.NewGateRunnerWith(func(context.Context, string, []string) error {
			cancel()
			return nil
		})
		m := durableManifest()
		m.Gates = append(m.Gates, swarm.Gate{Name: "second", Command: "second"})
		receipts := swarm.NewDurableReceipts(st, durableManifestSHA())
		if _, err := swarm.Run(ctx, m, hashFor(), gates, nil, receipts); !errors.Is(err, context.Canceled) {
			panic("the run was not cancelled")
		}
		runs, err := st.SwarmRunsFor(context.Background(), "demo.swarm")
		if err != nil {
			panic(err)
		}
		if len(runs) != 1 {
			panic("the cancelled run persisted no single record")
		}
		Expect(runs[0].Outcome).To(Equal(store.SwarmRunOutcomeFail))
	})
})
