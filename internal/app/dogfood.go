// Package app — dogfood.go holds the M1-DOGFOOD seams the CLI needs
// so cmd imports only app.
package app

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/baphled/flowstate/internal/metadata"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/storage"
	"github.com/baphled/flowstate/internal/store"
)

// DogfoodCreate creates a dogfood session via the standard
// session-create path.
func DogfoodCreate(ctx context.Context, st *store.Store, providerName, modelID, task string) (*Session, error) {
	return session.Create(ctx, st, providerName, modelID, session.DefaultHarness, "", task)
}

// receiptsOpen is the receipts-database opener seam (test-swappable).
var receiptsOpen = openReceipts

// EmitDogfoodReceipt emits one immutable metadata attempt against the
// receipts database at dbPath; the cmd-facing seam over metadata.Emit
// so cmd never imports metadata or storage directly.
func EmitDogfoodReceipt(ctx context.Context, dbPath string, a Attempt) (*AttemptRecord, error) {
	db, err := receiptsOpen(dbPath)
	if err != nil {
		return nil, fmt.Errorf("app: open receipts db: %w", err)
	}
	defer func() { _ = db.Close() }()
	return metadata.Emit(ctx, db.db(), a)
}

// db exposes the underlying *sql.DB for metadata.Emit.
func (r receiptsDB) db() *sql.DB { return r.sqlDB }

// Close releases the underlying database handle.
func (r receiptsDB) Close() error { return r.sqlDB.Close() }

// openReceipts opens and migrates the receipts database at path.
func openReceipts(path string) (receiptsDB, error) {
	db, _, err := storage.Open(path)
	if err != nil {
		return receiptsDB{}, err
	}
	if err := storage.Migrate(db); err != nil {
		_ = db.Close()
		return receiptsDB{}, err
	}
	return receiptsDB{sqlDB: db}, nil
}
