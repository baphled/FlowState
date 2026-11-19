package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Reservation lifecycle statuses; the whitelist lives in the domain
// layer, not DB CHECKs (see orm_types.go).
const (
	// ReservationStatusReserved marks a live grant still drawable by its child.
	ReservationStatusReserved = "reserved"
	// ReservationStatusCharged marks a settled grant: its unspent
	// remainder no longer counts against the parent pool.
	ReservationStatusCharged = "charged"
)

// Budget reservation sentinels; they stay distinct from the app-layer
// usage mapping (see app.ReserveChildBudget).
var (
	// ErrBudgetPoolMissing is returned when a reservation targets a
	// session that has declared no budget pool.
	ErrBudgetPoolMissing = errors.New("store: session declares no budget pool")

	// ErrBudgetExceeded is returned when a grant would exceed the
	// parent pool's available budget; nothing is written.
	ErrBudgetExceeded = errors.New("store: budget pool exceeded")

	// ErrUnknownReservation is returned when no reservation row exists
	// for the child id.
	ErrUnknownReservation = errors.New("store: unknown budget reservation")

	// ErrReservationDuplicate is returned when the child already holds
	// a reservation.
	ErrReservationDuplicate = errors.New("store: child already holds a reservation")

	// ErrReservationCharged is returned when charging an already
	// settled reservation.
	ErrReservationCharged = errors.New("store: reservation already charged")

	// ErrReservationAmountInvalid rejects non-positive grant or charge
	// amounts.
	ErrReservationAmountInvalid = errors.New("store: reservation amount must be positive")
)

// SetBudgetPool anchors (total > 0) or clears (total <= 0, SQL NULL)
// the declared budget pool for sessionID and its children. Unknown
// ids are ErrUnknownSession.
func (s *Store) SetBudgetPool(ctx context.Context, sessionID string, total int64) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	var pool any
	if total > 0 {
		pool = total
	}
	q := s.gdb.WithContext(ctx).Model(&sessionRow{}).
		Where(map[string]any{"id": sessionID}).
		Updates(map[string]any{"budget_pool": pool, "updated_at_utc": iso(time.Now().UTC())})
	if q.Error != nil {
		return fmt.Errorf("set budget pool for %s: %w", sessionID, q.Error)
	}
	if q.RowsAffected == 0 {
		return fmt.Errorf("%w: %s", ErrUnknownSession, sessionID)
	}
	return nil
}

// BudgetSnapshot reports the session's pool anchor, the outstanding
// budget reserved by live child reservations and the resulting
// available budget. Unknown ids are ErrUnknownSession.
func (s *Store) BudgetSnapshot(ctx context.Context, sessionID string) (BudgetSnapshot, error) {
	if err := s.checkOpen(); err != nil {
		return BudgetSnapshot{}, err
	}
	var row sessionRow
	err := s.gdb.WithContext(ctx).Where(map[string]any{"id": sessionID}).First(&row).Error
	if isRecordNotFound(err) {
		return BudgetSnapshot{}, fmt.Errorf("%w: %s", ErrUnknownSession, sessionID)
	}
	if err != nil {
		return BudgetSnapshot{}, err
	}
	snap := BudgetSnapshot{}
	if row.BudgetPool != nil {
		pool := *row.BudgetPool
		snap.Pool = &pool
	}
	reserved, err := s.reservedOutstanding(ctx, s.gdb, sessionID)
	if err != nil {
		return BudgetSnapshot{}, err
	}
	snap.Reserved = reserved
	if snap.Pool != nil {
		available := *snap.Pool - reserved
		snap.Available = &available
	}
	return snap, nil
}

// ReserveBudget transactionally draws grant for childID against
// parentID's declared pool: capacity is the pool minus the
// outstanding live reservations, a grant beyond it fails closed with
// ErrBudgetExceeded and nothing written. A missing pool is
// ErrBudgetPoolMissing; a duplicate child reservation is
// ErrReservationDuplicate. Returns the granted amount.
func (s *Store) ReserveBudget(ctx context.Context, parentID, childID string, grant int64) (int64, error) {
	if err := s.checkOpen(); err != nil {
		return 0, err
	}
	if grant <= 0 {
		return 0, fmt.Errorf("%w: %d", ErrReservationAmountInvalid, grant)
	}
	err := s.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var parent sessionRow
		perr := tx.Where(map[string]any{"id": parentID}).First(&parent).Error
		if isRecordNotFound(perr) {
			return fmt.Errorf("%w: %s", ErrUnknownSession, parentID)
		}
		if perr != nil {
			return perr
		}
		if parent.BudgetPool == nil {
			return fmt.Errorf("%w: %s", ErrBudgetPoolMissing, parentID)
		}
		var existing budgetReservationRow
		eerr := tx.Where(map[string]any{"child_id": childID}).First(&existing).Error
		if eerr == nil {
			return fmt.Errorf("%w: %s", ErrReservationDuplicate, childID)
		}
		if !isRecordNotFound(eerr) {
			return eerr
		}
		outstanding, oerr := s.reservedOutstanding(ctx, tx, parentID)
		if oerr != nil {
			return oerr
		}
		if available := *parent.BudgetPool - outstanding; grant > available {
			return fmt.Errorf("%w: grant %d exceeds available %d", ErrBudgetExceeded, grant, available)
		}
		id, merr := uuid.NewV7()
		if merr != nil {
			return fmt.Errorf("mint reservation id: %w", merr)
		}
		row := budgetReservationRow{
			ID: id.String(), ParentID: parentID, ChildID: childID,
			Granted: grant, Status: ReservationStatusReserved, CreatedAtUTC: iso(time.Now().UTC()),
		}
		if cerr := tx.Create(&row).Error; cerr != nil {
			return fmt.Errorf("create reservation: %w", cerr)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return grant, nil
}

// ReservationFor loads the child's reservation row; absence wraps
// ErrUnknownReservation.
func (s *Store) ReservationFor(ctx context.Context, childID string) (BudgetReservation, error) {
	if err := s.checkOpen(); err != nil {
		return BudgetReservation{}, err
	}
	var row budgetReservationRow
	err := s.gdb.WithContext(ctx).Where(map[string]any{"child_id": childID}).First(&row).Error
	if isRecordNotFound(err) {
		return BudgetReservation{}, fmt.Errorf("%w: %s", ErrUnknownReservation, childID)
	}
	if err != nil {
		return BudgetReservation{}, err
	}
	res := BudgetReservation{
		ID: row.ID, ParentID: row.ParentID, ChildID: row.ChildID,
		Granted: row.Granted, Consumed: row.Consumed, Status: row.Status,
	}
	res.CreatedAt, _ = time.Parse(time.RFC3339Nano, row.CreatedAtUTC)
	return res, nil
}

// ChargeReservation records settled usage on the child's live
// reservation: consumed is capped at the granted amount and the
// status stays reserved (a paused child may resume within its
// remaining grant; SetReservationCharged settles it instead).
func (s *Store) ChargeReservation(ctx context.Context, childID string, consumed int64) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	return s.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row budgetReservationRow
		err := tx.Where(map[string]any{"child_id": childID}).First(&row).Error
		if isRecordNotFound(err) {
			return fmt.Errorf("%w: %s", ErrUnknownReservation, childID)
		}
		if err != nil {
			return err
		}
		if row.Status != ReservationStatusReserved {
			return fmt.Errorf("%w: %s", ErrReservationCharged, childID)
		}
		next := consumed
		if next > row.Granted {
			next = row.Granted
		}
		q := tx.Model(&budgetReservationRow{}).
			Where(map[string]any{"id": row.ID}).
			Updates(map[string]any{"consumed": next})
		if q.Error != nil {
			return fmt.Errorf("charge reservation %s: %w", row.ID, q.Error)
		}
		return nil
	})
}

// SetReservationCharged settles the child's reservation: status
// reserved becomes charged and the unspent remainder releases back to
// the parent pool. Already-charged and missing reservations are a
// no-op success so terminal transitions need no pre-checks.
func (s *Store) SetReservationCharged(ctx context.Context, childID string) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	q := s.gdb.WithContext(ctx).Model(&budgetReservationRow{}).
		Where(map[string]any{"child_id": childID, "status": ReservationStatusReserved}).
		Updates(map[string]any{"status": ReservationStatusCharged})
	if q.Error != nil {
		return fmt.Errorf("settle reservation for %s: %w", childID, q.Error)
	}
	return nil
}

// reservedOutstanding sums granted - consumed over the parent's live
// (reserved) reservations inside the given ORM scope; settled grants
// no longer count against the pool.
func (s *Store) reservedOutstanding(ctx context.Context, scope *gorm.DB, parentID string) (int64, error) {
	var outstanding int64
	err := scope.WithContext(ctx).Model(&budgetReservationRow{}).
		Where(map[string]any{"parent_id": parentID, "status": ReservationStatusReserved}).
		Select("COALESCE(SUM(granted - consumed), 0)").Scan(&outstanding).Error
	if err != nil {
		return 0, fmt.Errorf("sum reserved budget: %w", err)
	}
	return outstanding, nil
}
