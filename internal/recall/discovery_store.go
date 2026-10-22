package recall

// DiscoveryStore defines operations for publishing, querying, and watching discovery events.
type DiscoveryStore interface {
	// Publish adds an event to the store.
	Publish(event any) error
	// Query returns events matching the filter (nil for all).
	Query(filter any) ([]any, error)
	// Watch streams new events as they are published.
	Watch() (<-chan any, error)
}
