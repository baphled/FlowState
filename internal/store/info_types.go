package store

// Info is a read-only description of an open database: what the file
// records and what this handle enforces. It is a diagnostic snapshot,
// not part of the event contract.
type Info struct {
	// SchemaVersion is the highest recorded migration version, 0 for
	// a file without a schema_migrations table.
	SchemaVersion int64
	// JournalMode is the file's journal mode, for example "wal".
	JournalMode string
	// ForeignKeysEnforced is this handle's foreign_keys setting. The
	// store's DSN forces it on, so it is not a property of the file.
	ForeignKeysEnforced bool
	// SQLiteVersion is the version of the SQLite engine in the driver.
	SQLiteVersion string
	// Tables lists the user tables in name order.
	Tables []TableInfo
}

// TableInfo describes one table of an Info.
type TableInfo struct {
	// Name is the table name.
	Name string
	// ForeignKeys counts the references the store declares on the table
	// that the table actually carries as foreign key constraints.
	ForeignKeys int
}

// pragmaJournalMode maps the pragma_journal_mode table-valued function.
type pragmaJournalMode struct {
	JournalMode string `gorm:"column:journal_mode"`
}

// pragmaForeignKeys maps the pragma_foreign_keys table-valued function.
type pragmaForeignKeys struct {
	Enabled int `gorm:"column:foreign_keys"`
}
