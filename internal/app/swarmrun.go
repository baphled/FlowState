package app

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/storage"
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
	rec, err := storage.NewSwarmReceipts(db.db())
	if err != nil {
		return swarm.RunResult{}, fmt.Errorf("app: initialise swarm persistence: %w", err)
	}
	ev := &runEventsTo{rec: rec, runID: m.Name}
	res, err := swarm.Run(m, swarm.NewAdmissionLog(), swarm.NewGateRunner(), ev, rec)
	if err != nil {
		return res, err
	}
	if !res.Gates.Accepted {
		return res, fmt.Errorf("app: gates failed: %v", res.Gates.FailedGateNames())
	}
	return res, nil
}

// PacketAdmitted persists one admitted packet.
func (e *runEventsTo) PacketAdmitted(p swarm.Packet) {
	_ = e.rec.RecordAdmission(e.runID, p, true, "")
}

// PacketRejected persists one rejected packet with its reason.
func (e *runEventsTo) PacketRejected(id, reason string) {
	_ = e.rec.RecordAdmission(e.runID, swarm.Packet{ID: id}, false, reason)
}

// Compile-time interface checks for the run-loop sinks.
var (
	_ swarm.RunEvents   = (*runEventsTo)(nil)
	_ swarm.RunReceipts = (*storage.SwarmReceipts)(nil)
)
