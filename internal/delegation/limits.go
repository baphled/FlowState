package delegation

import "time"

// SpawnLimits defines the default constraints for delegation fan-out.
type SpawnLimits struct {
	MaxDepth                int
	MaxConcurrentPerSession int
	MaxTotalBudget          int
	StaleTimeout            time.Duration
}

// DefaultSpawnLimits returns the standard delegation spawn limits.
//
// Returns:
//   - The default spawn limit configuration.
//
// Side effects:
//   - None.
func DefaultSpawnLimits() SpawnLimits {
	return SpawnLimits{
		MaxDepth:                5,
		MaxConcurrentPerSession: 10,
		MaxTotalBudget:          50,
		StaleTimeout:            45 * time.Minute,
	}
}

// ExceedsDepth reports whether the provided depth reaches the configured maximum.
//
// Expected:
//   - depth is the current delegation depth.
//
// Returns:
//   - True when the depth meets or exceeds the configured limit.
//
// Side effects:
//   - None.
func (s SpawnLimits) ExceedsDepth(depth int) bool {
	return depth >= s.MaxDepth
}

// ExceedsBudget reports whether the active count reaches the configured budget.
//
// Expected:
//   - active is the current active delegation count.
//
// Returns:
//   - True when the active count meets or exceeds the configured budget.
//
// Side effects:
//   - None.
func (s SpawnLimits) ExceedsBudget(active int) bool {
	return active >= s.MaxTotalBudget
}
