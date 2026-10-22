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

	It("appends the explicit coordination-file prohibition for manifests carrying coordination_store", func() {
		out := buildToolUsageRequirement(manifestWith)

		Expect(out).To(ContainSubstring("MUST ONLY be written through the `coordination_store` tool"))
		Expect(out).To(ContainSubstring("sanctioned CLI verbs"))
		Expect(out).To(ContainSubstring("NEVER read"))
		Expect(out).To(ContainSubstring("coordination.json"))
		Expect(out).To(ContainSubstring("~/.local/share/flowstate/coordination.json"))
		Expect(out).To(ContainSubstring("no per-key files exist"))
		Expect(out).To(ContainSubstring("silently overwritten on its next persist"))
		Expect(out).To(ContainSubstring("hard prohibition with no exceptions"))
	})

	It("omits the section for manifests without coordination_store", func() {
		Expect(buildToolUsageRequirement(manifestWithout)).To(BeEmpty())
	})
})

// These specs pin the unconditional coordination-store guard: every
// assembled system prompt carries the blanket prohibition against
// touching the coordination.json backing file, regardless of whether
// the manifest exposes the coordination_store tool.
var _ = Describe("assembleSystemPromptLocked coordination guard", func() {
	manifestWithTool := agent.Manifest{
		ID:   "guard-probe-with-tool",
		Name: "Guard Probe With Tool",
		Instructions: agent.Instructions{
			SystemPrompt: "You are a guard probe agent.",
		},
		Capabilities: agent.Capabilities{Tools: []string{"read", "write", "coordination_store"}},
	}
	manifestWithoutTool := agent.Manifest{
		ID:   "guard-probe-without-tool",
		Name: "Guard Probe Without Tool",
		Instructions: agent.Instructions{
			SystemPrompt: "You are a guard probe agent.",
		},
		Capabilities: agent.Capabilities{Tools: []string{"read", "write"}},
	}

	It("appends the blanket guard line to every prompt", func() {
		for _, manifest := range []agent.Manifest{manifestWithTool, manifestWithoutTool} {
			eng := New(Config{Manifest: manifest})

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring(
				"NEVER read or write `coordination.json` (the coordination-store backing file) via file tools or shell commands; coordination data moves only through sanctioned tools and CLI verbs.",
			))
		}
	})

	It("omits the Tool-Usage Requirement when coordination_store is absent", func() {
		eng := New(Config{Manifest: manifestWithoutTool})

		prompt := eng.BuildSystemPrompt()

		Expect(prompt).NotTo(ContainSubstring("Tool-Usage Requirement"))
		Expect(prompt).To(ContainSubstring("NEVER read or write `coordination.json`"))
	})
})
