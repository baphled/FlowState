package registry

import (
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/zai"
)

// and CLI stay provider-agnostic; this factory is the only place a
// concrete adapter is selected (M1-CORE-REQUIREMENTS §3).
func Resolve(name string) (provider.Provider, error) {
	switch name {
	case "z.ai", "zai":
		return zai.NewFromEnv()
	default:
		return nil, fmt.Errorf("registry: unknown provider %q", name)
	}
}
