package app

import (
	"database/sql"

	"github.com/baphled/flowstate/internal/metadata"
	"github.com/baphled/flowstate/internal/store"
)

// Session aliases the session row so cmd depends only on app.
type Session = store.Session

// Attempt, Attribution, Artefact and AttemptRecord alias the metadata
// attempt-record types so cmd depends only on app; the app layer owns
// the metadata emission seam.
type (
	// Attempt is the inbound attempt record awaiting emission.
	Attempt = metadata.Attempt
	// Attribution is the runtime-observed attribution recorded in
	// attribution.json.
	Attribution = metadata.Attribution
	// Artefact is one immutable artefact placed under artefacts/.
	Artefact = metadata.Artefact
	// AttemptRecord is the emission result with every written path
	// and its SHA-256 digest.
	AttemptRecord = metadata.AttemptRecord
)

// receiptsDB is the narrow handle over the receipts database the
// dogfood path needs (hides *sql.DB from cmd).
type receiptsDB struct {
	sqlDB *sql.DB
}
