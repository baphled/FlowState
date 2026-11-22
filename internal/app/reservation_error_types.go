package app

// ReservationError reports why a reservation operation failed.
type ReservationError string

// ErrReservationChildEmpty reports that the child id must not be empty.
const ErrReservationChildEmpty ReservationError = "child id must not be empty"

// ErrReservationAmountInvalid reports that reserve/charge amounts must be positive.
const ErrReservationAmountInvalid ReservationError = "amount must be positive"

// ErrReservationDuplicate reports that the child already holds a reservation.
const ErrReservationDuplicate ReservationError = "child already holds a reservation"

// ErrReservationUnknown reports that no reservation is held for the child.
const ErrReservationUnknown ReservationError = "no reservation held for child"

// ErrReservationCharged reports that the reservation has already been charged once.
const ErrReservationCharged ReservationError = "reservation already charged"

// ErrReservationOverCharge reports that the charge exceeds the granted reservation.
const ErrReservationOverCharge ReservationError = "charge exceeds granted reservation"

// SettleError reports that a session's budget reservation could not be
// settled. It carries the cause and the session id structurally, so a
// caller (the run command) reads them with errors.As instead of cutting
// the message text. Unwrap exposes ErrReservationSettle, the original
// cause and, when the settle followed a cancellation, that cancellation
// (ErrSessionCancelled), so errors.Is holds for each.
type SettleError struct {
	// SessionID names the session whose reservation stayed unsettled.
	SessionID string
	// Cause is the failure the store returned.
	Cause error
	// Cancelled is the cancellation the settle followed, or nil when the
	// settle was not part of a session cancellation.
	Cancelled error
}
