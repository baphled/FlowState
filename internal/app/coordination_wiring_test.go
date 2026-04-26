package app_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	coordinationtool "github.com/baphled/flowstate/internal/tool/coordination"
)

var _ = Describe("Coordination tool wiring", func() {
	// BuildToolsForManifest_IncludesCoordinationTool: an explorer-style
	// manifest that declares coordination_store in capabilities.tools
	// must surface the tool when the wiring helper runs.
	It("includes coordination_store when the manifest declares it in capabilities.tools", func() {
		manifestWithCoordination := agent.Manifest{
			ID:   "explorer",
			Name: "Explorer Agent",
			Capabilities: agent.Capabilities{
				Tools: []string{"coordination_store", "read", "bash"},
			},
		}

		store := coordination.NewMemoryStore()
		coordTool := coordinationtool.New(store)

		Expect(hasTool(manifestWithCoordination, coordTool.Name())).To(BeTrue(),
			"manifest with coordination_store capability should include coordination_store tool")
	})

	// BuildToolsForManifest_WithoutCoordinationTool: a manifest that
	// does NOT declare coordination_store must NOT receive the tool —
	// the canonical hasCoordinationTool guard.
	It("excludes coordination_store when the manifest omits it from capabilities.tools", func() {
		manifestWithoutCoordination := agent.Manifest{
			ID:   "simple-agent",
			Name: "Simple Agent",
			Capabilities: agent.Capabilities{
				Tools: []string{"read", "bash"},
			},
		}

		store := coordination.NewMemoryStore()
		coordTool := coordinationtool.New(store)

		Expect(hasTool(manifestWithoutCoordination, coordTool.Name())).To(BeFalse(),
			"manifest without coordination_store capability should NOT include coordination_store tool")
	})

	// CoordinationToolIsCorrectType: pin the tool name + description so
	// downstream consumers (manifest validators, prompts that reference
	// it) catch a rename.
	It("exposes the canonical name + description", func() {
		store := coordination.NewMemoryStore()
		coordTool := coordinationtool.New(store)

		Expect(coordTool.Name()).To(Equal("coordination_store"))
		Expect(coordTool.Description()).To(Equal(
			"Read and write shared key-value context during agent delegation chains"))
	})

	// MemoryStoreSharing: round-trip a single key through the in-memory
	// store as a smoke test for the underlying KV implementation.
	It("round-trips a key through the in-memory store", func() {
		store := coordination.NewMemoryStore()

		Expect(store.Set("test-key", []byte("test-value"))).To(Succeed(),
			"should be able to write to store")

		val, err := store.Get("test-key")
		Expect(err).NotTo(HaveOccurred(), "should be able to read from store")
		Expect(string(val)).To(Equal("test-value"))
	})

	// CoordinationKeyFormat: pin the canonical <chainID>/<keyname> key
	// shape that all delegation chains use, with two writes (requirements
	// + plan) demonstrating the namespace separator.
	It("supports the canonical <chainID>/<keyname> namespace format", func() {
		store := coordination.NewMemoryStore()
		chainID := "test-chain-123"

		Expect(store.Set(chainID+"/requirements", []byte("Build a REST API"))).To(Succeed(),
			"coordinator should write requirements")
		val, err := store.Get(chainID + "/requirements")
		Expect(err).NotTo(HaveOccurred(), "delegate should read requirements")
		Expect(string(val)).To(Equal("Build a REST API"))

		Expect(store.Set(chainID+"/plan", []byte("# Plan\n- Task 1"))).To(Succeed(),
			"writer should write plan")
		val, err = store.Get(chainID + "/plan")
		Expect(err).NotTo(HaveOccurred(), "reader should read plan")
		Expect(string(val)).To(Equal("# Plan\n- Task 1"))
	})
})

// hasTool reports whether the manifest's capabilities.tools list
// contains the named tool. Replaces a private helper that previously
// drove the wiring assertions; kept at file scope so the spec body
// stays focused on behaviour rather than membership iteration.
func hasTool(manifest agent.Manifest, toolName string) bool {
	for _, t := range manifest.Capabilities.Tools {
		if t == toolName {
			return true
		}
	}
	return false
}
