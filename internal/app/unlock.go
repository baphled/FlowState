package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// ErrLeaseLive reports a lease whose lock is held by a live process on this
// machine; stop that process instead. See LeaseLiveError for the holder.
var ErrLeaseLive = errors.New("app: lease is held by a live process on this machine")

// ErrLeaseChanged reports that the lease row was replaced between the read and
// the delete, so nothing was deleted; run unlock again to see the new holder.
var ErrLeaseChanged = errors.New("app: lease changed while unlocking")

// Error names the holder.
func (e *LeaseLiveError) Error() string {
	return fmt.Sprintf("%v: session %s held by %s (host %s, pid %d, token %d); stop that process",
		ErrLeaseLive, e.SessionID, e.Holder.Owner, e.Holder.Host, e.Holder.Pid, e.Holder.Token)
}

// Unwrap lets errors.Is match ErrLeaseLive.
func (e *LeaseLiveError) Unwrap() error { return ErrLeaseLive }

// unlockBeforeDeleteHook runs with the lease lock held, just before the
// compare-and-delete; specs use it to act inside that window.
var unlockBeforeDeleteHook = func(string) {}

// unlockBeforeAppendHook runs after the audit writer of the given attempt (0
// based) exists and before it appends; specs use it to force a seq collision.
var unlockBeforeAppendHook = func(sessionID string, attempt int) {}

// unlockAppendAttempts bounds the audit append on store.ErrSeqCollision.
const unlockAppendAttempts = 3

// UnlockSession reports, or with force deletes, the exec lease row of a
// session (ADR-011 D8). It never takes the lease it inspects.
//
//   - No row: UnlockNoLease, nothing written.
//   - A row of this machine whose lock is held: a live holder; it returns a
//     *LeaseLiveError (matching ErrLeaseLive) even with force, and writes nothing.
//   - Any other row (this machine with a free lock, another machine, or a legacy
//     row with no machine) cannot be proved live: without force it is reported
//     and nothing is deleted; with force it is deleted only if still the row
//     read (owner and token), and one engine event session.unlocked is appended
//     to the log, unless the session is terminal (the log is then left alone).
//
// A legacy row holds no lock, so force deletes it even if its run continues; the
// report says so. The lock is held for the whole call so a run cannot acquire
// between the check and the delete. An unknown session is store.ErrUnknownSession.
//
// Without force the call is a report of the lease row and writes nothing to the
// log, but for a row of this machine it still creates the lock file under
// .locks beside the database: "read-only" means the row and the log.
//
// The audit append retries up to three times on store.ErrSeqCollision, each
// time with a fresh writer. A live legacy writer appending at the same moment
// can still win every attempt; the unlocker then reports the failure (the row is
// deleted, EventAppended false) and the legacy writer's next append, not the
// unlocker, takes the seq collision. A session that became terminal during the
// call gets no event.
//
// The report carries the row's Owner and Host verbatim, so they can hold control
// characters; the command (packet 6) must print them through the terminal-safe
// JSON helper rather than raw. LeaseLiveError.Error is likewise unescaped.
func UnlockSession(ctx context.Context, st *Store, sessionID string, force bool) (UnlockReport, error) {
	sess, err := st.GetSession(ctx, sessionID)
	if err != nil {
		return UnlockReport{}, err
	}
	item := "exec:" + sessionID
	report := UnlockReport{SessionID: sessionID, Item: item, Outcome: UnlockNoLease,
		Message: fmt.Sprintf("session %s has no lease; nothing to do", sessionID)}
	info, found, err := st.ReadLease(ctx, item)
	if err != nil || !found {
		return report, err
	}
	report.Holder = UnlockHolder{Owner: info.Owner, Host: info.Host, Pid: info.Pid, Token: info.Token, AcquiredAt: info.AcquiredAt}
	report.LegacyRow = info.Machine == ""
	report.MachineMatches = !report.LegacyRow && info.Machine == store.MachineIdentity()
	report.Outcome = UnlockReported
	if report.MachineMatches {
		lock, lockErr := st.TryItemLock(item)
		if errors.Is(lockErr, store.ErrLockHeld) {
			report.Message = "lease is held by a live process on this machine; stop it instead"
			return report, &LeaseLiveError{SessionID: sessionID, Holder: report.Holder}
		}
		if lockErr != nil {
			return report, fmt.Errorf("unlock: check lease lock: %w", lockErr)
		}
		defer func() { _ = lock.Release() }()
	}
	if !force {
		report.Message = reportText(report) + "; nothing deleted; --force would delete this row. " + forceWarning
		return report, nil
	}
	return forceUnlock(ctx, st, sess, info, report)
}

// forceWarning is the caution unlock --force prints (ADR-011).
const forceWarning = "--force cannot tell whether a row it did not lock is still running: check first."

// forceUnlock deletes the row read and appends the audit event.
func forceUnlock(ctx context.Context, st *Store, sess *store.Session, info store.LeaseInfo, report UnlockReport) (UnlockReport, error) {
	unlockBeforeDeleteHook(info.Item)
	deleted, err := st.DeleteLeaseIf(ctx, info.Item, info.Owner, info.Token)
	if err != nil {
		return report, fmt.Errorf("unlock: %w", err)
	}
	if !deleted {
		return report, fmt.Errorf("%w: %s", ErrLeaseChanged, info.Item)
	}
	report.Outcome = UnlockDeleted
	report.Message = reportText(report) + "; deleted by --force."
	if report.LegacyRow {
		report.Message += " A legacy row holds no lock: if its run is still alive it continues as a second writer."
	}
	now, err := st.GetSession(ctx, sess.ID)
	if err != nil {
		return report, fmt.Errorf("unlock: lease deleted but the audit event failed: %w", err)
	}
	if isTerminalStatus(sess.Status) || isTerminalStatus(now.Status) {
		return report, nil
	}
	if err := appendUnlockedRetry(ctx, st, sess.ID, report); err != nil {
		return report, fmt.Errorf("unlock: lease deleted but the audit event failed: %w", err)
	}
	report.EventAppended = true
	return report, nil
}

// appendUnlockedRetry appends the event, retrying on a seq collision with a
// fresh writer each attempt.
func appendUnlockedRetry(ctx context.Context, st *Store, sessionID string, r UnlockReport) error {
	var err error
	for attempt := 0; attempt < unlockAppendAttempts; attempt++ {
		if err = appendUnlocked(ctx, st, sessionID, r, attempt); !errors.Is(err, store.ErrSeqCollision) {
			return err
		}
	}
	return err
}

// appendUnlocked writes session.unlocked with a writer of its own: no lease.
func appendUnlocked(ctx context.Context, st *Store, sessionID string, r UnlockReport, attempt int) error {
	w, err := st.NewEventWriter(ctx, sessionID)
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()
	unlockBeforeAppendHook(sessionID, attempt)
	_, err = w.Append(ctx, session.ActorEngine, session.TypeSessionUnlocked, UnlockedPayload{
		Owner: r.Holder.Owner, Host: r.Holder.Host, Pid: r.Holder.Pid, Token: r.Holder.Token,
		Forced: true, MachineMatches: r.MachineMatches, LegacyRow: r.LegacyRow,
	})
	return err
}

// isTerminalStatus reports a session state that accepts no further events.
func isTerminalStatus(status string) bool {
	return status == "completed" || status == "cancelled" || status == "failed"
}

// reportText describes the holder and what kind of row it is.
func reportText(r UnlockReport) string {
	kind := "another machine's row, which cannot be judged"
	switch {
	case r.LegacyRow:
		kind = "legacy row (no machine, no lock)"
	case r.MachineMatches:
		kind = "this machine, lock free: the holder is dead"
	}
	return fmt.Sprintf("lease %s held by %s (host %s, pid %d, token %d, acquired %s): %s",
		r.Item, r.Holder.Owner, r.Holder.Host, r.Holder.Pid, r.Holder.Token, r.Holder.AcquiredAt, kind)
}
