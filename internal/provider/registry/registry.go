// Package registry maps provider names to their concrete adapters so
// the engine and CLI stay provider-agnostic.
package registry

import (
	"errors"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
	"github.com/baphled/flowstate/internal/provider/zai"
)

// Resolve returns the concrete provider adapter for name; the engine
// and CLI stay provider-agnostic, this factory is the only place a
// concrete adapter is selected (M1-CORE-REQUIREMENTS §3).
func Resolve(name string) (provider.Provider, error) {
	switch name {
	case "z.ai", "zai":
		return resolveZai()
	case "anthropic":
		return resolveAnthropic()
	default:
		return nil, fmt.Errorf("registry: unknown provider %q", name)
	}
}

// resolveAnthropic builds the anthropic adapter. FLOWSTATE_ANTHROPIC_AUTH
// selects the route: unset, empty or `oauth` is the subscription route, the
// user's own `claude` binary (FLOWSTATE_CLAUDE_BIN, default `claude`);
// `api_key` is the Anthropic API. The route is never inferred from
// ANTHROPIC_API_KEY, which users export for other tools. Resolving never runs
// the binary or checks sign-in; both happen on the first turn.
func resolveAnthropic() (provider.Provider, error) {
	switch auth := os.Getenv("FLOWSTATE_ANTHROPIC_AUTH"); auth {
	case "", "oauth":
		return anthropic.NewSubscription(os.Getenv("FLOWSTATE_CLAUDE_BIN"), anthropic.ExecLauncher), nil
	case "api_key":
		return resolveAnthropicKey()
	default:
		return nil, fmt.Errorf("registry: unknown FLOWSTATE_ANTHROPIC_AUTH %q (supported: oauth, api_key)", auth)
	}
}

// resolveAnthropicKey builds the API-key route from ANTHROPIC_API_KEY
// (required) and ANTHROPIC_BASE_URL (default: the Anthropic API). The key is
// never included in an error.
func resolveAnthropicKey() (provider.Provider, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, errors.New("registry: FLOWSTATE_ANTHROPIC_AUTH=api_key needs a key: ANTHROPIC_API_KEY is not set")
	}
	return anthropic.NewAPIKey(key, os.Getenv("ANTHROPIC_BASE_URL"), nil), nil
}

// resolveZai builds the z.ai adapter from the environment only: ZAI_API_KEY
// (required), ZAI_BASE_URL, ZAI_PLAN (unset means coding, see zaiPlan) and
// ZAI_CODING_ROUTE (see codingRouteFor).
func resolveZai() (provider.Provider, error) {
	opts := []zai.Option{}
	if host := os.Getenv("ZAI_BASE_URL"); host != "" {
		opts = append(opts, zai.WithBaseURL(host))
	}
	if zaiPlan() == "coding" {
		opts = append(opts, zai.WithCoding())
		if route := codingRouteFor(); route != "" {
			opts = append(opts, zai.WithCodingRoute(route))
		}
	}
	key := os.Getenv("ZAI_API_KEY")
	if key == "" {
		return nil, errors.New("zai: ZAI_API_KEY is not set")
	}
	// Every resolved provider reports finish reasons so truncation is visible.
	opts = append(opts, zai.WithFinishEvents())
	return zai.New(key, opts...), nil
}
