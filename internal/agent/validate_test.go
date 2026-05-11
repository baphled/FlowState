// validate_test.go pins the manifest-set validator that powers
// `flowstate agents validate`. The validator walks an fs.FS of agent
// manifests and applies category-and-capability rules so the next
// "manifest shipped with empty tools[]" regression surfaces at the
// CI gate rather than at runtime when an agent silently can't act
// (see ecbe59d3 / b17038c2 — the embedded engineering and
// documentation manifests previously shipped with no tools and went
// stuck because the engine's tool-gating is fail-closed).
//
// Validator contract (held by these specs):
//   - Pure function over an fs.FS rooted at agent-manifest directory.
//   - Returns a typed []Violation slice describing every failure,
//     never panics, never writes anywhere.
//   - Empty slice means "all manifests pass the rules in this
//     validator's table".
package agent_test

import (
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

// manifestFS is a tiny helper for building synthetic manifest FS
// trees inside the spec. The rules under test only care about the
// frontmatter; bodies can stay empty.
func manifestFS(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, body := range files {
		out[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return out
}

var _ = Describe("ValidateManifestSet", func() {
	Context("when every manifest in the set is well-formed", func() {
		It("returns an empty violation slice", func() {
			fs := manifestFS(map[string]string{
				"agents/Good-Engineer.md": "---\n" +
					"id: Good-Engineer\n" +
					"name: Good Engineer\n" +
					"orchestrator_meta:\n" +
					"  category: implementation\n" +
					"capabilities:\n" +
					"  tools: [bash, read, write, edit, grep, glob, skill_load]\n" +
					"---\nbody\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			Expect(violations).To(BeEmpty())
		})
	})

	Context("when a manifest declares a tool name that is not in the canonical set", func() {
		It("reports an unknown-tool violation naming the bogus token", func() {
			fs := manifestFS(map[string]string{
				"agents/Typo-Engineer.md": "---\n" +
					"id: Typo-Engineer\n" +
					"name: Typo Engineer\n" +
					"orchestrator_meta:\n" +
					"  category: implementation\n" +
					"capabilities:\n" +
					"  tools: [bash, read, write, edit, grep, glob, bsah]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Manifest).To(Equal("Typo-Engineer.md"))
			Expect(violations[0].Rule).To(Equal("tool-canonical"))
			Expect(violations[0].Detail).To(ContainSubstring("bsah"))
		})

		It("accepts any mcp_*-prefixed tool name as canonical (runtime-discovered)", func() {
			fs := manifestFS(map[string]string{
				"agents/MCP-Consumer.md": "---\n" +
					"id: MCP-Consumer\n" +
					"name: MCP Consumer\n" +
					"orchestrator_meta:\n" +
					"  category: implementation\n" +
					"capabilities:\n" +
					"  tools: [bash, read, write, edit, grep, glob, mcp_memory_search_nodes, mcp_vault-rag_query_vault]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			Expect(violations).To(BeEmpty())
		})

		It("accepts the bundle aliases file and delegate", func() {
			fs := manifestFS(map[string]string{
				"agents/Bundle-User.md": "---\n" +
					"id: Bundle-User\n" +
					"name: Bundle User\n" +
					"orchestrator_meta:\n" +
					"  category: orchestration\n" +
					"delegation:\n" +
					"  can_delegate: true\n" +
					"capabilities:\n" +
					"  tools: [delegate, file]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			Expect(violations).To(BeEmpty())
		})
	})

	Context("when a manifest claims delegation but omits the delegate tool", func() {
		It("reports a can-delegate-requires-delegate-tool violation", func() {
			fs := manifestFS(map[string]string{
				"agents/Stuck-Coordinator.md": "---\n" +
					"id: Stuck-Coordinator\n" +
					"name: Stuck Coordinator\n" +
					"orchestrator_meta:\n" +
					"  category: orchestration\n" +
					"delegation:\n" +
					"  can_delegate: true\n" +
					"capabilities:\n" +
					"  tools: [bash, read]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			Expect(violations).NotTo(BeEmpty())
			ruleNames := violationRules(violations)
			Expect(ruleNames).To(ContainElement("delegate-tool-required"))
		})
	})

	Context("when a manifest in the implementation category omits a required tool", func() {
		It("reports a category-required-tool violation naming the missing tool(s)", func() {
			fs := manifestFS(map[string]string{
				"agents/Half-Engineer.md": "---\n" +
					"id: Half-Engineer\n" +
					"name: Half Engineer\n" +
					"orchestrator_meta:\n" +
					"  category: implementation\n" +
					"capabilities:\n" +
					"  tools: [read, grep, glob]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			Expect(violations).NotTo(BeEmpty())

			ruleNames := violationRules(violations)
			Expect(ruleNames).To(ContainElement("category-required-tool"))

			detail := concatDetails(violations, "category-required-tool")
			Expect(detail).To(ContainSubstring("bash"))
			Expect(detail).To(ContainSubstring("write"))
			Expect(detail).To(ContainSubstring("edit"))
		})
	})

	Context("when the role prose claims write capability but the tools omit write/edit", func() {
		It("reports a role-mentions-write-but-no-write-tool violation", func() {
			fs := manifestFS(map[string]string{
				"agents/Read-Only-Writer.md": "---\n" +
					"id: Read-Only-Writer\n" +
					"name: Read Only Writer\n" +
					"metadata:\n" +
					"  role: \"Writes documentation and curates the knowledge base\"\n" +
					"orchestrator_meta:\n" +
					"  category: documentation\n" +
					"capabilities:\n" +
					"  tools: [bash, read, grep, glob]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			Expect(violations).NotTo(BeEmpty())

			ruleNames := violationRules(violations)
			Expect(ruleNames).To(ContainElement("role-write-capability-mismatch"))
		})

		It("accepts the manifest when at least one of write or edit is declared", func() {
			fs := manifestFS(map[string]string{
				"agents/Edit-Only-Writer.md": "---\n" +
					"id: Edit-Only-Writer\n" +
					"name: Edit Only Writer\n" +
					"metadata:\n" +
					"  role: \"Writes API documentation\"\n" +
					"orchestrator_meta:\n" +
					"  category: documentation\n" +
					"capabilities:\n" +
					"  tools: [bash, read, edit, grep, glob]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			// Should pass the role-write check; may still flag category-required-tool
			// for missing "write", but the role check itself must not fire.
			ruleNames := violationRules(violations)
			Expect(ruleNames).NotTo(ContainElement("role-write-capability-mismatch"))
		})
	})

	Context("when a manifest declares no tools at all (empty list)", func() {
		It("reports a tools-empty violation independently of category rules", func() {
			fs := manifestFS(map[string]string{
				"agents/Tools-Empty.md": "---\n" +
					"id: Tools-Empty\n" +
					"name: Tools Empty\n" +
					"orchestrator_meta:\n" +
					"  category: implementation\n" +
					"capabilities:\n" +
					"  tools: []\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())

			ruleNames := violationRules(violations)
			Expect(ruleNames).To(ContainElement("tools-empty"))
		})
	})

	Context("when the directory contains non-manifest files", func() {
		It("skips non-.md entries silently", func() {
			fs := manifestFS(map[string]string{
				"agents/README.txt":   "not a manifest",
				"agents/notes.go":     "not a manifest",
				"agents/Good.md": "---\n" +
					"id: Good\n" +
					"name: Good\n" +
					"orchestrator_meta:\n" +
					"  category: implementation\n" +
					"capabilities:\n" +
					"  tools: [bash, read, write, edit, grep, glob]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			Expect(violations).To(BeEmpty())
		})
	})

	Context("when the directory does not exist", func() {
		It("returns an error rather than panicking", func() {
			fs := manifestFS(map[string]string{})
			_, err := agent.ValidateManifestSet(fs, "no-such-dir")
			Expect(err).To(HaveOccurred())
		})
	})
})

// violationRules returns the Rule field of each violation in order;
// used by specs that only care about the rule taxonomy, not the
// per-violation detail.
func violationRules(violations []agent.Violation) []string {
	out := make([]string, 0, len(violations))
	for _, v := range violations {
		out = append(out, v.Rule)
	}
	return out
}

// concatDetails joins the Detail field of every violation whose Rule
// matches the supplied rule name. Lets a spec assert "the detail of
// the category-required-tool violation mentions bash, write, and edit"
// without binding to violation ordering.
func concatDetails(violations []agent.Violation, rule string) string {
	out := ""
	for _, v := range violations {
		if v.Rule == rule {
			out += v.Detail + "\n"
		}
	}
	return out
}
