package app

import "fmt"

// Error renders the failure as it was printed before the error was typed:
// the cancellation (when any), ErrReservationSettle, then Detail.
func (e *SettleError) Error() string {
	msg := fmt.Sprintf("%s: %s", ErrReservationSettle, e.Detail())
	if e.Cancelled != nil {
		return fmt.Sprintf("%s: %s", e.Cancelled, msg)
	}
	return msg
}

// Detail is the part of the message after "reservation_settle_failed: ":
// the session and the settle cause. A settle that followed a cancellation
// says the session is cancelled.
func (e *SettleError) Detail() string {
	if e.Cancelled != nil {
		return fmt.Sprintf("session %s is cancelled: %v", e.SessionID, e.Cause)
	}
	return fmt.Sprintf("session %s: %v", e.SessionID, e.Cause)
}

// SettleCause returns the failure the store reported for the settle.
func (e *SettleError) SettleCause() error { return e.Cause }

// Unwrap returns the errors errors.Is and errors.As see through: the
// cancellation (when any), ErrReservationSettle and the settle cause.
func (e *SettleError) Unwrap() []error {
	errs := make([]error, 0, 3)
	if e.Cancelled != nil {
		errs = append(errs, e.Cancelled)
	}
	return append(errs, ErrReservationSettle, e.Cause)
}
