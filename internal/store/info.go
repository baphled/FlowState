package store

import (
	"fmt"
	"os"
	"sort"

	sqlite3 "modernc.org/sqlite/lib"
)

// OpenReadOnly opens an existing database for reading: the connection is
// read-only, nothing is migrated, rebuilt or backed up, and the database
// file is never modified, so an old database reports the version it
// holds. SQLite may still create or update the -wal and -shm sidecar
// files of a WAL-mode database, so a WAL database in a read-only
// directory without them cannot be read. A missing file fails with ErrNoDatabase
// and is not created. Callers must Close.
func OpenReadOnly(path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNoDatabase, path)
		}
		return nil, fmt.Errorf("stat database: %w", err)
	}
	return openDSN(fileDSN(path, "mode=ro&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"), path, false)
}

// Info reads the database description. Everything is read through the
// ORM: the pragma values come from mapped models over SQLite's
// table-valued pragma functions, and the engine version is the driver's
// compiled-in constant.
func (s *Store) Info() (Info, error) {
	if err := s.checkOpen(); err != nil {
		return Info{}, err
	}
	info := Info{SQLiteVersion: sqlite3.SQLITE_VERSION}
	if s.gdb.Migrator().HasTable(&schemaMigrationRow{}) {
		v, err := s.maxAppliedVersion()
		if err != nil {
			return Info{}, err
		}
		info.SchemaVersion = v
	}
	var journal pragmaJournalMode
	if err := s.gdb.Take(&journal).Error; err != nil {
		return Info{}, fmt.Errorf("read journal mode: %w", err)
	}
	info.JournalMode = journal.JournalMode
	var keys pragmaForeignKeys
	if err := s.gdb.Take(&keys).Error; err != nil {
		return Info{}, fmt.Errorf("read foreign keys setting: %w", err)
	}
	info.ForeignKeysEnforced = keys.Enabled == 1
	tables, err := s.tableInfos()
	if err != nil {
		return Info{}, err
	}
	info.Tables = tables
	return info, nil
}

// tableInfos lists the user tables, in name order, with the foreign keys each
// carries. The migrator already omits the sqlite_ internal tables.
func (s *Store) tableInfos() ([]TableInfo, error) {
	names, err := s.gdb.Migrator().GetTables()
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	sort.Strings(names)
	var out []TableInfo
	for _, name := range names {
		out = append(out, TableInfo{Name: name, ForeignKeys: s.carriedKeys(name)})
	}
	return out, nil
}

// carriedKeys counts the declared references of a table that the table
// carries as a foreign key constraint.
func (s *Store) carriedKeys(table string) int {
	n := 0
	for _, spec := range foreignKeySpecs() {
		if spec.table == table && declaresKey(s.gdb, spec) {
			n++
		}
	}
	return n
}

// TableName names the pragma function the model reads.
func (pragmaJournalMode) TableName() string { return "pragma_journal_mode" }

// TableName names the pragma function the model reads.
func (pragmaForeignKeys) TableName() string { return "pragma_foreign_keys" }
