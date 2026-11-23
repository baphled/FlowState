package store

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// knownSchemaVersion is the highest schema version this code manages.
// v4 adds the sessions.parent_id column for child-session delegation;
// v5 adds sessions.budget_pool and the budget_reservations table for
// durable shared budgets; v7 adds the swarm run tables; v8 adds
// swarm_run_children, the child sessions a swarm run created.
const knownSchemaVersion = int64(8)

// migrate applies forward-only schema management through the ORM:
// idempotent AutoMigrate (v4 parent_id, v5 budget_pool and
// budget_reservations, missing tables/indexes; never drops or
// rewrites rows), then v1..v8 bookkeeping. A database whose
// recorded version exceeds knownSchemaVersion fails with
// ErrFutureSchema, checked before any migration touches the schema.
// A database with orphan rows in tables that declare no foreign key is
// refused with ErrReferentialIntegrity before anything is changed.
func (s *Store) migrate() error {
	if err := s.rejectFutureSchema(); err != nil {
		return err
	}
	if err := s.refuseOrphans(); err != nil {
		return err
	}
	if err := s.autoMigrateModels(); err != nil {
		return err
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

// autoMigrateModels migrates the models parents-first. A table that
// does not exist yet is created with CreateTable, which emits the
// declared foreign keys inline and, unlike AutoMigrate, does not pull
// in (and re-migrate) the tables it references. A table that already
// exists is migrated with constraint creation disabled, because SQLite
// can only add a foreign key by rebuilding the table, which would drop
// the WITHOUT ROWID shape of the legacy events table and rewrite rows.
func (s *Store) autoMigrateModels() error {
	existing := s.gdb.Session(&gorm.Session{NewDB: true})
	cfg := *existing.Config
	cfg.DisableForeignKeyConstraintWhenMigrating = true
	existing.Config = &cfg
	for _, model := range migrateModels() {
		var err error
		if s.gdb.Migrator().HasTable(model) {
			err = existing.AutoMigrate(model)
		} else {
			err = s.gdb.Migrator().CreateTable(model)
		}
		if err != nil {
			return fmt.Errorf("automigrate schema: %w", err)
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
