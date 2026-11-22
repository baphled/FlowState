package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	sqlite "github.com/libtnb/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Swarm-receipt sentinels; a rejected receipt writes nothing.
var (
	// ErrSwarmGateOutcomeInvalid rejects a member receipt gate outcome
	// outside pass/fail.
	ErrSwarmGateOutcomeInvalid = errors.New("store: swarm gate_outcome must be pass or fail")
)

// TableName pins the physical table name.
func (swarmAdmissionRow) TableName() string { return "swarm_admissions" }

// TableName pins the physical table name.
func (swarmGateReceiptRow) TableName() string { return "swarm_gate_receipts" }

// TableName pins the physical table name.
func (swarmMemberReceiptRow) TableName() string { return "swarm_member_receipts" }

// swarmReceiptTimestamp is the sortable UTC format the legacy tables hold.
func swarmReceiptTimestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// openSwarmReceiptORM binds the ORM to an existing database handle.
func openSwarmReceiptORM(db *sql.DB) (*gorm.DB, error) {
	g, err := gorm.Open(sqlite.New(sqlite.Config{Conn: db}), &gorm.Config{
		Logger:                 logger.Discard,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("store: open swarm receipt orm: %w", err)
	}
	return g, nil
}

// NewSwarmReceiptLog binds the ORM to an existing database handle
// whose schema already holds the swarm receipt tables (see
// MigrateSwarmReceipts). The caller owns the handle and must keep it
// open while the log is used.
func NewSwarmReceiptLog(db *sql.DB) (*SwarmReceiptLog, error) {
	g, err := openSwarmReceiptORM(db)
	if err != nil {
		return nil, err
	}
	return &SwarmReceiptLog{gdb: g}, nil
}

// MigrateSwarmReceipts creates the swarm_admissions,
// swarm_gate_receipts and swarm_member_receipts tables when absent.
// Existing tables are left untouched so deployed constraints are never
// rewritten. The caller owns the handle. A database created here has
// no CHECK constraints (the models carry none, as the store's models
// may not); the outcome rule is enforced in Go by RecordMemberReceipt,
// and a database the legacy code migrated keeps the constraints it was
// created with.
func MigrateSwarmReceipts(db *sql.DB) error {
	g, err := openSwarmReceiptORM(db)
	if err != nil {
		return err
	}
	for _, model := range []any{
		&swarmAdmissionRow{}, &swarmGateReceiptRow{}, &swarmMemberReceiptRow{},
	} {
		if g.Migrator().HasTable(model) {
			continue
		}
		if err := g.Migrator().CreateTable(model); err != nil {
			return fmt.Errorf("store: migrate swarm receipt tables: %w", err)
		}
	}
	return nil
}

// RecordAdmission persists one packet admission decision, stored as
// outcome admit or reject, stamped with the current UTC time.
func (l *SwarmReceiptLog) RecordAdmission(ctx context.Context, runID, packetID string, admitted bool, reason string) error {
	outcome := "reject"
	if admitted {
		outcome = "admit"
	}
	row := &swarmAdmissionRow{
		RunID: runID, PacketID: packetID, Outcome: outcome,
		Reason: reason, RecordedAtUTC: swarmReceiptTimestamp(),
	}
	if err := l.gdb.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("store: record admission: %w", err)
	}
	return nil
}

// RecordGates persists every gate outcome of a run atomically: a
// failure on any gate writes none of them. Outcomes are stored as
// pass or fail.
func (l *SwarmReceiptLog) RecordGates(ctx context.Context, runID string, results []GateResult) error {
	return l.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, res := range results {
			state := "fail"
			if res.Passed {
				state = "pass"
			}
			row := &swarmGateReceiptRow{
				RunID: runID, GateName: res.Name, Outcome: state,
				RecordedAtUTC: swarmReceiptTimestamp(),
			}
			if err := tx.Create(row).Error; err != nil {
				return fmt.Errorf("store: record gate receipt: %w", err)
			}
		}
		return nil
	})
}

// RecordMemberReceipt persists one member receipt. The gate outcome
// must be pass or fail (ErrSwarmGateOutcomeInvalid); a rejected
// receipt writes nothing.
func (l *SwarmReceiptLog) RecordMemberReceipt(ctx context.Context, rec MemberReceiptRecord) error {
	if rec.GateOutcome != SwarmRunOutcomePass && rec.GateOutcome != SwarmRunOutcomeFail {
		return fmt.Errorf("%w, got %q", ErrSwarmGateOutcomeInvalid, rec.GateOutcome)
	}
	row := &swarmMemberReceiptRow{
		RunName: rec.RunName, PacketID: rec.PacketID,
		GateOutcome: rec.GateOutcome, EvidenceSHA256: rec.EvidenceSHA256,
		RecordedAtUTC: swarmReceiptTimestamp(),
	}
	if err := l.gdb.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("store: record member receipt: %w", err)
	}
	return nil
}
