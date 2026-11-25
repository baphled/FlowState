package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrSessionBusy is the sentinel for a refused AcquireRunLease: the holder is
// alive, or the row cannot be judged. The error carries a *SessionBusyError.
var ErrSessionBusy = errors.New("store: session busy")

// runLeaseAfterLockHook runs after the lock is held and before the
// transaction opens; specs use it to write a row in that window.
var runLeaseAfterLockHook = func(string) {}

// runLeaseBeforeSwapHook runs inside the takeover transaction between the read
// and the compare-and-set; specs use it to change the row there.
var runLeaseBeforeSwapHook = func(*gorm.DB, string) {}

// runLeaseTxAttempts bounds the transaction retries on SQLITE_BUSY/LOCKED.
const runLeaseTxAttempts = 5

// runLeaseBackoff is the base wait between transaction retries; attempt n
// waits n times this long. Specs shorten it.
var runLeaseBackoff = 10 * time.Millisecond

// runLeaseTxHook runs first in every transaction attempt; specs use it to
// inject an error or a panic. A non-nil return fails the attempt.
var runLeaseTxHook = func(int) error { return nil }

// Error names the holder and the manual command.
func (e *SessionBusyError) Error() string {
	id := strings.TrimPrefix(e.Item, "exec:")
	cmd := "flowstate session unlock " + id
	if e.Owner == "" {
		return fmt.Sprintf("%v: %s is held by another process; if it is gone, run `%s`", ErrSessionBusy, e.Item, cmd)
	}
	why := "held"
	if e.Unjudgeable {
		why = "held by a lease that cannot be judged (another machine or an older writer)"
	}
	host, pid := e.Host, "unknown"
	if host == "" {
		host = "unknown"
	}
	if e.Pid != 0 {
		pid = strconv.FormatInt(e.Pid, 10)
	}
	return fmt.Sprintf("%v: %s %s: owner %s, host %s, pid %s, since %s; if it is gone, run `%s`",
		ErrSessionBusy, e.Item, why, e.Owner, host, pid, e.AcquiredAtUTC, cmd)
}

// Is reports a match with ErrSessionBusy.
func (e *SessionBusyError) Is(target error) bool { return target == ErrSessionBusy }

func busyFromRow(item string, r leaseRow, unjudgeable bool) *SessionBusyError {
	return &SessionBusyError{Item: item, Owner: r.Agent, Host: r.Host, Pid: r.Pid,
		AcquiredAtUTC: r.AcquiredAtUTC, Unjudgeable: unjudgeable}
}

// AcquireRunLease takes the exec lease for item under owner, judging a
// present row by the advisory lock and the machine (ADR-011 rule 5). It takes
// the item's lock without blocking first; a held lock is refused with
// *SessionBusyError (errors.Is ErrSessionBusy). With the lock held, one
// transaction re-reads the row: none is inserted (token from lease_history);
// a row of this machine is a crashed run's and is taken over by compare-and-set
// on owner and token, token plus one, recorded in lease_history; a row with an
// empty or another machine cannot be judged and is refused, never taken over.
// Every failure, including a panic, releases the lock. The handle keeps the
// lock until Release. A transaction that fails with SQLITE_BUSY or
// SQLITE_LOCKED is retried a bounded number of times with the lock held; the
// final error stays typed as the driver error, never ErrSessionBusy.
//
// Residual (ADR-011): FlowState never deletes the lock file; if something
// removes it while a lease is held, a second acquire creates a new file and
// succeeds, so two holders can coexist. Fence detects the loser by token.
func (s *Store) AcquireRunLease(ctx context.Context, item, owner string) (*RunLease, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	if item == "" || owner == "" {
		return nil, errors.New("store: lease item and owner must not be empty")
	}
	machine := MachineIdentity()
	if machine == "" {
		return nil, errors.New("store: no machine identity: cannot judge a lease")
	}
	lock, err := AcquireItemLock(s.locks, s.path, item)
	if errors.Is(err, ErrLockHeld) {
		return nil, s.heldError(ctx, item)
	}
	if err != nil {
		return nil, err
	}
	runLeaseAfterLockHook(item)
	h := &RunLease{store: s, lock: lock, item: item, owner: owner, machine: machine, pid: int64(os.Getpid())}
	h.host, _ = hostName()
	ok := false
	defer func() {
		if !ok {
			_ = lock.Release()
		}
	}()
	token, err := s.claimRunLease(ctx, h)
	if err != nil {
		return nil, err
	}
	h.token = token
	ok = true
	return h, nil
}

// heldError builds the refusal for a held lock from the row, when there is one.
func (s *Store) heldError(ctx context.Context, item string) error {
	var rows []leaseRow
	err := s.gdb.WithContext(ctx).Where(map[string]any{"item": item}).Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return &SessionBusyError{Item: item}
	}
	return busyFromRow(item, rows[0], false)
}

// claimRunLease runs the judgement transaction, retrying a busy database.
func (s *Store) claimRunLease(ctx context.Context, h *RunLease) (int64, error) {
	for attempt := 1; ; attempt++ {
		token, err := s.claimRunLeaseOnce(ctx, h, attempt)
		if err == nil || !isSQLiteBusy(err) {
			return token, err
		}
		if attempt >= runLeaseTxAttempts {
			return 0, fmt.Errorf("store: lease transaction still busy after %d attempts: %w", attempt, err)
		}
		timer := time.NewTimer(time.Duration(attempt) * runLeaseBackoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, ctx.Err()
		case <-timer.C:
		}
	}
}

// isSQLiteBusy reports a driver error with primary code SQLITE_BUSY (5) or
// SQLITE_LOCKED (6).
func isSQLiteBusy(err error) bool {
	var coded interface{ Code() int }
	if !errors.As(err, &coded) {
		return false
	}
	code := coded.Code() & 0xff
	return code == 5 || code == 6
}

// claimRunLeaseOnce is one attempt: one transaction, returning the token.
func (s *Store) claimRunLeaseOnce(ctx context.Context, h *RunLease, attempt int) (int64, error) {
	var token int64
	err := s.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := runLeaseTxHook(attempt); err != nil {
			return err
		}
		var rows []leaseRow
		if err := tx.Where(map[string]any{"item": h.item}).Limit(1).Find(&rows).Error; err != nil {
			return fmt.Errorf("store: read lease: %w", err)
		}
		var err error
		switch {
		case len(rows) == 0:
			token, err = insertRunLease(tx, h)
		case rows[0].Machine == "" || rows[0].Machine != h.machine:
			return busyFromRow(h.item, rows[0], true)
		default:
			token, err = swapRunLease(tx, h, rows[0])
		}
		return err
	})
	return token, err
}

func nowUTC() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

func insertRunLease(tx *gorm.DB, h *RunLease) (int64, error) {
	var hist []leaseHistoryRow
	err := tx.Where(map[string]any{"item": h.item}).Order("token desc").Limit(1).Find(&hist).Error
	if err != nil {
		return 0, fmt.Errorf("store: read lease history: %w", err)
	}
	token := int64(1)
	if len(hist) > 0 {
		token = hist[0].Token + 1
	}
	row := leaseRow{Item: h.item, Agent: h.owner, Token: token, AcquiredAtUTC: nowUTC(),
		Machine: h.machine, Host: h.host, Pid: h.pid}
	if err := tx.Create(&row).Error; err != nil {
		return 0, fmt.Errorf("store: insert lease: %w", err)
	}
	return token, recordRunLeaseToken(tx, h.item, token)
}

func swapRunLease(tx *gorm.DB, h *RunLease, old leaseRow) (int64, error) {
	runLeaseBeforeSwapHook(tx, h.item)
	token := old.Token + 1
	res := tx.Model(&leaseRow{}).
		Where(map[string]any{"item": h.item, "agent": old.Agent, "token": old.Token}).
		Updates(map[string]any{"agent": h.owner, "token": token, "acquired_at_utc": nowUTC(),
			"machine": h.machine, "host": h.host, "pid": h.pid})
	if res.Error != nil {
		return 0, fmt.Errorf("store: take over lease: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		var cur []leaseRow
		if err := tx.Where(map[string]any{"item": h.item}).Limit(1).Find(&cur).Error; err != nil || len(cur) == 0 {
			return 0, &SessionBusyError{Item: h.item}
		}
		return 0, busyFromRow(h.item, cur[0], false)
	}
	return token, recordRunLeaseToken(tx, h.item, token)
}

func recordRunLeaseToken(tx *gorm.DB, item string, token int64) error {
	err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&leaseHistoryRow{Item: item, Token: token}).Error
	if err != nil {
		return fmt.Errorf("store: record lease token: %w", err)
	}
	return nil
}

// Item returns the leased item.
func (h *RunLease) Item() string { return h.item }

// Owner returns the owner the lease was taken under.
func (h *RunLease) Owner() string { return h.owner }

// Token returns the fencing token of the lease.
func (h *RunLease) Token() int64 { return h.token }

// Machine returns the machine identity recorded in the row.
func (h *RunLease) Machine() string { return h.machine }

// Host returns the host name recorded in the row (display only).
func (h *RunLease) Host() string { return h.host }

// Pid returns the process id recorded in the row (display only).
func (h *RunLease) Pid() int64 { return h.pid }

// Fence reports whether the handle still holds the lease: it fails with
// ErrLeaseFenced once another owner or token has replaced the row.
func (h *RunLease) Fence(ctx context.Context) error {
	return h.store.FenceLease(ctx, h.item, h.owner, h.token)
}

// Release deletes the row only if it still has this owner and token, then
// releases the lock, which is released even when the delete fails. It is
// idempotent: a second call returns nil.
func (h *RunLease) Release(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released {
		return nil
	}
	h.released = true
	var derr error
	if err := h.store.checkOpen(); err != nil {
		derr = err
	} else if err := h.store.gdb.WithContext(ctx).
		Where(map[string]any{"item": h.item, "agent": h.owner, "token": h.token}).
		Delete(&leaseRow{}).Error; err != nil {
		derr = fmt.Errorf("store: delete lease: %w", err)
	}
	return errors.Join(derr, h.lock.Release())
}
