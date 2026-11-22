package registry

import (
	"os"
	"strings"
)

// codingRouteFor resolves the coding-plan wire format: an explicit
// ZAI_CODING_ROUTE wins; otherwise a ZAI_BASE_URL that names the anthropic
// surface means the host only speaks the anthropic wire format, so infer
// route=anthropic rather than posting the openai shape there.
func codingRouteFor() string {
	route := os.Getenv("ZAI_CODING_ROUTE")
	if route == "" && strings.Contains(os.Getenv("ZAI_BASE_URL"), "/api/anthropic") {
		return "anthropic"
	}
	return route
}
