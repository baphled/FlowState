package app

import (
	"context"
	"database/sql"
	"fmt"

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
	rec := &dbRunReceipts{db: db.db()}
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

// RecordAdmission persists one admission decision row.
func (r *dbRunReceipts) RecordAdmission(runID string, p swarm.Packet, admitted bool, reason string) error {
	outcome := "reject"
	if admitted {
		outcome = "admit"
	}
	_, err := r.db.Exec(
		`INSERT INTO swarm_admissions (run_id, packet_id, outcome, reason, recorded_at_utc)
		 VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		runID, p.ID, outcome, reason)
	if err != nil {
		return fmt.Errorf("app: record admission: %w", err)
	}
	return nil
}

// RecordGates persists one receipt row per gate outcome.
func (r *dbRunReceipts) RecordGates(runID string, d swarm.GatesDecision) error {
	for _, o := range d.Outcomes {
		outcome := "fail"
		if o.Passed {
			outcome = "pass"
		}
		if _, err := r.db.Exec(
			`INSERT INTO swarm_gate_receipts (run_id, gate_name, outcome, recorded_at_utc)
			 VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
			runID, o.Gate.Name, outcome); err != nil {
			return fmt.Errorf("app: record gate receipt: %w", err)
		}
	}
	return nil
}

// MigrateSwarmTables creates the swarm run tables if absent; called
// by storage migration wiring when the receipts database opens.
func MigrateSwarmTables(db *sql.DB) error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS swarm_admissions (
			run_id TEXT NOT NULL,
			packet_id TEXT NOT NULL,
			outcome TEXT NOT NULL CHECK (outcome IN ('admit','reject')),
			reason TEXT NOT NULL,
			recorded_at_utc TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS swarm_gate_receipts (
			run_id TEXT NOT NULL,
			gate_name TEXT NOT NULL,
			outcome TEXT NOT NULL CHECK (outcome IN ('pass','fail')),
			recorded_at_utc TEXT NOT NULL
		)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("app: migrate swarm tables: %w", err)
		}
	}
	return nil
}

// Compile-time interface checks for the run-loop sinks.
var (
	_ swarm.RunEvents   = (*runEventsTo)(nil)
	_ swarm.RunReceipts = (*dbRunReceipts)(nil)
)
