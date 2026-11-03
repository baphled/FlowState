package app

// ReservationError reports why a reservation operation failed.
type ReservationError string

// Error implements error for ReservationError.
func (e ReservationError) Error() string { return string(e) }

// ErrReservationChildEmpty: the child id must not be empty.
const ErrReservationChildEmpty ReservationError = "child id must not be empty"

// ErrReservationAmountInvalid: reserve/charge amounts must be positive.
const ErrReservationAmountInvalid ReservationError = "amount must be positive"

// ErrReservationDuplicate: the child already holds a reservation.
const ErrReservationDuplicate ReservationError = "child already holds a reservation"

// ErrReservationUnknown: no reservation is held for the child.
const ErrReservationUnknown ReservationError = "no reservation held for child"

// ErrReservationCharged: the reservation has already been charged once.
const ErrReservationCharged ReservationError = "reservation already charged"

// ErrReservationOverCharge: the charge exceeds the granted reservation.
const ErrReservationOverCharge ReservationError = "charge exceeds granted reservation"
