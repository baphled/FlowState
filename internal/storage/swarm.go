package storage

import (
	"database/sql"
	"fmt"
	"time"

	sqlite "github.com/libtnb/sqlite"
	"gorm.io/gorm"

	"github.com/baphled/flowstate/internal/swarm"
)

// NewSwarmReceipts binds GORM to the existing qualified SQLite connection.
// The caller owns the database handle and its lifecycle.
func NewSwarmReceipts(db *sql.DB) (*SwarmReceipts, error) {
	mapped, err := gorm.Open(sqlite.New(sqlite.Config{Conn: db}), &gorm.Config{
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: open swarm ORM: %w", err)
	}
	return &SwarmReceipts{db: mapped}, nil
}

// TableName preserves the existing swarm_admissions table name.
func (swarmAdmission) TableName() string { return "swarm_admissions" }

// TableName preserves the existing swarm_gate_receipts table name.
func (swarmGateReceipt) TableName() string { return "swarm_gate_receipts" }

// TableName preserves the existing swarm_member_receipts table name.
func (swarmMemberReceipt) TableName() string { return "swarm_member_receipts" }

// swarmTimestamp matches the existing sortable UTC timestamp format.
func swarmTimestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// RecordAdmission persists an admission decision through a mapped model.
func (r *SwarmReceipts) RecordAdmission(runID string, p swarm.Packet, admitted bool, reason string) error {
	outcome := "reject"
	if admitted {
		outcome = "admit"
	}
	row := &swarmAdmission{
		RunID: runID, PacketID: p.ID, Outcome: outcome,
		Reason: reason, RecordedAtUTC: swarmTimestamp(),
	}
	if err := r.db.Create(row).Error; err != nil {
		return fmt.Errorf("storage: record admission: %w", err)
	}
	return nil
}

// RecordGates persists gate outcomes atomically through GORM.
func (r *SwarmReceipts) RecordGates(runID string, d swarm.GatesDecision) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, outcome := range d.Outcomes {
			state := "fail"
			if outcome.Passed {
				state = "pass"
			}
			row := &swarmGateReceipt{
				RunID: runID, GateName: outcome.Gate.Name, Outcome: state,
				RecordedAtUTC: swarmTimestamp(),
			}
			if err := tx.Create(row).Error; err != nil {
				return fmt.Errorf("storage: record gate receipt: %w", err)
			}
		}
		return nil
	})
}

// RecordMemberReceipt persists a member receipt using its mapped model.
func (r *SwarmReceipts) RecordMemberReceipt(rec swarm.MemberReceipt) error {
	row := &swarmMemberReceipt{
		RunName: rec.Identity.RunName, PacketID: rec.Identity.PacketID,
		GateOutcome: rec.GateOutcome, EvidenceSHA256: rec.EvidenceSHA256,
		RecordedAtUTC: swarmTimestamp(),
	}
	if err := r.db.Create(row).Error; err != nil {
		return fmt.Errorf("storage: record member receipt: %w", err)
	}
	return nil
}

// MigrateSwarmTables creates missing tables through ORM model mappings.
// Existing tables are left intact to avoid rewriting deployed constraints.
func MigrateSwarmTables(db *sql.DB) error {
	mapped, err := NewSwarmReceipts(db)
	if err != nil {
		return err
	}
	for _, model := range []any{
		&swarmAdmission{}, &swarmGateReceipt{}, &swarmMemberReceipt{},
	} {
		if mapped.db.Migrator().HasTable(model) {
			continue
		}
		if err := mapped.db.Migrator().CreateTable(model); err != nil {
			return fmt.Errorf("storage: migrate swarm tables: %w", err)
		}
	}
	return nil
}
