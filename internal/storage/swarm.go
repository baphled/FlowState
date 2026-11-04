package storage

import (
	"database/sql"
	"fmt"

	"github.com/baphled/flowstate/internal/swarm"
)

// NewSwarmReceipts binds a persistent swarm receipt sink to an open database.
// The caller owns the database lifecycle and must migrate the schema first.
func NewSwarmReceipts(db *sql.DB) *SwarmReceipts {
	return &SwarmReceipts{db: db}
}

// RecordAdmission persists one admitted or rejected packet for a run.
func (r *SwarmReceipts) RecordAdmission(runID string, p swarm.Packet, admitted bool, reason string) error {
	outcome := "reject"
	if admitted {
		outcome = "admit"
	}
	_, err := r.db.Exec(
		`INSERT INTO swarm_admissions (run_id, packet_id, outcome, reason, recorded_at_utc)
		 VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		runID, p.ID, outcome, reason)
	if err != nil {
		return fmt.Errorf("storage: record admission: %w", err)
	}
	return nil
}

// RecordGates persists one receipt for each gate outcome.
func (r *SwarmReceipts) RecordGates(runID string, d swarm.GatesDecision) error {
	for _, o := range d.Outcomes {
		outcome := "fail"
		if o.Passed {
			outcome = "pass"
		}
		if _, err := r.db.Exec(
			`INSERT INTO swarm_gate_receipts (run_id, gate_name, outcome, recorded_at_utc)
			 VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
			runID, o.Gate.Name, outcome); err != nil {
			return fmt.Errorf("storage: record gate receipt: %w", err)
		}
	}
	return nil
}

// RecordMemberReceipt persists one admitted member's receipt.
func (r *SwarmReceipts) RecordMemberReceipt(rec swarm.MemberReceipt) error {
	_, err := r.db.Exec(
		`INSERT INTO swarm_member_receipts (run_name, packet_id, gate_outcome, evidence_sha256, recorded_at_utc)
		 VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		rec.Identity.RunName, rec.Identity.PacketID, rec.GateOutcome, rec.EvidenceSHA256)
	if err != nil {
		return fmt.Errorf("storage: record member receipt: %w", err)
	}
	return nil
}

// MigrateSwarmTables creates the swarm run and receipt tables if missing.
// It is idempotent for databases that already contain these tables.
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
		`CREATE TABLE IF NOT EXISTS swarm_member_receipts (
			run_name TEXT NOT NULL,
			packet_id TEXT NOT NULL,
			gate_outcome TEXT NOT NULL CHECK (gate_outcome IN ('pass','fail')),
			evidence_sha256 TEXT NOT NULL,
			recorded_at_utc TEXT NOT NULL
		)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("storage: migrate swarm tables: %w", err)
		}
	}
	return nil
}
