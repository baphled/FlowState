// Package web provides a web fetching tool implementation.
package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/baphled/flowstate/internal/tool"
)

// minExtractedText is the minimum length of extracted text required before the
// tool treats extraction as successful. Shorter results fall back to the raw
// body so callers always receive something usable.
const minExtractedText = 50

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
//
// Expected: parameters for Name.
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
//
// Expected: parameters for Description.
func (t *Tool) Description() string {
	return "Fetch content from a URL via HTTP GET, returning extracted text for HTML pages. Truncated to 10KB."
}

// Schema returns the input schema for the web tool.
//
// Returns:
//   - A tool.Schema describing the required url property.
//
// Side effects:
//   - None.
//
// Expected: parameters for Schema.
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

	raw := string(body)
	if !isHTML(resp.Header.Get("Content-Type"), raw) {
		return tool.Result{Output: raw}, nil
	}

	extracted := extractTextFromHTML(raw)
	if len(extracted) < minExtractedText {
		return tool.Result{Output: raw}, nil
	}
	if len(extracted) > maxBodySize {
		extracted = extracted[:maxBodySize]
	}
	return tool.Result{Output: extracted}, nil
}

// isHTML reports whether a response should be treated as HTML for text
// extraction. It returns true when the Content-Type header names text/html or
// the body opens with an HTML document marker, matching case-insensitively.
//
// Expected: parameters for isHTML.
// Returns: result of isHTML.
// Side effects: None.
func isHTML(contentType, body string) bool {
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return true
	}
	trimmed := strings.TrimLeft(strings.ToLower(body), " \t\r\n")
	return strings.HasPrefix(trimmed, "<!doctype") || strings.HasPrefix(trimmed, "<html")
}

// extractTextFromHTML strips HTML markup from raw, returning only visible text
// content. Script, style, and noscript elements are dropped along with the
// head section other than the title; HTML comments and all remaining tags are
// removed. Excessive whitespace is collapsed so consecutive blank lines become
// a single blank line and each line is trimmed.
//
// Expected: parameters for extractTextFromHTML.
// Returns: result of extractTextFromHTML.
// Side effects: None.
func extractTextFromHTML(raw string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(raw))
	var buf strings.Builder
	var inScriptStyle, inHead, inTitle bool

	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			break
		}

		switch tt {
		case html.StartTagToken:
			tag := strings.ToLower(string(tagName(tokenizer)))
			switch tag {
			case "script", "style", "noscript":
				inScriptStyle = true
			case "head":
				inHead = true
			case "title":
				inTitle = true
			}
		case html.EndTagToken:
			tag := strings.ToLower(string(tagName(tokenizer)))
			switch tag {
			case "script", "style", "noscript":
				inScriptStyle = false
			case "head":
				inHead = false
			case "title":
				inTitle = false
			}
		case html.TextToken:
			if inScriptStyle || (inHead && !inTitle) {
				continue
			}
			buf.Write(tokenizer.Text())
		}
	}

	return collapseWhitespace(buf.String())
}

// tagName returns the name of the current tag token.
//
// Expected: parameters for tagName.
// Returns: result of tagName.
// Side effects: None.
func tagName(t *html.Tokenizer) []byte {
	name, _ := t.TagName()
	return name
}

// collapseWhitespace trims each line and reduces runs of blank lines to a
// single blank line, removing leading and trailing blank lines entirely.
//
// Expected: parameters for collapseWhitespace.
// Returns: result of collapseWhitespace.
// Side effects: None.
func collapseWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	prevBlank := true
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if !prevBlank {
				out = append(out, "")
				prevBlank = true
			}
			continue
		}
		out = append(out, trimmed)
		prevBlank = false
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
