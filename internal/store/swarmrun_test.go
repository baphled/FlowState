package store

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mustSwarmRunStore opens an in-memory store; panics are setup failures.
func mustSwarmRunStore() *Store {
	st, err := OpenMemory()
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = st.Close() })
	return st
}

// sampleSwarmRun builds a two-packet run fixture for the named swarm.
func sampleSwarmRun(name, outcome string) SwarmRun {
	return SwarmRun{
		Name:           name,
		ManifestSHA256: sha256Hex([]byte("manifest:" + name)),
		Outcome:        outcome,
		Packets: []SwarmRunPacket{
			{PacketID: "p1", Admitted: true},
			{PacketID: "p2", Admitted: false, Reason: "source hash mismatch"},
		},
	}
}

// recordSwarmRun persists a fixture run; panics are setup failures.
func recordSwarmRun(st *Store, run SwarmRun) SwarmRun {
	rec, err := st.RecordSwarmRun(context.Background(), run)
	if err != nil {
		panic(err)
	}
	return rec
}

// swarmRunRowCounts reads the persisted row totals straight from the
// tables; panics are setup failures.
func swarmRunRowCounts(st *Store) [2]int {
	var runs, packets int
	if err := st.db.QueryRow(`SELECT count(*) FROM swarm_runs`).Scan(&runs); err != nil {
		panic(err)
	}
	if err := st.db.QueryRow(`SELECT count(*) FROM swarm_run_packets`).Scan(&packets); err != nil {
		panic(err)
	}
	return [2]int{runs, packets}
}

// swarmRunRejection bundles a failed record's error and row counts.
type swarmRunRejection struct {
	Err  error
	Rows [2]int
}

// rejectSwarmRun records a fixture run expected to fail.
func rejectSwarmRun(st *Store, run SwarmRun) swarmRunRejection {
	_, err := st.RecordSwarmRun(context.Background(), run)
	if err == nil {
		panic("RecordSwarmRun unexpectedly succeeded")
	}
	return swarmRunRejection{Err: err, Rows: swarmRunRowCounts(st)}
}

var _ = Describe("durable swarm run records", func() {
	It("round-trips a recorded run and its packet rows through SwarmRunsFor", func() {
		st := mustSwarmRunStore()
		rec := recordSwarmRun(st, sampleSwarmRun("demo.swarm", SwarmRunOutcomePass))
		runs, err := st.SwarmRunsFor(context.Background(), "demo.swarm")
		if err != nil {
			panic(err)
		}
		if len(runs) != 1 {
			panic("SwarmRunsFor did not return the single recorded run")
		}
		type roundTrip struct {
			ID, Name, ManifestSHA256, Outcome string
			CreatedAt                         time.Time
			Packets                           []SwarmRunPacket
		}
		Expect(roundTrip{runs[0].ID, runs[0].Name, runs[0].ManifestSHA256,
			runs[0].Outcome, runs[0].CreatedAt, runs[0].Packets}).
			To(Equal(roundTrip{rec.ID, "demo.swarm", rec.ManifestSHA256,
				SwarmRunOutcomePass, rec.CreatedAt, []SwarmRunPacket{
					{PacketID: "p1", Admitted: true},
					{PacketID: "p2", Admitted: false, Reason: "source hash mismatch"},
				}}))
	})

	It("round-trips the children a run names, in recorded order", func() {
		st := mustSwarmRunStore()
		run := sampleSwarmRun("demo.swarm", SwarmRunOutcomeFail)
		run.Children = []SwarmRunChild{{PacketID: "p1", SessionID: "s-1"}, {PacketID: "p2", SessionID: "s-2"}}
		recordSwarmRun(st, run)
		runs, err := st.SwarmRunsFor(context.Background(), "demo.swarm")
		if err != nil || len(runs) != 1 {
			panic("SwarmRunsFor did not return the single recorded run")
		}
		Expect(runs[0].Children).To(Equal(run.Children))
	})

	It("rejects a child without a session id and writes no run", func() {
		st := mustSwarmRunStore()
		run := sampleSwarmRun("demo.swarm", SwarmRunOutcomePass)
		run.Children = []SwarmRunChild{{PacketID: "p1"}}
		got := rejectSwarmRun(st, run)
		Expect(got).To(Equal(swarmRunRejection{Err: ErrSwarmRunChildMissing}))
	})

	It("rolls back the run and its packet rows when a child insert fails", func() {
		st := mustSwarmRunStore()
		mustRawExec(st, `CREATE TRIGGER fail_child BEFORE INSERT ON swarm_run_children
			BEGIN SELECT RAISE(ABORT, 'forced child failure'); END`)
		run := sampleSwarmRun("demo.swarm", SwarmRunOutcomePass)
		run.Children = []SwarmRunChild{{PacketID: "p1", SessionID: "s-1"}}
		got := rejectSwarmRun(st, run)
		Expect(got.Rows).To(Equal([2]int{0, 0}))
	})

	It("persists no child row when a child insert fails", func() {
		st := mustSwarmRunStore()
		mustRawExec(st, `CREATE TRIGGER fail_child BEFORE INSERT ON swarm_run_children
			BEGIN SELECT RAISE(ABORT, 'forced child failure'); END`)
		run := sampleSwarmRun("demo.swarm", SwarmRunOutcomePass)
		run.Children = []SwarmRunChild{{PacketID: "p1", SessionID: "s-1"}}
		rejectSwarmRun(st, run)
		var children int
		if err := st.db.QueryRow(`SELECT count(*) FROM swarm_run_children`).Scan(&children); err != nil {
			panic(err)
		}
		Expect(children).To(Equal(0))
	})

	It("returns the runs of one swarm oldest first", func() {
		st := mustSwarmRunStore()
		first := recordSwarmRun(st, sampleSwarmRun("demo.swarm", SwarmRunOutcomeFail))
		second := recordSwarmRun(st, sampleSwarmRun("demo.swarm", SwarmRunOutcomePass))
		runs, err := st.SwarmRunsFor(context.Background(), "demo.swarm")
		if err != nil {
			panic(err)
		}
		Expect([]string{runs[0].ID, runs[1].ID}).To(Equal([]string{first.ID, second.ID}))
	})

	It("isolates runs by swarm name", func() {
		st := mustSwarmRunStore()
		alpha := recordSwarmRun(st, sampleSwarmRun("alpha.swarm", SwarmRunOutcomePass))
		recordSwarmRun(st, sampleSwarmRun("beta.swarm", SwarmRunOutcomePass))
		runs, err := st.SwarmRunsFor(context.Background(), "alpha.swarm")
		if err != nil {
			panic(err)
		}
		Expect(runs[0].ID).To(Equal(alpha.ID))
	})

	It("yields no runs for an unknown swarm name", func() {
		st := mustSwarmRunStore()
		runs, err := st.SwarmRunsFor(context.Background(), "missing.swarm")
		if err != nil {
			panic(err)
		}
		Expect(runs).To(BeEmpty())
	})

	It("rejects invalid runs and writes nothing", func() {
		st := mustSwarmRunStore()
		noDigest := sampleSwarmRun("demo.swarm", SwarmRunOutcomePass)
		noDigest.ManifestSHA256 = ""
		noPacketID := sampleSwarmRun("demo.swarm", SwarmRunOutcomePass)
		noPacketID.Packets = append(noPacketID.Packets, SwarmRunPacket{})
		Expect([]swarmRunRejection{
			rejectSwarmRun(st, sampleSwarmRun("", SwarmRunOutcomePass)),
			rejectSwarmRun(st, noDigest),
			rejectSwarmRun(st, sampleSwarmRun("demo.swarm", "maybe")),
			rejectSwarmRun(st, noPacketID),
		}).To(Equal([]swarmRunRejection{
			{Err: ErrSwarmRunNameMissing},
			{Err: ErrSwarmRunManifestMissing},
			{Err: ErrSwarmRunOutcomeInvalid},
			{Err: ErrSwarmRunPacketMissing},
		}))
	})
})
