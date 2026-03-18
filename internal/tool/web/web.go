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
func New() *Tool {
	return &Tool{
		client: &http.Client{Timeout: timeout},
	}
}

// NewWithClient creates a new web tool with the given HTTP client.
func NewWithClient(client *http.Client) *Tool {
	return &Tool{client: client}
}

// Name returns the tool name.
func (t *Tool) Name() string {
	return "web"
}

// Description returns the tool description.
func (t *Tool) Description() string {
	return "Fetch content from a URL via HTTP GET, truncated to 10KB"
}

// Schema returns the input schema for the tool.
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
