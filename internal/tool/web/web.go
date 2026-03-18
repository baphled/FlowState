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

type Tool struct {
	client *http.Client
}

func New() *Tool {
	return &Tool{
		client: &http.Client{Timeout: timeout},
	}
}

func NewWithClient(client *http.Client) *Tool {
	return &Tool{client: client}
}

func (t *Tool) Name() string {
	return "web"
}

func (t *Tool) Description() string {
	return "Fetch content from a URL via HTTP GET, truncated to 10KB"
}

func (t *Tool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
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

func (t *Tool) Execute(ctx context.Context, input tool.ToolInput) (tool.ToolResult, error) {
	url, ok := input.Arguments["url"].(string)
	if !ok || url == "" {
		return tool.ToolResult{}, errors.New("url argument is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return tool.ToolResult{Error: fmt.Errorf("invalid URL: %w", err)}, nil
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return tool.ToolResult{Error: fmt.Errorf("fetch failed: %w", err)}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return tool.ToolResult{Error: fmt.Errorf("read body failed: %w", err)}, nil
	}

	return tool.ToolResult{Output: string(body)}, nil
}
