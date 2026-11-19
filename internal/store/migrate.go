package store

import (
	"fmt"
	"time"
)

// knownSchemaVersion is the highest schema version this code manages.
// v4 adds the sessions.parent_id column for child-session delegation;
// v5 adds sessions.budget_pool and the budget_reservations table for
// durable shared budgets.
const knownSchemaVersion = int64(5)

// migrate applies forward-only schema management through the ORM:
// idempotent AutoMigrate (v4 parent_id, v5 budget_pool and
// budget_reservations, missing tables/indexes; never drops or
// rewrites rows), then v1..v5 bookkeeping. A database whose
// recorded version exceeds knownSchemaVersion fails with
// ErrFutureSchema, checked before any migration touches the schema.
func (s *Store) migrate() error {
	if err := s.rejectFutureSchema(); err != nil {
		return err
	}
	if err := s.gdb.AutoMigrate(migrateModels()...); err != nil {
		return fmt.Errorf("automigrate schema: %w", err)
	}
	maxApplied, err := s.maxAppliedVersion()
	if err != nil {
		return err
	}
	if maxApplied > knownSchemaVersion {
		return fmt.Errorf("%w: db at %d, code knows %d", ErrFutureSchema, maxApplied, knownSchemaVersion)
	}
	for v := int64(1); v <= knownSchemaVersion; v++ {
		if v <= maxApplied {
			continue
		}
		row := schemaMigrationRow{Version: v, AppliedAtUTC: iso(time.Now().UTC())}
		if err := s.gdb.Create(&row).Error; err != nil {
			return fmt.Errorf("record schema version %d: %w", v, err)
		}
	}
	return nil
}

// rejectFutureSchema fails opening a database that already records a
// version newer than this build, before AutoMigrate could mutate it.
func (s *Store) rejectFutureSchema() error {
	if !s.gdb.Migrator().HasTable(&schemaMigrationRow{}) {
		return nil
	}
	maxApplied, err := s.maxAppliedVersion()
	if err != nil {
		return err
	}
	if maxApplied > knownSchemaVersion {
		return fmt.Errorf("%w: db at %d, code knows %d", ErrFutureSchema, maxApplied, knownSchemaVersion)
	}
	return nil
}

// maxAppliedVersion returns the highest recorded schema version, 0 for
// an empty schema_migrations table.
func (s *Store) maxAppliedVersion() (int64, error) {
	var top schemaMigrationRow
	err := s.gdb.Order("version desc").First(&top).Error
	if isRecordNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read schema_migrations: %w", err)
	}
	return top.Version, nil
}

// SchemaVersion returns the highest applied migration version.
func (s *Store) SchemaVersion() (int64, error) {
	if err := s.checkOpen(); err != nil {
		return 0, err
	}
	return s.maxAppliedVersion()
}
