// Package web provides a web fetching tool implementation.
package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/baphled/flowstate/internal/tool"
)

const (
	timeout     = 10 * time.Second
	maxBodySize = 10 * 1024
)

// Tool implements a web fetching tool.
type Tool struct {
	client *http.Client
}

// New creates a new web tool with the default HTTP client.
//
// Returns:
//   - A configured web Tool with a 10-second timeout.
//
// Side effects:
//   - None.
func New() *Tool {
	return &Tool{
		client: &http.Client{Timeout: timeout},
	}
}

// NewWithClient creates a new web tool with the given HTTP client.
//
// Expected:
//   - client is a non-nil HTTP client to use for requests.
//
// Returns:
//   - A configured web Tool using the provided client.
//
// Side effects:
//   - None.
func NewWithClient(client *http.Client) *Tool {
	return &Tool{client: client}
}

// Name returns the tool name.
//
// Returns:
//   - The string "web".
//
// Side effects:
//   - None.
func (t *Tool) Name() string {
	return "web"
}

// Description returns the tool description.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *Tool) Description() string {
	return "Fetch content from a URL via HTTP GET, truncated to 10KB"
}

// Schema returns the input schema for the web tool.
//
// Returns:
//   - A tool.Schema describing the required url property.
//
// Side effects:
//   - None.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"url": {
				Type:        "string",
				Description: "The URL to fetch",
			},
		},
		Required: []string{"url"},
	}
}

// Execute fetches content from the URL specified in the input.
//
// Expected:
//   - ctx is a valid context for the HTTP request.
//   - input contains a "url" string argument.
//
// Returns:
//   - A tool.Result containing the fetched content, truncated to 10KB.
//   - An error if the url argument is missing.
//
// Side effects:
//   - Makes an HTTP GET request to the specified URL.
func (t *Tool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	url, ok := input.Arguments["url"].(string)
	if !ok || url == "" {
		return tool.Result{}, errors.New("url argument is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return tool.Result{Error: fmt.Errorf("invalid URL: %w", err)}, nil
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return tool.Result{Error: fmt.Errorf("fetch failed: %w", err)}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return tool.Result{Error: fmt.Errorf("read body failed: %w", err)}, nil
	}

	return tool.Result{Output: string(body)}, nil
}
