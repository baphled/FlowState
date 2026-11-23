package app

// StoreInfo is the `flowstate store info` report: what the database
// file records, what the store handle enforces and which driver and
// toolchain produced the answer. It replaces the retired T019
// qualification capture as a diagnostic.
type StoreInfo struct {
	// SchemaVersion is the highest migration version the file records.
	SchemaVersion int64 `json:"schema_version"`
	// JournalMode is the file's journal mode, for example "wal".
	JournalMode string `json:"journal_mode"`
	// ForeignKeysEnforced is the store handle's foreign_keys setting, not
	// a property of the file: the store's DSN always turns it on.
	ForeignKeysEnforced bool `json:"foreign_keys_enforced"`
	// Driver names the SQLite driver module.
	Driver string `json:"driver"`
	// DriverVersion is the driver module version from the build info,
	// "unknown" when the binary carries none.
	DriverVersion string `json:"driver_version"`
	// SQLiteVersion is the version of the SQLite engine in the driver.
	SQLiteVersion string `json:"sqlite_version"`
	// Path is the database file that was inspected.
	Path string `json:"path"`
	// GoVersion is the Go toolchain version of the running binary.
	GoVersion string `json:"go_version"`
	// Tables lists the user tables with their foreign key counts.
	Tables []StoreTableInfo `json:"tables"`
}

// StoreTableInfo is one table of a StoreInfo.
type StoreTableInfo struct {
	// Name is the table name.
	Name string `json:"name"`
	// ForeignKeys counts the foreign keys the table carries.
	ForeignKeys int `json:"foreign_keys"`
}
