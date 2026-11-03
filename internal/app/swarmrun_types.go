package app

import "database/sql"

// dbRunReceipts persists swarm run progress onto the receipts
// database as immutable gate outcomes and admission decisions.
type dbRunReceipts struct {
	db *sql.DB
}

// runEventsTo adapts RunEvents callbacks onto the receipts sink.
type runEventsTo struct {
	rec   *dbRunReceipts
	runID string
}
