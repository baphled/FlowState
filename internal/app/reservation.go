package app

// Reserve draws at most want from the pool for child and records the
// granted amount. It fails closed on an empty child id, a non-positive
// amount or a duplicate reservation; the pool is untouched on error.
func (l *ReservationLedger) Reserve(child string, want int64) (int64, error) {
	if child == "" {
		return 0, ErrReservationChildEmpty
	}
	if want <= 0 {
		return 0, ErrReservationAmountInvalid
	}
	if _, exists := l.byChild[child]; exists {
		return 0, ErrReservationDuplicate
	}
	granted := l.pool.Reserve(want)
	if granted <= 0 {
		return 0, nil
	}
	l.byChild[child] = &reservation{child: child, granted: granted}
	return granted, nil
}

// Charge settles actual use for child exactly once, returning the
// unused remainder to the pool. It fails closed when the child holds
// no reservation, when the reservation is already charged, or when
// the charge exceeds the granted amount.
func (l *ReservationLedger) Charge(child string, used int64) error {
	if used <= 0 {
		return ErrReservationAmountInvalid
	}
	r, ok := l.byChild[child]
	if !ok {
		return ErrReservationUnknown
	}
	if r.charged {
		return ErrReservationCharged
	}
	if used > r.granted {
		return ErrReservationOverCharge
	}
	l.pool.Release(r.granted - used)
	r.charged = true
	return nil
}

// Granted reports the amount reserved for child, or false when the
// child holds no reservation.
func (l *ReservationLedger) Granted(child string) (int64, bool) {
	r, ok := l.byChild[child]
	if !ok {
		return 0, false
	}
	return r.granted, true
}
