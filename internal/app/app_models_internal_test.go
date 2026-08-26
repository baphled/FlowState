package app

import (
	"context"
	"errors"
	"testing"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

// stubModelsProvider is a minimal provider.Registry entry whose Models
// output is scripted per test, covering ListModels and SetModel paths
// without any network access.
type stubModelsProvider struct {
	name   string
	models []provider.Model
	err    error
}

func (s *stubModelsProvider) Name() string { return s.name }

func (s *stubModelsProvider) Models() ([]provider.Model, error) {
	return s.models, s.err
}

func (s *stubModelsProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errors.New("stream not supported by stub")
}

func (s *stubModelsProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errors.New("chat not supported by stub")
}

func (s *stubModelsProvider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	return nil, errors.New("embed not supported by stub")
}

// newModelsTestApp builds an App with just enough wiring for the
// model-selection accessors, avoiding the full New bootstrap.
func newModelsTestApp(t *testing.T) *App {
	t.Helper()
	return &App{}
}

func TestListModelsEmptyWithoutRegistry(t *testing.T) {
	a := newModelsTestApp(t)
	models, err := a.ListModels()
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("expected empty model list, got %d entries", len(models))
	}
}

func TestListModelsAggregatesAcrossProviders(t *testing.T) {
	a := newModelsTestApp(t)
	registry := provider.NewRegistry()
	registry.Register(&stubModelsProvider{name: "alpha", models: []provider.Model{{ID: "a-1", Provider: "alpha"}, {ID: "a-2", Provider: "alpha"}}})
	registry.Register(&stubModelsProvider{name: "beta", models: []provider.Model{{ID: "b-1", Provider: "beta"}}})
	a.SetProviderRegistry(registry)

	models, err := a.ListModels()
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if len(models) != 3 {
		t.Errorf("expected 3 aggregated models, got %d", len(models))
	}
}

func TestListModelsPropagatesProviderError(t *testing.T) {
	a := newModelsTestApp(t)
	registry := provider.NewRegistry()
	registry.Register(&stubModelsProvider{name: "broken", err: errors.New("upstream down")})
	a.SetProviderRegistry(registry)

	if _, err := a.ListModels(); err == nil {
		t.Fatal("expected ListModels to surface provider error")
	}
}

func TestSetModelRejectsMissingWiring(t *testing.T) {
	a := newModelsTestApp(t)
	if err := a.SetModel("any"); err == nil {
		t.Error("expected error when engine is nil")
	}
}

func TestSetModelRejectsUnknownModel(t *testing.T) {
	a := newModelsTestApp(t)
	a.Engine = newEngineForModelTest()
	registry := provider.NewRegistry()
	registry.Register(&stubModelsProvider{name: "alpha", models: []provider.Model{{ID: "a-1", Provider: "alpha"}}})
	a.SetProviderRegistry(registry)

	if err := a.SetModel("nope"); err == nil {
		t.Error("expected error for unknown model id")
	}
}

func TestShutdownNilClientSafe(t *testing.T) {
	a := newModelsTestApp(t)
	if err := a.Shutdown(); err != nil {
		t.Errorf("Shutdown on nil mcp client returned error: %v", err)
	}
}

// newEngineForModelTest builds a default-configured engine for the
// SetModel happy path without touching any provider network.
func newEngineForModelTest() *engine.Engine {
	return engine.New(engine.Config{})
}

func TestSetModelAppliesPreference(t *testing.T) {
	a := newModelsTestApp(t)
	a.Engine = newEngineForModelTest()
	registry := provider.NewRegistry()
	registry.Register(&stubModelsProvider{name: "alpha", models: []provider.Model{{ID: "a-1", Provider: "alpha"}}})
	a.SetProviderRegistry(registry)

	if err := a.SetModel("a-1"); err != nil {
		t.Fatalf("SetModel returned error: %v", err)
	}
	if got := a.Engine.LastModel(); got != "a-1" {
		t.Errorf("expected model preference a-1 applied, engine reports %q", got)
	}
}
