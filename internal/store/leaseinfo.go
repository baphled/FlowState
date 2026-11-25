package store

import (
	"context"
	"fmt"
)

// ReadLease returns the lease row for item. The bool is false, with a zero
// LeaseInfo, when the item has no row. It takes no lock and changes nothing.
func (s *Store) ReadLease(ctx context.Context, item string) (LeaseInfo, bool, error) {
	if err := s.checkOpen(); err != nil {
		return LeaseInfo{}, false, err
	}
	var row leaseRow
	err := s.gdb.WithContext(ctx).Where(map[string]any{"item": item}).First(&row).Error
	if isRecordNotFound(err) {
		return LeaseInfo{}, false, nil
	}
	if err != nil {
		return LeaseInfo{}, false, fmt.Errorf("store: read lease: %w", err)
	}
	return LeaseInfo{
		Item: row.Item, Owner: row.Agent, Token: row.Token, Machine: row.Machine,
		Host: row.Host, Pid: row.Pid, AcquiredAt: row.AcquiredAtUTC,
	}, true, nil
}

// DeleteLeaseIf deletes the lease on item only when its owner AND token are
// still the expected ones, and reports whether it did. A row that was replaced
// meanwhile (another owner, or a bumped token) survives and the answer is
// false. Unlike ClearLeaseOwnedBy the caller supplies the expected token, so it
// is the primitive of an operator's `unlock --force`, which must not delete a
// row it did not show the operator. Tokens stay monotonic (lease_history is
// untouched).
func (s *Store) DeleteLeaseIf(ctx context.Context, item, owner string, token int64) (bool, error) {
	if err := s.checkOpen(); err != nil {
		return false, err
	}
	res := s.gdb.WithContext(ctx).
		Where(map[string]any{"item": item, "agent": owner, "token": token}).Delete(&leaseRow{})
	if res.Error != nil {
		return false, fmt.Errorf("store: delete lease: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}

// TryItemLock takes the exclusive liveness lock for item without blocking, as
// AcquireItemLock does for this store: a file lock beside the database, or the
// store's own process-local table when it has no path. It returns ErrLockHeld
// when a holder, in this or another process, has it. The caller releases it.
func (s *Store) TryItemLock(item string) (*LeaseLock, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	return AcquireItemLock(s.locks, s.path, item)
}
