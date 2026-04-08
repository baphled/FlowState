// Package websearch provides a web search tool backed by Exa.
package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/baphled/flowstate/internal/tool"
)

const (
	defaultEndpoint = "https://api.exa.ai/search"
	defaultTimeout  = 10 * time.Second
	defaultNumItems = 10
)

// Tool implements an Exa-backed web search tool.
type Tool struct {
	endpoint   string
	apiKey     string
	httpClient *http.Client
}

// searchRequest models the request sent to Exa.
type searchRequest struct {
	Query      string `json:"query"`
	NumResults int    `json:"numResults,omitempty"`
	Type       string `json:"type,omitempty"`
}

// searchResult models one Exa search result.
type searchResult struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text,omitempty"`
}

// searchResponse models the Exa response payload.
type searchResponse struct {
	Results []searchResult `json:"results"`
	Error   string         `json:"error,omitempty"`
}

// New creates a new web search tool.
//
// Returns:
//   - A Tool configured for Exa search requests.
//
// Expected:
//   - endpoint may be empty to use the default endpoint.
//
// Side effects:
//   - None.
func New(endpoint, apiKey string) *Tool {
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	return &Tool{
		endpoint: endpoint,
		apiKey:   apiKey,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

// Name returns the tool name.
//
// Returns:
//   - The string "websearch".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Name() string { return "websearch" }

// Description returns the tool description.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Description() string { return "Search the web using Exa" }

// Schema returns the tool schema.
//
// Returns:
//   - A schema describing query, numResults, and timeout.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"query": {
				Type:        "string",
				Description: "The search query",
			},
			"numResults": {
				Type:        "integer",
				Description: "The number of results to return",
			},
			"timeout": {
				Type:        "integer",
				Description: "Request timeout in milliseconds",
			},
		},
		Required: []string{"query"},
	}
}

// Execute searches Exa and formats the results for the agent.
//
// Expected:
//   - input contains a non-empty query string.
//
// Returns:
//   - A tool.Result containing formatted results or an error.
//
// Side effects:
//   - Sends an HTTP request to Exa.
func (t *Tool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	query, ok := input.Arguments["query"].(string)
	if !ok || query == "" {
		queryErr := errors.New("query argument is required")
		return tool.Result{Error: queryErr}, nil
	}

	numResults := parsePositiveInt(input.Arguments["numResults"], defaultNumItems)
	requestTimeout := parseTimeout(input.Arguments["timeout"], defaultTimeout)

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	payload, err := json.Marshal(searchRequest{Query: query, NumResults: numResults, Type: "auto"})
	if err != nil {
		return tool.Result{}, fmt.Errorf("encoding request: %w", err)
	}

	output, err := executeSearch(reqCtx, t, payload)
	if err != nil {
		return tool.Result{Error: err}, nil
	}

	return tool.Result{Output: output}, nil
}

// formatResults serialises search results for output.
//
// Expected:
//   - results contains zero or more search results.
//
// Returns:
//   - A JSON string or an error.
//
// Side effects:
//   - None.
func formatResults(results []searchResult) (string, error) {
	payload, err := json.Marshal(results)
	if err != nil {
		return "", fmt.Errorf("encoding results: %w", err)
	}
	return string(payload), nil
}

// executeSearch sends the Exa request and decodes the response.
//
// Expected:
//   - ctx is valid and t is configured with an endpoint.
//
// Returns:
//   - The formatted search results or an error.
//
// Side effects:
//   - Sends an HTTP request and closes the response body.
func executeSearch(ctx context.Context, t *Tool, payload []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	if t.apiKey != "" {
		req.Header.Set("x-api-key", t.apiKey)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("search failed: exa returned %s", resp.Status)
	}

	var parsed searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	return formatResults(parsed.Results)
}

// parsePositiveInt converts a raw value into a positive integer.
//
// Expected:
//   - raw may be a numeric or string value.
//
// Returns:
//   - A positive integer or the default value.
//
// Side effects:
//   - None.
func parsePositiveInt(raw any, defaultValue int) int {
	parsed, err := toInt(raw)
	if err == nil && parsed > 0 {
		return parsed
	}
	return defaultValue
}

// parseTimeout converts a raw timeout value into a duration.
//
// Expected:
//   - raw is either a numeric value in milliseconds or a string integer.
//
// Returns:
//   - A positive duration derived from raw, or defaultValue when raw is invalid.
//
// Side effects:
//   - None.
func parseTimeout(raw any, defaultValue time.Duration) time.Duration {
	parsed, err := toInt(raw)
	if err == nil && parsed > 0 {
		return time.Duration(parsed) * time.Millisecond
	}
	return defaultValue
}

// toInt converts a supported value into an integer.
//
// Expected:
//   - value is an int, int64, float64, or string containing an integer.
//
// Returns:
//   - The converted integer, or an error when the type is unsupported or parsing fails.
//
// Side effects:
//   - None.
func toInt(value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case string:
		return strconv.Atoi(v)
	default:
		return 0, fmt.Errorf("unsupported integer type %T", value)
	}
}
