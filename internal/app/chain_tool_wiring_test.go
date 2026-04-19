package app

import (
	"context"
	"testing"

	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tool"
)

func toolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name())
	}
	return names
}

// stubTool is a minimal tool.Tool implementation for testing tool list composition.
type stubTool struct {
	name string
}

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return "" }
func (s *stubTool) Schema() tool.Schema { return tool.Schema{} }
func (s *stubTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	return tool.Result{}, nil
}

func TestAppendChainTools_AddsChainToolsWhenStoreNonNil(t *testing.T) {
	base := []tool.Tool{}
	store := recall.NewInMemoryChainStore(nil)

	result := appendChainTools(base, store)

	names := toolNames(result)
	if len(names) != 2 {
		t.Fatalf("expected 2 tools, got %d: %v", len(names), names)
	}
	if names[0] != "chain_search_context" {
		t.Errorf("expected chain_search_context, got %s", names[0])
	}
	if names[1] != "chain_get_messages" {
		t.Errorf("expected chain_get_messages, got %s", names[1])
	}
}

func TestAppendChainTools_ReturnsOriginalSliceWhenStoreNil(t *testing.T) {
	base := []tool.Tool{}

	result := appendChainTools(base, nil)

	if len(result) != 0 {
		t.Fatalf("expected empty slice, got %d tools", len(result))
	}
}

func TestAppendChainTools_PreservesExistingTools(t *testing.T) {
	stub := &stubTool{name: "existing_tool"}
	base := []tool.Tool{stub}
	store := recall.NewInMemoryChainStore(nil)

	result := appendChainTools(base, store)

	names := toolNames(result)
	if len(names) != 3 {
		t.Fatalf("expected 3 tools, got %d: %v", len(names), names)
	}
	if names[0] != "existing_tool" {
		t.Errorf("expected existing_tool first, got %s", names[0])
	}
}
