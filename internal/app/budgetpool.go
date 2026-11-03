package app

// NewBudgetPool creates a pool with total budget; total <= 0 means
// unlimited and Reserve always grants the full request.
func NewBudgetPool(total int64) *BudgetPool {
	return &BudgetPool{remain: total}
}

// Reserve draws at most want from the pool and reports how much was
// granted. When the pool is unlimited (total <= 0) the full want is
// granted. Granted amounts are tracked so Exhausted can distinguish
// a capped pool that is spent from one that never had a cap.
func (b *BudgetPool) Reserve(want int64) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remain <= 0 && b.granted == 0 && want > 0 {
		// unlimited pool: total was <= 0 at construction
		return want
	}
	granted := want
	if b.remain < granted {
		granted = b.remain
	}
	if granted < 0 {
		granted = 0
	}
	b.remain -= granted
	b.granted += granted
	return granted
}

// Remaining reports the budget left in the pool.
func (b *BudgetPool) Remaining() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.remain
}

// Release returns unused budget to the pool when a child consumed
// less than it reserved.
func (b *BudgetPool) Release(unused int64) {
	if unused <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.remain += unused
	b.granted -= unused
	if b.remain < 0 {
		b.remain = 0
	}
}
