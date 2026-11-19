package store

import (
	"errors"
	"fmt"

	sqlite "github.com/libtnb/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openORM binds GORM onto the existing *sql.DB pool (single
// serialized connection shared with grandfathered raw paths).
func (s *Store) openORM() error {
	g, err := gorm.Open(sqlite.New(sqlite.Config{Conn: s.db}), &gorm.Config{
		Logger:                 logger.Discard,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return fmt.Errorf("open orm: %w", err)
	}
	s.gdb = g
	return nil
}

// TableName pins the physical table name.
func (schemaMigrationRow) TableName() string { return "schema_migrations" }

// TableName pins the physical table name.
func (sessionRow) TableName() string { return "sessions" }

// TableName pins the physical table name.
func (eventRow) TableName() string { return "events" }

// TableName pins the physical table name.
func (receiptRow) TableName() string { return "receipts" }

// TableName pins the physical table name.
func (leaseRow) TableName() string { return "leases" }

// TableName pins the physical table name.
func (leaseHistoryRow) TableName() string { return "lease_history" }

// TableName pins the physical table name.
func (budgetReservationRow) TableName() string { return "budget_reservations" }

// migrateModels lists every model so AutoMigrate completes the schema
// idempotently. Index names match the committed DDL; no CHECK
// constraints are declared (v1–v3 checks are unnamed — naming them
// would force legacy table rebuilds; whitelists live in the domain).
func migrateModels() []any {
	return []any{
		&schemaMigrationRow{},
		&sessionRow{},
		&eventRow{},
		&receiptRow{},
		&leaseRow{},
		&leaseHistoryRow{},
		&budgetReservationRow{},
		&sessionMessageRow{},
		&swarmRunRow{},
		&swarmRunPacketRow{},
	}
}

// TableName pins the physical table name.
func (sessionMessageRow) TableName() string { return "session_messages" }

// TableName pins the physical table name.
func (swarmRunRow) TableName() string { return "swarm_runs" }

// TableName pins the physical table name.
func (swarmRunPacketRow) TableName() string { return "swarm_run_packets" }

// isRecordNotFound maps gorm.ErrRecordNotFound for call sites that
// treat absence as data, not error.
func isRecordNotFound(err error) bool {
	return err != nil && errors.Is(err, gorm.ErrRecordNotFound)
}
