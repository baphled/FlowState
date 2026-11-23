package store

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// foreignKeySpecs lists every reference a current database enforces.
func foreignKeySpecs() []fkSpec {
	return []fkSpec{
		{model: &sessionRow{}, table: "sessions", relation: "Parent", column: "parent_id", parent: "sessions", idColumns: []string{"id"}, blankIsRoot: true},
		{model: &eventRow{}, table: "events", relation: "Session", column: "session_id", parent: "sessions", idColumns: []string{"session_id", "seq"}},
		{model: &budgetReservationRow{}, table: "budget_reservations", relation: "Parent", column: "parent_id", parent: "sessions", idColumns: []string{"id"}},
		{model: &sessionMessageRow{}, table: "session_messages", relation: "Session", column: "session_id", parent: "sessions", idColumns: []string{"id"}},
		{model: &swarmRunPacketRow{}, table: "swarm_run_packets", relation: "Run", column: "run_id", parent: "swarm_runs", idColumns: []string{"id"}},
		{model: &swarmRunChildRow{}, table: "swarm_run_children", relation: "Run", column: "run_id", parent: "swarm_runs", idColumns: []string{"id"}},
	}
}

// refuseOrphans fails with a ReferentialIntegrityError, changing nothing,
// when a row of a table that declares no foreign key names a missing
// parent, or a session names itself. Tables or columns that do not exist
// yet (an older database before AutoMigrate) cannot hold orphans, and a
// table that already declares its key is enforced by SQLite itself.
func (s *Store) refuseOrphans() error {
	var unkeyed []fkSpec
	for _, spec := range foreignKeySpecs() {
		migrator := s.gdb.Migrator()
		if !migrator.HasTable(spec.table) || !migrator.HasColumn(spec.table, spec.column) {
			continue
		}
		if !declaresKey(s.gdb, spec) {
			unkeyed = append(unkeyed, spec)
		}
	}
	return findOrphans(s.gdb, unkeyed)
}

// declaresKey reports whether the table of spec declares the foreign
// key of its relation, which is how a database created since #421
// differs from an older one. It asks the migrator for the exact
// constraint of the one relation, so neither the word REFERENCES in a
// default or comment nor a key on another column counts.
func declaresKey(db *gorm.DB, spec fkSpec) bool {
	return db.Migrator().HasConstraint(spec.model, spec.relation)
}

// findOrphans returns a ReferentialIntegrityError naming the orphans and
// self-references of specs, at most maxReportedOrphans in all, or nil.
func findOrphans(db *gorm.DB, specs []fkSpec) error {
	failure := &ReferentialIntegrityError{}
	for _, spec := range specs {
		missing := db.Table(spec.table).
			Where(spec.column+" IS NOT NULL").
			Where(spec.column+" NOT IN (?)", db.Table(spec.parent).Select("id"))
		if spec.blankIsRoot {
			missing = missing.Where(spec.column + " <> ''")
		}
		if err := appendOrphans(failure, missing, spec, "missing parent"); err != nil {
			return err
		}
		if spec.blankIsRoot {
			self := db.Table(spec.table).Where(spec.column + " = id")
			if err := appendOrphans(failure, self, spec, "references itself"); err != nil {
				return err
			}
		}
	}
	if failure.Total > 0 {
		return failure
	}
	return nil
}

// appendOrphans runs one orphan query and records what it finds.
func appendOrphans(failure *ReferentialIntegrityError, found *gorm.DB, spec fkSpec, reason string) error {
	var rows []map[string]any
	cols := append(append([]string{}, spec.idColumns...), spec.column)
	if err := found.Select(cols).Order(strings.Join(spec.idColumns, ", ")).Find(&rows).Error; err != nil {
		return fmt.Errorf("check %s.%s: %w", spec.table, spec.column, err)
	}
	for _, r := range rows {
		failure.Total++
		if len(failure.Orphans) >= maxReportedOrphans {
			continue
		}
		ids := make([]string, len(spec.idColumns))
		for i, c := range spec.idColumns {
			ids[i] = fmt.Sprint(r[c])
		}
		failure.Orphans = append(failure.Orphans, OrphanRow{Table: spec.table, RowID: strings.Join(ids, "/"),
			Column: spec.column, Missing: fmt.Sprint(r[spec.column]), Reason: reason})
	}
	return nil
}

// Error names the offending rows and states that the database is untouched.
func (e *ReferentialIntegrityError) Error() string {
	parts := make([]string, 0, len(e.Orphans))
	for _, o := range e.Orphans {
		parts = append(parts, fmt.Sprintf("%s %q: %s %s=%q", o.Table, o.RowID, o.Reason, o.Column, o.Missing))
	}
	return fmt.Sprintf("%v: %d row(s), first %d: %s; nothing was changed: "+
		"repair or remove these rows, then open it again", ErrReferentialIntegrity, e.Total, len(parts),
		strings.Join(parts, "; "))
}

// Unwrap lets errors.Is match ErrReferentialIntegrity.
func (e *ReferentialIntegrityError) Unwrap() error { return ErrReferentialIntegrity }
