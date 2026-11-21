// Package registry maps provider names to their concrete adapters so
// the engine and CLI stay provider-agnostic.
package registry

import (
	"errors"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/config"
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

// resolveAnthropic builds the anthropic adapter from providers.anthropic. An
// empty or `oauth` auth_type selects the subscription route: the user's own
// `claude` binary (FLOWSTATE_CLAUDE_BIN, default `claude`). Resolving never
// runs the binary or checks sign-in; both happen on the first turn. A config
// file that exists but cannot be read or parsed is an error, never a silent
// fallback to the subscription route; an absent file is the default config.
func resolveAnthropic() (provider.Provider, error) {
	var cfg *config.ProviderConfig
	if path, err := config.Path(); err == nil { // no config dir means no file
		doc, err := config.Load(path)
		if err != nil {
			return nil, fmt.Errorf("registry: anthropic: %w", err)
		}
		cfg = doc.GetProvider("anthropic")
	}
	authType := ""
	if cfg != nil {
		authType = string(cfg.AuthType)
	}
	switch authType {
	case "", "oauth":
		return anthropic.NewSubscription(os.Getenv("FLOWSTATE_CLAUDE_BIN"), anthropic.ExecLauncher), nil
	case "api_key":
		return resolveAnthropicKey(cfg)
	default:
		return nil, fmt.Errorf("registry: unknown anthropic auth_type %q (supported: oauth, api_key)", authType)
	}
}

// resolveAnthropicKey builds the API-key route. The key comes from
// ANTHROPIC_API_KEY, else providers.anthropic.api_key; the base URL from
// ANTHROPIC_BASE_URL, else providers.anthropic.host, else the Anthropic API.
// The key is never included in an error.
func resolveAnthropicKey(cfg *config.ProviderConfig) (provider.Provider, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	host := os.Getenv("ANTHROPIC_BASE_URL")
	if cfg != nil {
		if key == "" {
			key = cfg.APIKey
		}
		if host == "" {
			host = cfg.Host
		}
	}
	if key == "" {
		return nil, errors.New("registry: anthropic auth_type api_key needs a key: " +
			"set ANTHROPIC_API_KEY or providers.anthropic.api_key in " + config.FileName)
	}
	return anthropic.NewAPIKey(key, host, nil), nil
}

// resolveZai builds the z.ai adapter from providers.yaml
// (providers.zai.plan / host / route), with env vars still overriding:
// ZAI_API_KEY, ZAI_BASE_URL, ZAI_PLAN and ZAI_CODING_ROUTE win over
// config values. Precedence for the coding-plan host: explicit
// config host / ZAI_BASE_URL > plan-tag default (openai coding
// endpoint, selectable "anthropic" via providers.zai.route).
// An unset plan defaults to coding (see zaiPlan).
func resolveZai() (provider.Provider, error) {
	var cfg *config.ProviderConfig
	if doc, err := config.LoadDefault(); err == nil {
		cfg = doc.GetProvider("zai")
	}
	opts := []zai.Option{}
	if host := os.Getenv("ZAI_BASE_URL"); host != "" {
		opts = append(opts, zai.WithBaseURL(host))
	} else if cfg != nil && cfg.Host != "" {
		opts = append(opts, zai.WithBaseURL(cfg.Host))
	}
	if zaiPlan(cfg) == "coding" {
		opts = append(opts, zai.WithCoding())
		if route := codingRouteFor(cfg); route != "" {
			opts = append(opts, zai.WithCodingRoute(route))
		}
	}
	key := os.Getenv("ZAI_API_KEY")
	if key == "" && cfg != nil {
		key = cfg.APIKey
	}
	if key == "" {
		return nil, errors.New("zai: ZAI_API_KEY is not set")
	}
	return zai.New(key, opts...), nil
}
