package provider_test

import (
	"testing"

	"github.com/baphled/flowstate/internal/provider"
)

// Phase 14 — every tool-related chunk carries an InternalToolCallID field.
//
// The field is populated by the engine (via a streaming.ToolCallCorrelator)
// on its way to consumers. Providers leave it empty. The type's existence
// is what P14 contracts: the downstream consumer compiles against it.
func TestStreamChunk_HasInternalToolCallIDField(t *testing.T) {
	ch := provider.StreamChunk{
		ToolCallID:         "toolu_01abc",
		InternalToolCallID: "fs_abcdef",
	}
	if ch.InternalToolCallID != "fs_abcdef" {
		t.Fatalf("InternalToolCallID must be settable and read back verbatim; got %q", ch.InternalToolCallID)
	}
	if ch.ToolCallID != "toolu_01abc" {
		t.Fatalf("ToolCallID must coexist with InternalToolCallID (audit trail); got %q", ch.ToolCallID)
	}
}
