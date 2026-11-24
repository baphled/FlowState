package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrEmptyLeaseOwner marks ClearLeaseOwnedBy called with an empty prefix or an
// empty exact name, either of which would match every owner.
var ErrEmptyLeaseOwner = errors.New("store: lease owner prefix must not be empty")

// ClearLeaseOwnedBy deletes the lease on item when its owner starts with prefix
// or equals one of the exact names, and does nothing when the item is free or
// owned by anyone else. An empty prefix or name is refused (ErrEmptyLeaseOwner):
// it would match every owner. The exact names exist for an owner name an earlier
// build used without the prefix's separator, which the prefix cannot match.
//
// It is for recovery after the owner's process died: a lease never expires. The
// row is deleted only if it is still the one that was read (same owner and
// token), so a lease taken over meanwhile survives. Tokens stay monotonic.
func (s *Store) ClearLeaseOwnedBy(ctx context.Context, item, prefix string, names ...string) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if prefix == "" {
		return ErrEmptyLeaseOwner
	}
	for _, n := range names {
		if n == "" {
			return ErrEmptyLeaseOwner
		}
	}
	var row leaseRow
	err := s.gdb.WithContext(ctx).Where(map[string]any{"item": item}).First(&row).Error
	if isRecordNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: read lease: %w", err)
	}
	if !strings.HasPrefix(row.Agent, prefix) && !containsName(names, row.Agent) {
		return nil
	}
	err = s.gdb.WithContext(ctx).
		Where(map[string]any{"item": item, "agent": row.Agent, "token": row.Token}).Delete(&leaseRow{}).Error
	if err != nil {
		return fmt.Errorf("store: clear lease: %w", err)
	}
	return nil
}

// containsName reports whether names holds name exactly.
func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
