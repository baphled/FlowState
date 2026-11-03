package app

// reservation is a single child's reserve/charge entry. It is the
// value type backing ReservationLedger; the ledger owns all access.
type reservation struct {
	child   string
	granted int64
	charged bool
}

// ReservationLedger performs reserve-before-delegate, charge-once
// accounting over a BudgetPool. Reserve draws a grant from the pool
// and records it against a child; Charge settles actual use exactly
// once, returning any unused budget to the pool. All operations fail
// closed on unknown or already-settled reservations.
type ReservationLedger struct {
	pool    *BudgetPool
	byChild map[string]*reservation
}
