package store

// maxReportedOrphans bounds how many orphan rows a refusal names.
const maxReportedOrphans = 10

// fkSpec describes one reference the store enforces: table.column must
// name an existing parent.id. idColumns are the primary key columns that
// name a row in a report; blankIsRoot marks sessions.parent_id, where
// legacy rows stored ” for a root, which is not an orphan.
type fkSpec struct {
	table       string
	column      string
	parent      string
	idColumns   []string
	blankIsRoot bool
}

// OrphanRow names one row that blocks opening the database.
type OrphanRow struct {
	Table  string
	RowID  string
	Column string
	// Missing is the referenced id that does not exist.
	Missing string
	// Reason says what is wrong with the reference.
	Reason string
}

// ReferentialIntegrityError refuses a database whose rows reference
// missing rows. It wraps ErrReferentialIntegrity, which the composition
// root maps to the config_invalid class. Nothing was changed.
type ReferentialIntegrityError struct {
	// Orphans holds at most maxReportedOrphans offending rows.
	Orphans []OrphanRow
	// Total counts every offending row found.
	Total int
}
