package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sqlite "github.com/libtnb/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/migrator"
	msqlite "modernc.org/sqlite"
)

// migrateForeignKeys brings a database below v9 to v9: it only records
// the version when every key is declared, else backs the file up and
// rebuilds the unkeyed tables in one transaction. refuseOrphans ran first.
func (s *Store) migrateForeignKeys() error {
	unkeyed := s.unkeyedSpecs()
	switch {
	case len(unkeyed) == 0:
		return s.recordVersion(s.gdb, foreignKeyVersion)
	case s.path == "":
		return errors.New("foreign-key migration needs a database file, not an in-memory database")
	}
	return s.rebuildTables(unkeyed)
}

// unkeyedSpecs lists the references whose table does not declare its key.
func (s *Store) unkeyedSpecs() []fkSpec {
	var unkeyed []fkSpec
	for _, spec := range foreignKeySpecs() {
		if !declaresKey(s.gdb, spec) {
			unkeyed = append(unkeyed, spec)
		}
	}
	return unkeyed
}

// backupExistingDatabase takes the pre-v9 backup BEFORE any schema change, so
// it is a true snapshot: a file database that already has a version history
// and is below v9 is about to change, unless it is v8 with every key declared.
func (s *Store) backupExistingDatabase() error {
	if s.path == "" || !s.gdb.Migrator().HasTable(&schemaMigrationRow{}) {
		return nil
	}
	maxApplied, err := s.maxAppliedVersion()
	if err != nil || maxApplied >= foreignKeyVersion {
		return err
	}
	if maxApplied == foreignKeyVersion-1 && len(s.unkeyedSpecs()) == 0 {
		return nil
	}
	return s.backupBeforeRebuild()
}

// recordVersion appends one schema_migrations row.
func (s *Store) recordVersion(tx *gorm.DB, v int64) error {
	row := schemaMigrationRow{Version: v, AppliedAtUTC: iso(time.Now().UTC())}
	if err := tx.Create(&row).Error; err != nil {
		return fmt.Errorf("record schema version %d: %w", v, err)
	}
	return nil
}

func (s *Store) backupPath() string { return s.path + ".pre-v9.bak" }

// backupBeforeRebuild copies the database, WAL included, to backupPath with
// the driver's online backup API into a file created exclusively, mode 0600.
func (s *Store) backupBeforeRebuild() error {
	dst := s.backupPath()
	dir, err := os.OpenRoot(filepath.Dir(dst))
	if err != nil {
		return fmt.Errorf("open the directory for the pre-v9 backup %s: %w", dst, err)
	}
	defer func() { _ = dir.Close() }()
	f, err := dir.OpenFile(filepath.Base(dst), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: %s; move it away to retry", ErrBackupExists, dst)
	}
	if err != nil {
		return fmt.Errorf("create pre-v9 backup %s: %w", dst, err)
	}
	if err := errors.Join(f.Close(), s.copyDatabase(dst)); err != nil {
		_ = dir.Remove(filepath.Base(dst))
		return fmt.Errorf("write pre-v9 backup %s: %w", dst, err)
	}
	return nil
}

// copyDatabase runs the driver's online backup to dst on its own connection.
func (s *Store) copyDatabase(dst string) error {
	conn, err := (&msqlite.Driver{}).Open("file:" + s.path + "?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	b, ok := conn.(interface {
		NewBackup(dstURI string) (*msqlite.Backup, error)
	})
	if !ok {
		return errors.New("sqlite driver offers no online backup")
	}
	bk, err := b.NewBackup(dst)
	if err != nil {
		return err
	}
	for more := true; more; {
		if more, err = bk.Step(-1); err != nil {
			_ = bk.Finish()
			return err
		}
	}
	return bk.Finish()
}

// openRebuildHandle opens a second connection with foreign keys OFF (set in
// the DSN: it cannot change inside a transaction); the Store's own stays ON.
func (s *Store) openRebuildHandle() (*gorm.DB, *sql.DB, error) {
	dsn := "file:" + s.path + "?_pragma=foreign_keys(OFF)&_pragma=legacy_alter_table(ON)&_pragma=busy_timeout(5000)"
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open rebuild connection: %w", err)
	}
	raw.SetMaxOpenConns(1)
	g, err := gorm.Open(sqlite.New(sqlite.Config{Conn: raw}),
		&gorm.Config{Logger: logger.Discard, SkipDefaultTransaction: true})
	if err != nil {
		_ = raw.Close()
		return nil, nil, fmt.Errorf("open rebuild orm: %w", err)
	}
	return g, raw, nil
}

// rebuildTables rebuilds each unkeyed table parent-first inside one
// transaction, re-runs the orphan queries, and records v9 on commit.
func (s *Store) rebuildTables(specs []fkSpec) error {
	g, raw, err := s.openRebuildHandle()
	if err != nil {
		return err
	}
	defer func() { _ = raw.Close() }()
	err = g.Transaction(func(tx *gorm.DB) error {
		for _, spec := range specs {
			if err := rebuildTable(tx, spec); err != nil {
				return fmt.Errorf("rebuild %s: %w", spec.table, err)
			}
		}
		if err := findOrphans(tx, foreignKeySpecs()); err != nil {
			return err
		}
		return s.recordVersion(tx, foreignKeyVersion)
	})
	if err != nil {
		return fmt.Errorf("%w; the database was rolled back; backup at %s", err, s.backupPath())
	}
	return nil
}

// rebuildTable replaces spec.table by a keyed copy: rename the old table to
// <t>__old, create the keyed table, copy every row, verify, drop the old one.
func rebuildTable(tx *gorm.DB, spec fkSpec) error {
	old := spec.table + "__old"
	withoutRowID, err := isWithoutRowID(tx, spec.table)
	if err != nil {
		return err
	}
	if err := dropDeclaredIndexes(tx, spec); err != nil {
		return err
	}
	if err := tx.Migrator().RenameTable(spec.table, old); err != nil {
		return fmt.Errorf("set old table aside: %w", err)
	}
	created := tx
	if withoutRowID {
		created = tx.Set("gorm:table_options", "WITHOUT ROWID")
	}
	if err := created.Migrator().CreateTable(spec.model); err != nil {
		return fmt.Errorf("create keyed table: %w", err)
	}
	copied, err := copyRows(tx, spec, old)
	if err != nil {
		return err
	}
	var kept int64
	if err := tx.Table(spec.table).Count(&kept).Error; err != nil {
		return fmt.Errorf("count rebuilt rows: %w", err)
	}
	if err := verifyRowCount(spec.table, copied, kept); err != nil {
		return err
	}
	if !declaresKey(tx, spec) {
		return fmt.Errorf("rebuilt %s declares no foreign key", spec.table)
	}
	// The dialect's DropTable uses a second connection and would deadlock here.
	base := migrator.Migrator{Config: migrator.Config{DB: tx, Dialector: tx.Dialector}}
	if err := base.DropTable(old); err != nil {
		return fmt.Errorf("drop old table: %w", err)
	}
	return nil
}

// isWithoutRowID reports whether table lacks the implicit rowid, which
// only a WITHOUT ROWID table does. No modelled table exposes the layout,
// so it selects rowid through the query builder: "no such column" means
// WITHOUT ROWID; any other failure is returned, never read as an answer.
func isWithoutRowID(tx *gorm.DB, table string) (bool, error) {
	var ids []int64
	err := tx.Table(table).Limit(1).Pluck("rowid", &ids).Error
	switch {
	case err == nil:
		return false, nil
	case strings.Contains(err.Error(), "no such column"):
		return true, nil
	}
	return false, fmt.Errorf("inspect %s layout: %w", table, err)
}

// verifyRowCount fails unless the rebuilt table holds every row read.
func verifyRowCount(table string, read, kept int64) error {
	if read != kept {
		return fmt.Errorf("%w: %s read %d rows, rebuilt table holds %d", ErrRowCountMismatch, table, read, kept)
	}
	return nil
}

// dropDeclaredIndexes frees the model's index names for the keyed table.
func dropDeclaredIndexes(tx *gorm.DB, spec fkSpec) error {
	stmt := &gorm.Statement{DB: tx}
	if err := stmt.Parse(spec.model); err != nil {
		return fmt.Errorf("parse model: %w", err)
	}
	for _, idx := range stmt.Schema.ParseIndexes() {
		if !tx.Migrator().HasIndex(spec.model, idx.Name) {
			continue
		}
		if err := tx.Migrator().DropIndex(spec.model, idx.Name); err != nil {
			return fmt.Errorf("remove index %s: %w", idx.Name, err)
		}
	}
	return nil
}

// copyRows copies every row of old into the keyed table, turning ” in a
// root-marked parent column into NULL; it returns how many it read.
func copyRows(tx *gorm.DB, spec fkSpec, old string) (int64, error) {
	rows, err := tx.Table(old).Order(strings.Join(spec.idColumns, ", ")).Rows()
	if err != nil {
		return 0, fmt.Errorf("read rows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var total int64
	batch := make([]map[string]any, 0, rebuildBatch)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := tx.Table(spec.table).Create(&batch).Error; err != nil {
			return fmt.Errorf("copy rows: %w", err)
		}
		batch = batch[:0]
		return nil
	}
	for rows.Next() {
		row := map[string]any{}
		if err := tx.ScanRows(rows, &row); err != nil {
			return 0, fmt.Errorf("scan row: %w", err)
		}
		if v, ok := row[spec.column].(string); ok && spec.blankIsRoot && v == "" {
			row[spec.column] = nil
		}
		batch = append(batch, row)
		total++
		if len(batch) == rebuildBatch {
			if err := flush(); err != nil {
				return 0, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read rows: %w", err)
	}
	return total, flush()
}
