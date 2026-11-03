// Package registry maps provider names to their concrete adapters so
// the engine and CLI stay provider-agnostic.
package registry

import (
	"errors"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/zai"
)

// Resolve returns the concrete provider adapter for name; the engine
// and CLI stay provider-agnostic, this factory is the only place a
// concrete adapter is selected (M1-CORE-REQUIREMENTS §3).
func Resolve(name string) (provider.Provider, error) {
	switch name {
	case "z.ai", "zai":
		return resolveZai()
	default:
		return nil, fmt.Errorf("registry: unknown provider %q", name)
	}
}

// resolveZai builds the z.ai adapter from config.yaml
// (providers.zai.plan / host), with env vars still overriding:
// ZAI_API_KEY, ZAI_BASE_URL and ZAI_PLAN win over config values.
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
	plan := os.Getenv("ZAI_PLAN")
	if plan == "" && cfg != nil {
		plan = cfg.Plan
	}
	if plan == "coding" {
		opts = append(opts, zai.WithCoding())
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
