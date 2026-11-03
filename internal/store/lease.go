package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Lease-table typed errors.
var (
	// ErrLeaseHeld marks an acquire against a lease held by another
	// owner that has not been released or fenced.
	ErrLeaseHeld = errors.New("store: lease held")
	// ErrLeaseFenced marks an operation attempted with a fencing
	// token older than the lease's current token (stale writer).
	ErrLeaseFenced = errors.New("store: lease fenced")
	// ErrLeaseNotFound marks an operate/release on an unknown lease.
	ErrLeaseNotFound = errors.New("store: lease not found")
)

// AcquireLease atomically acquires the item lease for agent in a
// transaction, returning the new fencing token. A free lease is
// granted immediately; a lease held by the same agent is re-granted
// (idempotent, token unchanged); a lease held by another agent fails
// with ErrLeaseHeld. Fencing tokens are monotonic per item: each
// ownership change bumps token by one, so any writer holding an
// older token is fenced out by FenceLease checks.
func (s *Store) AcquireLease(ctx context.Context, item, agent string) (int64, error) {
	if err := s.checkOpen(); err != nil {
		return 0, err
	}
	if item == "" || agent == "" {
		return 0, errors.New("store: lease item and agent must not be empty")
	}
	tx, txErr := s.db.BeginTx(ctx, nil)
	if txErr != nil {
		return 0, fmt.Errorf("store: lease tx: %w", txErr)
	}
	defer func() { _ = tx.Rollback() }() // no-op after commit
	var holder string
	var tok int64
	row := tx.QueryRowContext(ctx, `SELECT agent, token FROM leases WHERE item=?`, item)
	err := row.Scan(&holder, &tok)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		var prev int64
		if perr := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(token),0) FROM lease_history WHERE item=?`, item).Scan(&prev); perr == nil && prev > 0 {
			tok = prev + 1
		} else {
			tok = 1
		}
		var execErr error
		_, execErr = tx.ExecContext(ctx,
			`INSERT INTO leases (item, agent, token, acquired_at_utc) VALUES (?,?,?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
			item, agent, tok)
		if execErr != nil {
			return 0, fmt.Errorf("store: insert lease: %w", execErr)
		}
		if _, hErr := tx.ExecContext(ctx, `INSERT OR IGNORE INTO lease_history (item, token) VALUES (?,?)`, item, tok); hErr != nil {
			return 0, fmt.Errorf("store: record lease token: %w", hErr)
		}
	case err != nil:
		return 0, fmt.Errorf("store: read lease: %w", err)
	case holder == agent:
		if _, uerr := tx.ExecContext(ctx, `UPDATE leases SET acquired_at_utc=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE item=?`, item); uerr != nil {
			return 0, fmt.Errorf("store: refresh lease: %w", uerr)
		}
	default:
		return 0, fmt.Errorf("%w: %s by %s", ErrLeaseHeld, item, holder)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: lease commit: %w", err)
	}
	return tok, nil
}

// FenceLease reports whether agent still holds item with exactly
// token; a newer token on the row means the caller was fenced (the
// lease was taken over and bumped) and fails with ErrLeaseFenced.
func (s *Store) FenceLease(ctx context.Context, item, agent string, token int64) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	var holder string
	var tok int64
	err := s.db.QueryRowContext(ctx, `SELECT agent, token FROM leases WHERE item=?`, item).Scan(&holder, &tok)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: %s", ErrLeaseNotFound, item)
	case err != nil:
		return fmt.Errorf("store: read lease: %w", err)
	case holder != agent:
		return fmt.Errorf("%w: %s held by %s", ErrLeaseFenced, item, holder)
	case tok != token:
		return fmt.Errorf("%w: %s at token %d, caller holds %d", ErrLeaseFenced, item, tok, token)
	}
	return nil
}

// ReleaseLease releases the item lease only when agent holds it with
// exactly token (fenced transfer: a stale writer cannot release a
// lease that was taken over). The row is deleted so the item becomes
// freely acquirable again.
func (s *Store) ReleaseLease(ctx context.Context, item, agent string, token int64) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM leases WHERE item=? AND agent=? AND token=?`, item, agent, token)
	if err != nil {
		return fmt.Errorf("store: delete lease: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete lease result: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s not held by %s at token %d", ErrLeaseFenced, item, agent, token)
	}
	return nil
}

// LeaseToken returns the current fencing token of item (0 when free);
// test/inspection seam for the lease ledger.
func (s *Store) LeaseToken(ctx context.Context, item string) (int64, error) {
	if err := s.checkOpen(); err != nil {
		return 0, err
	}
	var tok sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT token FROM leases WHERE item=?`, item).Scan(&tok)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: read lease token: %w", err)
	}
	return tok.Int64, nil
}
