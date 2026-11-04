package storage

import "database/sql"

// SwarmReceipts implements the swarm persistence ports via SQLite.
// Construction and migrations are performed by the application composition root.
type SwarmReceipts struct {
	db *sql.DB
}
