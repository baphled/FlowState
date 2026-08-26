package engine

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

// These specs pin the Tool-Usage Requirement contract: manifests carrying
// coordination_store must be told the tool is an inter-agent handoff
// channel whose write mandate is scoped to swarm membership, that it is
// never the destination for user-facing output, and that write requests
// mean the write tool (files). Manifests without the tool receive no
// section at all.
var _ = Describe("buildToolUsageRequirement", func() {
	manifestWith := agent.Manifest{Capabilities: agent.Capabilities{Tools: []string{"read", "coordination_store"}}}
	manifestWithout := agent.Manifest{Capabilities: agent.Capabilities{Tools: []string{"read", "write"}}}

	It("emits the handoff-scoped requirement for manifests carrying coordination_store", func() {
		out := buildToolUsageRequirement(manifestWith)

		Expect(out).To(ContainSubstring("Tool-Usage Requirement"))
		Expect(out).To(ContainSubstring("inter-agent handoff channel"))
		Expect(out).To(ContainSubstring("you MUST write your contracted output key"))
		Expect(out).To(ContainSubstring("Outside a delegation chain, do not use `coordination_store`"))
	})

	It("states that writing means writing to a file", func() {
		out := buildToolUsageRequirement(manifestWith)

		Expect(out).To(ContainSubstring("use the `write` tool"))
		Expect(out).To(ContainSubstring("writing means"))
		Expect(out).To(ContainSubstring("writing to a file"))
	})

	It("omits the section for manifests without coordination_store", func() {
		Expect(buildToolUsageRequirement(manifestWithout)).To(BeEmpty())
	})
})
