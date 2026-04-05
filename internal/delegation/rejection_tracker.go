package delegation

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/coordination"
)

const defaultMaxRejections = 3

// RejectionTracker counts plan reviewer rejections per chain ID.
// It is backed by a coordination store so the count persists across
// delegation calls within the same planning session.
type RejectionTracker struct {
	store      coordination.Store
	maxAllowed int
}

// NewRejectionTracker returns a RejectionTracker backed by the given store.
// If maxRejections is zero or negative, defaultMaxRejections is used.
//
// Expected:
//   - store is a non-nil coordination.Store implementation.
//   - maxRejections is the maximum number of rejections before exhaustion.
//
// Returns:
//   - A pointer to a new RejectionTracker ready for use.
//
// Side effects:
//   - Allocates a new RejectionTracker instance.
func NewRejectionTracker(store coordination.Store, maxRejections int) *RejectionTracker {
	if maxRejections <= 0 {
		maxRejections = defaultMaxRejections
	}

	return &RejectionTracker{store: store, maxAllowed: maxRejections}
}

// Record increments the rejection count for the given chainID and returns
// the new count. Returns an error if the store operation fails.
//
// Expected:
//   - chainID is a non-empty string identifying the delegation chain.
//
// Returns:
//   - The new rejection count and nil error on success.
//   - 0 and an error if the store operation fails.
//
// Side effects:
//   - Increments the counter stored at the rejection key in the backing store.
func (r *RejectionTracker) Record(_ context.Context, chainID string) (int, error) {
	count, err := r.store.Increment(rejectionKey(chainID))
	if err != nil {
		return 0, fmt.Errorf("recording rejection for chain %q: %w", chainID, err)
	}

	return count, nil
}

// Count returns the current rejection count for the given chainID.
//
// Expected:
//   - chainID identifies the delegation chain to inspect.
//
// Returns:
//   - The current count for chainID.
//   - nil when the count can be read or no count exists.
//
// Side effects:
//   - Reads the backing coordination store.
func (r *RejectionTracker) Count(_ context.Context, chainID string) (int, error) {
	val, err := r.store.Get(rejectionKey(chainID))
	if errors.Is(err, coordination.ErrKeyNotFound) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("reading rejection count for chain %q: %w", chainID, err)
	}

	if len(val) == 0 {
		return 0, nil
	}

	var n int
	if _, scanErr := fmt.Sscanf(string(val), "%d", &n); scanErr != nil {
		return 0, fmt.Errorf("parsing rejection count for chain %q: %w", chainID, scanErr)
	}

	return n, nil
}

// ExhaustedFor reports whether the rejection count for chainID has reached
// or exceeded the maximum.
//
// Expected:
//   - chainID identifies the delegation chain to inspect.
//
// Returns:
//   - true when the rejection count is at or above the configured maximum.
//   - false otherwise.
//   - An error if the current count cannot be read.
//
// Side effects:
//   - Reads the backing coordination store.
func (r *RejectionTracker) ExhaustedFor(ctx context.Context, chainID string) (bool, error) {
	count, err := r.Count(ctx, chainID)
	if err != nil {
		return false, err
	}

	return count >= r.maxAllowed, nil
}

// Reset clears the rejection count for the given chainID.
//
// Expected:
//   - chainID identifies the delegation chain to reset.
//
// Returns:
//   - nil when the count is cleared.
//   - An error if the backing store delete operation fails.
//
// Side effects:
//   - Deletes the stored count from the coordination store.
func (r *RejectionTracker) Reset(_ context.Context, chainID string) error {
	return r.store.Delete(rejectionKey(chainID))
}

// rejectionKey builds the coordination-store key used for rejection counts.
//
// Expected:
//   - chainID is a non-empty string identifying the delegation chain.
//
// Returns:
//   - The store key string for the rejection counter.
//
// Side effects:
//   - None.
func rejectionKey(chainID string) string {
	return chainID + "/rejection-count"
}
