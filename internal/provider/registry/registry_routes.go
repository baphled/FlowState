package registry

import (
	"os"
	"strings"

	"github.com/baphled/flowstate/internal/config"
)

// codingRouteFor resolves the coding-plan wire format: explicit
// ZAI_CODING_ROUTE / providers.zai.route wins; otherwise a config
// host that names the anthropic surface means the operator's host
// only speaks the anthropic wire format, so infer route=anthropic
// rather than posting the openai shape there.
func codingRouteFor(cfg *config.ProviderConfig) string {
	route := os.Getenv("ZAI_CODING_ROUTE")
	if route == "" && cfg != nil {
		route = cfg.Route
	}
	if route == "" && strings.Contains(hostForRouteInference(cfg), "/api/anthropic") {
		return "anthropic"
	}
	return route
}

// hostForRouteInference returns the resolved host in precedence
// order for route inference: ZAI_BASE_URL, then config host.
func hostForRouteInference(cfg *config.ProviderConfig) string {
	if h := os.Getenv("ZAI_BASE_URL"); h != "" {
		return h
	}
	if cfg != nil {
		return cfg.Host
	}
	return ""
}
