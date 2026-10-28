package registry_test

import (
	"testing"

	"github.com/baphled/flowstate/internal/provider/registry"
	"github.com/baphled/flowstate/internal/provider/zai"
)

func TestResolveZaiFromEnv(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "k")
	p, err := registry.Resolve("z.ai")
	if err != nil {
		t.Fatalf("Resolve(z.ai): %v", err)
	}
	if p.Name() != "z.ai" {
		t.Fatalf("Name = %q", p.Name())
	}
	if _, ok := p.(*zai.Provider); !ok {
		t.Fatalf("want *zai.Provider, got %T", p)
	}
}

func TestResolveUnknownName(t *testing.T) {
	if _, err := registry.Resolve("nope"); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestResolveZaiMissingKey(t *testing.T) {
	if _, err := registry.Resolve("z.ai"); err == nil {
		t.Fatal("expected error when ZAI_API_KEY unset")
	}
}
