package app

// ReservationError reports why a reservation operation failed.
type ReservationError string

// Error implements error for ReservationError.
func (e ReservationError) Error() string { return string(e) }

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
