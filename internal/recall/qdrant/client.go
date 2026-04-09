package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultTimeout  = 30 * time.Second
	collectionsPath = "/collections/"
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

// NewClient creates a new Qdrant HTTP client.
//
// Expected:
//   - baseURL is the Qdrant server address (e.g. "http://localhost:6333").
//   - apiKey is the optional API key for authentication; pass empty string to skip.
//   - httpClient is the HTTP client to use; if nil, a default client with 30s timeout is created.
//
// Returns:
//   - A configured *Client ready for use.
//
// Side effects:
//   - None.
func NewClient(baseURL, apiKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		baseURL:    baseURL,
		httpClient: httpClient,
		apiKey:     apiKey,
	}
}

// searchResponse is the JSON envelope returned by the Qdrant search endpoint.
type searchResponse struct {
	Result []ScoredPoint `json:"result"`
}

// do sends an HTTP request and returns the response body and status code.
//
// Expected:
//   - ctx carries cancellation and deadline signals.
//   - method is a valid HTTP method (GET, PUT, POST, DELETE).
//   - path starts with a leading slash.
//
// Returns:
//   - The response body bytes, HTTP status code, and nil on success.
//   - nil bytes, zero status, and a wrapped error on transport failure.
//
// Side effects:
//   - Sends an HTTP request to c.baseURL + path.
func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshalling request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("creating request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading response body: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

// requireOK returns a *Error if the HTTP status code is outside the 2xx range.
//
// Expected:
//   - body is the raw response body from the server.
//   - statusCode is the HTTP status code from the response.
//
// Returns:
//   - nil when statusCode is in the 2xx range.
//   - A *Error containing the status code and body otherwise.
//
// Side effects:
//   - None.
func requireOK(body []byte, statusCode int) error {
	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
		return nil
	}
	return &Error{StatusCode: statusCode, Message: string(body)}
}

// CreateCollection creates a new Qdrant collection with the supplied configuration.
//
// Expected:
//   - name is the unique collection identifier.
//   - config specifies vector dimensions and distance metric.
//
// Returns:
//   - nil on success.
//   - A *Error if the server returns a non-2xx status.
//
// Side effects:
//   - Creates a new collection in the Qdrant instance at c.baseURL.
func (c *Client) CreateCollection(ctx context.Context, name string, config CollectionConfig) error {
	payload := map[string]any{
		"vectors": map[string]any{
			"size":     config.VectorSize,
			"distance": config.Distance,
		},
	}
	body, status, err := c.do(ctx, http.MethodPut, collectionsPath+name, payload)
	if err != nil {
		return fmt.Errorf("creating collection %q: %w", name, err)
	}
	return requireOK(body, status)
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
//   - A *Error if the server returns a non-2xx status.
//
// Side effects:
//   - Writes or overwrites the supplied points in the named collection.
func (c *Client) Upsert(ctx context.Context, collection string, points []Point, wait bool) error {
	path := collectionsPath + collection + "/points"
	if wait {
		path += "?wait=true"
	}
	payload := map[string]any{
		"points": points,
	}
	body, status, err := c.do(ctx, http.MethodPut, path, payload)
	if err != nil {
		return fmt.Errorf("upserting points to %q: %w", collection, err)
	}
	return requireOK(body, status)
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
//   - A *Error if the server returns a non-2xx status.
//
// Side effects:
//   - None.
func (c *Client) Search(ctx context.Context, collection string, vector []float64, limit int) ([]ScoredPoint, error) {
	payload := map[string]any{
		"vector": vector,
		"limit":  limit,
	}
	body, status, err := c.do(ctx, http.MethodPost, collectionsPath+collection+"/points/search", payload)
	if err != nil {
		return nil, fmt.Errorf("searching collection %q: %w", collection, err)
	}
	if err := requireOK(body, status); err != nil {
		return nil, err
	}
	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decoding search response: %w", err)
	}
	return resp.Result, nil
}

// DeleteCollection removes a Qdrant collection and all its data.
//
// Expected:
//   - name identifies the collection to remove.
//
// Returns:
//   - nil on success.
//   - A *Error if the server returns a non-2xx status.
//
// Side effects:
//   - Permanently deletes the named collection from the Qdrant instance.
func (c *Client) DeleteCollection(ctx context.Context, name string) error {
	body, status, err := c.do(ctx, http.MethodDelete, collectionsPath+name, nil)
	if err != nil {
		return fmt.Errorf("deleting collection %q: %w", name, err)
	}
	return requireOK(body, status)
}

// CollectionExists reports whether the named Qdrant collection exists.
//
// Expected:
//   - name identifies the collection to check.
//
// Returns:
//   - true if the collection exists, false otherwise.
//   - A *Error if the server returns an unexpected status (neither 200 nor 404).
//
// Side effects:
//   - None.
func (c *Client) CollectionExists(ctx context.Context, name string) (bool, error) {
	body, status, err := c.do(ctx, http.MethodGet, collectionsPath+name, nil)
	if err != nil {
		return false, fmt.Errorf("checking collection %q: %w", name, err)
	}
	if status == http.StatusOK {
		return true, nil
	}
	if status == http.StatusNotFound {
		return false, nil
	}
	return false, &Error{StatusCode: status, Message: string(body)}
}

// compile-time assertion: Client implements VectorStore.
var _ VectorStore = (*Client)(nil)
