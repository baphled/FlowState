package app

// ReservationError reports why a reservation operation failed.
type ReservationError string

func (e ReservationError) Error() string { return string(e) }

// Reservation ledger failure modes (fail-closed).
const (
	// ErrReservationChildEmpty: the child id must not be empty.
	ErrReservationChildEmpty ReservationError = "child id must not be empty"
	// ErrReservationAmountInvalid: reserve/charge amounts must be positive.
	ErrReservationAmountInvalid ReservationError = "amount must be positive"
	// ErrReservationDuplicate: the child already holds a reservation.
	ErrReservationDuplicate ReservationError = "child already holds a reservation"
	// ErrReservationUnknown: no reservation is held for the child.
	ErrReservationUnknown ReservationError = "no reservation held for child"
	// ErrReservationCharged: the reservation has already been charged once.
	ErrReservationCharged ReservationError = "reservation already charged"
	// ErrReservationOverCharge: the charge exceeds the granted reservation.
	ErrReservationOverCharge ReservationError = "charge exceeds granted reservation"
)
