package learning

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// CollectionEnsurer creates a missing collection on demand. The Qdrant
// client implements this via EnsureCollection.
type CollectionEnsurer interface {
	EnsureCollection(ctx context.Context, name string, vectorSize int, distance string) error
}

// MissingCollectionDetector reports whether an error returned by the
// underlying VectorStoreClient signals "the collection does not exist
// yet". The qdrant package supplies the canonical implementation via
// qdrant.IsCollectionNotFound; alternative backends pass a predicate
// that matches their own error shape.
type MissingCollectionDetector func(error) bool

// EnsuringVectorStore wraps a VectorStoreClient and lazily creates the
// target collection the first time the wrapped store reports it as
// missing. Subsequent writes hit the underlying store directly.
//
// This is the auto-create path for the learning capture pipeline:
// before any other request has populated the collection, the first
// upsert against a fresh Qdrant instance returns 404. Rather than
// silently dropping the entry (and emitting the
// "qdrant: HTTP 404: Collection does not exist" warning), the wrapper
// creates the collection — using the dim of the just-embedded vector —
// and retries the upsert exactly once.
//
// The wrapper is safe for concurrent use; an internal mutex guards
// the per-collection "already ensured" cache so concurrent first
// writes do not all racing-create.
type EnsuringVectorStore struct {
	inner    VectorStoreClient
	ensurer  CollectionEnsurer
	missing  MissingCollectionDetector
	distance string

	mu      sync.Mutex
	ensured map[string]bool
}

// NewEnsuringVectorStore wraps inner so the first upsert that returns
// a "missing collection" error triggers a CreateCollection then a
// retry.
//
// Expected:
//   - inner is non-nil.
//   - ensurer is non-nil and creates collections when invoked.
//   - missing is non-nil and recognises the underlying store's
//     "collection not found" error shape.
//   - distance is the Qdrant distance metric used when creating a
//     fresh collection (e.g. "Cosine").
//
// Returns:
//   - A *EnsuringVectorStore implementing VectorStoreClient.
//
// Side effects:
//   - None until Upsert is invoked.
func NewEnsuringVectorStore(
	inner VectorStoreClient,
	ensurer CollectionEnsurer,
	missing MissingCollectionDetector,
	distance string,
) *EnsuringVectorStore {
	return &EnsuringVectorStore{
		inner:    inner,
		ensurer:  ensurer,
		missing:  missing,
		distance: distance,
		ensured:  make(map[string]bool),
	}
}

// Upsert forwards points to the wrapped store, auto-creating the
// collection on the first 404 (or backend-equivalent) and retrying.
//
// Expected:
//   - points carries at least one entry whose Vector defines the
//     collection's dimensionality. Empty points slices skip the
//     auto-create branch and propagate the underlying response.
//
// Returns:
//   - nil on success after at most one auto-create + retry.
//   - The underlying error when auto-create is not applicable, or the
//     joined error when create / retry fails.
//
// Side effects:
//   - May PUT /collections/<name> against the configured ensurer.
func (e *EnsuringVectorStore) Upsert(
	ctx context.Context,
	collection string,
	points []VectorPoint,
	wait bool,
) error {
	err := e.inner.Upsert(ctx, collection, points, wait)
	if err == nil {
		return nil
	}
	if !e.shouldEnsure(err, points) {
		return err
	}
	if cerr := e.ensure(ctx, collection, len(points[0].Vector)); cerr != nil {
		return errors.Join(err, cerr)
	}
	if rerr := e.inner.Upsert(ctx, collection, points, wait); rerr != nil {
		return fmt.Errorf("retry upsert after auto-create: %w", rerr)
	}
	return nil
}

// shouldEnsure reports whether the auto-create path should run for the
// given upsert failure. The path is gated on a recognised "missing
// collection" error and a non-empty point slice (so vector size is
// resolvable).
//
// Expected:
//   - err is non-nil; this helper is invoked only on the failure path.
//
// Returns:
//   - true when err matches the missing-collection detector and points
//     is non-empty; false otherwise.
//
// Side effects:
//   - None.
func (e *EnsuringVectorStore) shouldEnsure(err error, points []VectorPoint) bool {
	if e.missing == nil || e.ensurer == nil {
		return false
	}
	if !e.missing(err) {
		return false
	}
	return len(points) > 0 && len(points[0].Vector) > 0
}

// ensure delegates to the configured CollectionEnsurer at most once
// per collection, regardless of concurrent callers. Subsequent calls
// short-circuit so the wrapper does not re-issue a CreateCollection on
// every transient 404.
//
// Expected:
//   - vectorSize is the dimensionality of vectors stored in the
//     collection.
//
// Returns:
//   - nil after a successful (or already-cached) ensure call.
//   - A wrapped error from the ensurer otherwise.
//
// Side effects:
//   - Records collection in e.ensured on success.
func (e *EnsuringVectorStore) ensure(ctx context.Context, collection string, vectorSize int) error {
	e.mu.Lock()
	if e.ensured[collection] {
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()

	if err := e.ensurer.EnsureCollection(ctx, collection, vectorSize, e.distance); err != nil {
		return fmt.Errorf("auto-create collection %q (size=%d distance=%s): %w",
			collection, vectorSize, e.distance, err)
	}

	e.mu.Lock()
	e.ensured[collection] = true
	e.mu.Unlock()
	return nil
}

// Search forwards directly to the wrapped store; the auto-create
// branch is only meaningful for writes.
//
// Expected:
//   - Pass-through; see VectorStoreClient.Search.
//
// Returns:
//   - The wrapped Search result and error.
//
// Side effects:
//   - None beyond the underlying Search call.
func (e *EnsuringVectorStore) Search(
	ctx context.Context,
	collection string,
	vector []float64,
	limit int,
) ([]ScoredVectorPoint, error) {
	return e.inner.Search(ctx, collection, vector, limit)
}

var _ VectorStoreClient = (*EnsuringVectorStore)(nil)
