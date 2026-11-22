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
	head := "session " + e.SessionID
	if e.Cancelled != nil {
		head += " is cancelled"
	}
	if e.Cause == nil {
		return head
	}
	return fmt.Sprintf("%s: %v", head, e.Cause)
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
	errs = append(errs, ErrReservationSettle)
	if e.Cause != nil {
		errs = append(errs, e.Cause)
	}
	return errs
}

// SettleErrors returns every SettleError in err's tree, in the order they
// occur, walking errors.Join and any Unwrap() []error as well as single
// Unwrap() error chains. A SettleError is not searched further: its own
// cause belongs to it. It returns nil for a nil error or a tree with none.
func SettleErrors(err error) []*SettleError {
	if err == nil {
		return nil
	}
	// Inspect the concrete node itself: errors.As would also descend into a
	// SettleError's cause and skip the ordering this walk guarantees.
	var node any = err
	switch e := node.(type) {
	case *SettleError:
		return []*SettleError{e}
	case interface{ Unwrap() []error }:
		var out []*SettleError
		for _, c := range e.Unwrap() {
			out = append(out, SettleErrors(c)...)
		}
		return out
	case interface{ Unwrap() error }:
		return SettleErrors(e.Unwrap())
	}
	return nil
}
