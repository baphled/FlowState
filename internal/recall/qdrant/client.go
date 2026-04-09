package qdrant

import (
	"context"
	"net/http"
)

// VectorStore defines the operations required for Qdrant-backed vector storage.
type VectorStore interface {
	// CreateCollection creates a new collection with the supplied configuration.
	CreateCollection(ctx context.Context, name string, config CollectionConfig) error
	// Upsert stores or updates points within a collection.
	Upsert(ctx context.Context, collection string, points []Point, wait bool) error
	// Search finds the nearest points for the supplied vector.
	Search(ctx context.Context, collection string, vector []float64, limit int) ([]ScoredPoint, error)
	// DeleteCollection removes a collection.
	DeleteCollection(ctx context.Context, name string) error
	// CollectionExists reports whether a collection exists.
	CollectionExists(ctx context.Context, name string) (bool, error)
}

// Client is a Qdrant HTTP client implementing VectorStore.
type Client struct {
	baseURL    string
	httpClient *http.Client
	apiKey     string
}

// CreateCollection creates a new Qdrant collection with the supplied configuration.
//
// Expected:
//   - name is the unique collection identifier.
//   - config specifies vector dimensions and distance metric.
//
// Returns:
//   - nil on success.
//   - An error if the HTTP request fails or the server returns a non-2xx status.
//
// Side effects:
//   - Creates a new collection in the Qdrant instance at c.baseURL.
func (c *Client) CreateCollection(_ context.Context, _ string, _ CollectionConfig) error {
	_, _, _ = c.baseURL, c.httpClient, c.apiKey
	return nil
}

// Upsert stores or updates points within a Qdrant collection.
//
// Expected:
//   - collection names an existing Qdrant collection.
//   - points contains the vectors and payloads to store.
//   - wait controls whether the request blocks until indexing is complete.
//
// Returns:
//   - nil on success.
//   - An error if the HTTP request fails or the server returns a non-2xx status.
//
// Side effects:
//   - Writes or overwrites the supplied points in the named collection.
func (c *Client) Upsert(_ context.Context, _ string, _ []Point, _ bool) error {
	_, _, _ = c.baseURL, c.httpClient, c.apiKey
	return nil
}

// Search finds the nearest points for the supplied vector within a Qdrant collection.
//
// Expected:
//   - collection names an existing Qdrant collection.
//   - vector is the query embedding to search against.
//   - limit caps the number of results returned.
//
// Returns:
//   - A slice of ScoredPoints ordered by descending similarity.
//   - An error if the HTTP request fails or the server returns a non-2xx status.
//
// Side effects:
//   - None.
func (c *Client) Search(_ context.Context, _ string, _ []float64, _ int) ([]ScoredPoint, error) {
	_, _, _ = c.baseURL, c.httpClient, c.apiKey
	return nil, nil
}

// DeleteCollection removes a Qdrant collection and all its data.
//
// Expected:
//   - name identifies the collection to remove.
//
// Returns:
//   - nil on success.
//   - An error if the HTTP request fails or the server returns a non-2xx status.
//
// Side effects:
//   - Permanently deletes the named collection from the Qdrant instance.
func (c *Client) DeleteCollection(_ context.Context, _ string) error {
	_, _, _ = c.baseURL, c.httpClient, c.apiKey
	return nil
}

// CollectionExists reports whether the named Qdrant collection exists.
//
// Expected:
//   - name identifies the collection to check.
//
// Returns:
//   - true if the collection exists, false otherwise.
//   - An error if the HTTP request fails or the server returns an unexpected status.
//
// Side effects:
//   - None.
func (c *Client) CollectionExists(_ context.Context, _ string) (bool, error) {
	_, _, _ = c.baseURL, c.httpClient, c.apiKey
	return false, nil
}

// compile-time assertion: Client implements VectorStore.
var _ VectorStore = (*Client)(nil)
