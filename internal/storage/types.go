package storage

import (
	"context"
	"database/sql"
)

// Receipt mirrors the receipts table row (storage.go schema v1):
// an index of acceptance receipts recorded after successful
// upload-then-readback of receipt documents.
type Receipt struct {
	ID            string
	CapabilityID  string
	CommitSHA     string
	ReceiptSHA256 string
	GateOutcome   string
	ReceiptRef    string
	RecordedAtUTC string
}

// QueryExecer is the narrow database surface InsertReceipt needs so
// *sql.DB and *sql.Tx both satisfy it without exposing the handle.
type QueryExecer interface {
	// ExecContext executes the statement with the given args,
	// honouring ctx cancellation, and returns the driver result.
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// OpenInfo records the effective runtime configuration observed after opening
// a database. T019 requires these to be recorded, not assumed.
type OpenInfo struct {
	DriverName    string            `json:"driver_name"`
	DriverVersion string            `json:"driver_version"`
	GoVersion     string            `json:"go_version"`
	SQLiteVersion string            `json:"sqlite_version"`
	PragmaTimeUTC string            `json:"pragma_time_utc"`
	Pragmas       map[string]string `json:"pragmas"`
}
