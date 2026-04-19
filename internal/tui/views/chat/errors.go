// Package chat provides error formatting utilities for the chat view.
package chat

import (
	"fmt"
	"regexp"
	"strings"
)

// httpErrorPattern matches HTTP error strings like POST "https://api.example.com/v1/messages": 404 Not Found.
var httpErrorPattern = regexp.MustCompile(
	`^(\w+)\s+"(https?://[^"]+)":\s+(\d+\s+\S[\w\s]*)(?:\s+(.+))?$`,
)

// modelInBodyPattern extracts model names from JSON error bodies.
var modelInBodyPattern = regexp.MustCompile(`"model[:\s]*([a-zA-Z0-9._/-]+)"`)

// messageInBodyPattern extracts human-readable messages from JSON error bodies.
var messageInBodyPattern = regexp.MustCompile(`"message":\s*"([^"]+)"`)

const maxFallbackLength = 100

// FormatErrorMessage parses a raw error and returns a structured, readable display string.
//
// Expected:
//   - err is a non-nil error from a provider streaming operation.
//
// Returns:
//   - A formatted multi-line string for HTTP errors with extracted fields.
//   - A truncated single-line fallback for unparseable errors.
//
// Side effects:
//   - None.
func FormatErrorMessage(err error) string {
	errMsg := err.Error()
	if matches := httpErrorPattern.FindStringSubmatch(errMsg); matches != nil {
		return buildHTTPErrorDisplay(matches)
	}
	return buildFallbackDisplay(errMsg)
}

// buildHTTPErrorDisplay formats an HTTP error into a structured multi-line display.
//
// Expected:
//   - matches contains [full, method, url, status, body] from httpErrorPattern.
//
// Returns:
//   - A multi-line formatted error string with provider, model, and detail fields.
//
// Side effects:
//   - None.
func buildHTTPErrorDisplay(matches []string) string {
	url := matches[2]
	status := strings.TrimSpace(matches[3])
	body := matches[4]

	providerName := extractProviderFromURL(url)
	modelName := extractModelFromBody(body)
	detail := extractDetailFromBody(body)

	var sb strings.Builder
	fmt.Fprintf(&sb, "⚠ API Error (%s)", status)
	if providerName != "" {
		fmt.Fprintf(&sb, "\n  Provider: %s", providerName)
	}
	if modelName != "" {
		fmt.Fprintf(&sb, "\n  Model: %s", modelName)
	}
	if detail != "" {
		fmt.Fprintf(&sb, "\n  Detail: %s", detail)
	}
	return sb.String()
}

// buildFallbackDisplay returns a single-line error marker for unparseable
// messages.
//
// P18a switched the fallback prefix to a compact "[ERROR: …]" marker so
// error blocks in the transcript read uniformly regardless of whether the
// underlying failure was network, auth, quota, or miscellaneous provider
// noise. The marker reuses the terminal's error-tone styling at the
// renderer layer — the prefix itself is plain ASCII so copy/paste and
// log-scraping remain trivial.
//
// Expected:
//   - errMsg is the raw error string.
//
// Returns:
//   - A single line of the form "[ERROR: <msg>]", truncated if the
//     underlying message exceeds maxFallbackLength.
//
// Side effects:
//   - None.
func buildFallbackDisplay(errMsg string) string {
	if len(errMsg) > maxFallbackLength {
		return "[ERROR: " + errMsg[:maxFallbackLength] + "...]"
	}
	return "[ERROR: " + errMsg + "]"
}

// extractProviderFromURL extracts a provider name from an API URL hostname.
//
// Expected:
//   - url is a valid HTTP(S) URL string.
//
// Returns:
//   - A provider name like "anthropic" or "openai", or empty string if not recognised.
//
// Side effects:
//   - None.
func extractProviderFromURL(url string) string {
	switch {
	case strings.Contains(url, "anthropic"):
		return "anthropic"
	case strings.Contains(url, "openai"):
		return "openai"
	case strings.Contains(url, "ollama"):
		return "ollama"
	default:
		return ""
	}
}

// extractModelFromBody extracts a model name from a JSON error body.
//
// Expected:
//   - body is a JSON string that may contain a model reference.
//
// Returns:
//   - The model name if found, or empty string.
//
// Side effects:
//   - None.
func extractModelFromBody(body string) string {
	if body == "" {
		return ""
	}
	if matches := modelInBodyPattern.FindStringSubmatch(body); matches != nil {
		return matches[1]
	}
	return ""
}

// extractDetailFromBody extracts a human-readable detail from a JSON error body.
//
// Expected:
//   - body is a JSON string that may contain a "message" field.
//
// Returns:
//   - The extracted detail message, or empty string.
//
// Side effects:
//   - None.
func extractDetailFromBody(body string) string {
	if body == "" {
		return ""
	}
	if matches := messageInBodyPattern.FindStringSubmatch(body); matches != nil {
		return matches[1]
	}
	return ""
}
