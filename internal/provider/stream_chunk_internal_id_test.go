package provider_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
)

// StreamChunk_HasInternalToolCallIDField — Phase 14 — every tool-related
// chunk carries an InternalToolCallID field.
//
// The field is populated by the engine (via a streaming.ToolCallCorrelator)
// on its way to consumers. Providers leave it empty. The type's existence
// is what P14 contracts: the downstream consumer compiles against it.
var _ = Describe("StreamChunk.InternalToolCallID", func() {
	It("is settable, readable, and coexists with the upstream ToolCallID (P14)", func() {
		ch := provider.StreamChunk{
			ToolCallID:         "toolu_01abc",
			InternalToolCallID: "fs_abcdef",
		}
		Expect(ch.InternalToolCallID).To(Equal("fs_abcdef"),
			"InternalToolCallID must be settable and read back verbatim")
		Expect(ch.ToolCallID).To(Equal("toolu_01abc"),
			"ToolCallID must coexist with InternalToolCallID (audit trail)")
	})
})
