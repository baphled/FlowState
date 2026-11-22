package app

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/swarm"
)

// SwarmRun drives one configured-swarm run from a manifest file:
// admission in dependency order, then every gate, with progress
// persisted to the store at dbPath. It is the cmd-facing seam so
// cmd imports only app.
func SwarmRun(ctx context.Context, dbPath, manifestPath string) (swarm.RunResult, error) {
	m, err := swarm.LoadManifest(manifestPath)
	if err != nil {
		return swarm.RunResult{}, err
	}
	return SwarmRunManifest(ctx, dbPath, m)
}

// SwarmRunManifest drives one in-memory manifest through the run
// loop with persistent admission and gate records.
func SwarmRunManifest(ctx context.Context, dbPath string, m swarm.Manifest) (swarm.RunResult, error) {
	db, err := openReceipts(dbPath)
	if err != nil {
		return swarm.RunResult{}, fmt.Errorf("app: open run store: %w", err)
	}
	defer func() { _ = db.Close() }()
	log, err := store.NewSwarmReceiptLog(db.db())
	if err != nil {
		return swarm.RunResult{}, fmt.Errorf("app: initialise swarm persistence: %w", err)
	}
	rec := &runReceiptLog{log: log}
	ev := &runEventsTo{rec: rec, runID: m.Name}
	res, err := swarm.Run(ctx, m, swarm.NewAdmissionLog(), swarm.NewGateRunner(), ev, rec)
	if err != nil {
		return res, mapSwarmRunError(err)
	}
	if !res.Gates.Accepted {
		return res, swarmNotAccepted(m, res)
	}
	return res, nil
}

// RecordAdmission persists one admission decision for the packet.
func (r *runReceiptLog) RecordAdmission(ctx context.Context, runID string, p swarm.Packet, admitted bool, reason string) error {
	return r.log.RecordAdmission(ctx, runID, p.ID, admitted, reason)
}

// RecordGates persists the gate outcomes of the decision in manifest order.
func (r *runReceiptLog) RecordGates(ctx context.Context, runID string, d swarm.GatesDecision) error {
	results := make([]store.GateResult, 0, len(d.Outcomes))
	for _, o := range d.Outcomes {
		results = append(results, store.GateResult{Name: o.Gate.Name, Passed: o.Passed})
	}
	return r.log.RecordGates(ctx, runID, results)
}

// PacketAdmitted persists one admitted packet.
func (e *runEventsTo) PacketAdmitted(ctx context.Context, p swarm.Packet) {
	_ = e.rec.RecordAdmission(ctx, e.runID, p, true, "")
}

// PacketRejected persists one rejected packet with its reason.
func (e *runEventsTo) PacketRejected(ctx context.Context, id, reason string) {
	_ = e.rec.RecordAdmission(ctx, e.runID, swarm.Packet{ID: id}, false, reason)
}

// Compile-time interface checks for the run-loop sinks.
var (
	_ swarm.RunEvents   = (*runEventsTo)(nil)
	_ swarm.RunReceipts = (*runReceiptLog)(nil)
)
