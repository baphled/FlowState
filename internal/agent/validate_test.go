// validate_test.go pins the manifest-set validator that powers
// `flowstate agents validate`. The validator walks an fs.FS of agent
// manifests and applies category-and-capability rules so the next
// "manifest shipped with empty tools[]" regression surfaces at the
// CI gate rather than at runtime when an agent silently can't act
// (see ecbe59d3 / b17038c2 — the embedded engineering and
// documentation manifests previously shipped with no tools and went
// stuck because the engine's tool-gating was fail-closed). Under D1
// (Agent Runtime Quality plan, May 2026) the engine inherits the
// DefaultBaseTools floor; the validator rule persists because empty
// tools[] still signals missing implementation surfaces, but the
// detail text now reflects the post-D1 reality.
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

		It("accepts the bundle aliases file and delegate on a non-orchestration manifest", func() {
			// Behaviour-Pinned: the pre-commit-3 pin asserted that an
			// orchestration manifest declaring `[delegate, file]` was a
			// no-op. Commit 3 Gap A FLIPS that — `file` expands to
			// read+write, which is exactly what orchestrators must NOT
			// declare. The bundle-alias acceptance contract still holds,
			// it just moves to a non-orchestration category.
			fs := manifestFS(map[string]string{
				"agents/Bundle-User.md": "---\n" +
					"id: Bundle-User\n" +
					"name: Bundle User\n" +
					"orchestrator_meta:\n" +
					"  category: implementation\n" +
					"delegation:\n" +
					"  can_delegate: true\n" +
					"capabilities:\n" +
					"  tools: [delegate, file, bash, edit, grep, glob]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			Expect(violations).To(BeEmpty())
		})

		// Behaviour-Pinned: commit 3 Gap A — the original "accepts the
		// bundle aliases file and delegate" spec allowed `[delegate, file]`
		// on an orchestration manifest. The new upper-bound rule fires on
		// `file` because it expands to read + write at runtime, which
		// orchestrators must not hold. The spec above was rewritten to
		// move the bundle-alias contract onto an implementation manifest;
		// this companion spec pins the new orchestration rejection.
		It("rejects the file bundle alias on an orchestration manifest because file expands to read+write", func() {
			fs := manifestFS(map[string]string{
				"agents/Bundle-Cheat.md": "---\n" +
					"id: Bundle-Cheat\n" +
					"name: Bundle Cheat\n" +
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
			ruleNames := violationRules(violations)
			Expect(ruleNames).To(ContainElement("category-forbidden-tool"),
				"the file alias is an end-run around the upper-bound rule — it must be flagged so operators don't slip implementation surfaces onto orchestrators via the bundle alias")
			detail := concatDetails(violations, "category-forbidden-tool")
			Expect(detail).To(ContainSubstring("file"))
		})

		// D3 (Agent Runtime Quality plan, May 2026).
		It("accepts a canonical tool name listed in capabilities.tools_deny", func() {
			fs := manifestFS(map[string]string{
				"agents/Deny-User.md": "---\n" +
					"id: Deny-User\n" +
					"name: Deny User\n" +
					"orchestrator_meta:\n" +
					"  category: implementation\n" +
					"capabilities:\n" +
					"  tools: [bash, read, write, edit, grep, glob]\n" +
					"  tools_deny: [todowrite]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			Expect(violations).To(BeEmpty())
		})

		It("reports an unknown-tool violation when capabilities.tools_deny names a non-canonical tool", func() {
			fs := manifestFS(map[string]string{
				"agents/Typo-Deny.md": "---\n" +
					"id: Typo-Deny\n" +
					"name: Typo Deny\n" +
					"orchestrator_meta:\n" +
					"  category: implementation\n" +
					"capabilities:\n" +
					"  tools: [bash, read, write, edit, grep, glob]\n" +
					"  tools_deny: [bashh]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Manifest).To(Equal("Typo-Deny.md"))
			Expect(violations[0].Rule).To(Equal("tool-canonical"))
			Expect(violations[0].Detail).To(ContainSubstring("bashh"))
			Expect(violations[0].Detail).To(ContainSubstring("tool_deny"))
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

	// Commit 3 — Gap A: upper-bound role enforcement (orchestration /
	// coordination categories must NOT declare implementation surfaces).
	// The lower-bound rule above (orchestration requires `delegate`)
	// proves the agent CAN delegate; the upper-bound rule below proves
	// it doesn't ALSO hold bash / read / write / edit / grep / glob /
	// autoresearch_run / autoresearch_prune. Without the upper bound, a
	// coordinator could declare bash and silently shell out instead of
	// delegating, which is the failure mode commits f35162a9 + 92d52fdc
	// closed at the runtime + UI layer; this rule closes it at the CI
	// gate so the next regression surfaces before ship.
	Context("when an orchestration-category manifest declares a forbidden implementation tool", func() {
		It("reports a category-forbidden-tool violation naming the offending tool", func() {
			fs := manifestFS(map[string]string{
				"agents/Naughty-Coordinator.md": "---\n" +
					"id: Naughty-Coordinator\n" +
					"name: Naughty Coordinator\n" +
					"orchestrator_meta:\n" +
					"  category: orchestration\n" +
					"delegation:\n" +
					"  can_delegate: true\n" +
					"capabilities:\n" +
					"  tools: [delegate, bash, skill_load]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			Expect(violations).NotTo(BeEmpty())

			ruleNames := violationRules(violations)
			Expect(ruleNames).To(ContainElement("category-forbidden-tool"))
			detail := concatDetails(violations, "category-forbidden-tool")
			Expect(detail).To(ContainSubstring("bash"))
			Expect(detail).To(ContainSubstring("orchestration"))
		})

		It("reports the forbidden tool when the coordination category declares autoresearch_run", func() {
			fs := manifestFS(map[string]string{
				"agents/Greedy-Coordinator.md": "---\n" +
					"id: Greedy-Coordinator\n" +
					"name: Greedy Coordinator\n" +
					"orchestrator_meta:\n" +
					"  category: coordination\n" +
					"delegation:\n" +
					"  can_delegate: true\n" +
					"capabilities:\n" +
					"  tools: [delegate, autoresearch_run]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())

			ruleNames := violationRules(violations)
			Expect(ruleNames).To(ContainElement("category-forbidden-tool"))
			detail := concatDetails(violations, "category-forbidden-tool")
			Expect(detail).To(ContainSubstring("autoresearch_run"))
		})

		It("reports every forbidden tool listed (not just the first)", func() {
			fs := manifestFS(map[string]string{
				"agents/Tool-Hoarder.md": "---\n" +
					"id: Tool-Hoarder\n" +
					"name: Tool Hoarder\n" +
					"orchestrator_meta:\n" +
					"  category: orchestration\n" +
					"delegation:\n" +
					"  can_delegate: true\n" +
					"capabilities:\n" +
					"  tools: [delegate, bash, read, write, edit, grep, glob]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())

			detail := concatDetails(violations, "category-forbidden-tool")
			for _, forbidden := range []string{"bash", "read", "write", "edit", "grep", "glob"} {
				Expect(detail).To(ContainSubstring(forbidden),
					"every forbidden tool the manifest declares should appear in the detail so operators can fix all of them in one pass")
			}
		})
	})

	Context("when an orchestration-category manifest declares only delegate + permitted coordination tools", func() {
		It("returns no violations (the upper-bound rule is scoped, not blanket)", func() {
			fs := manifestFS(map[string]string{
				"agents/Clean-Coordinator.md": "---\n" +
					"id: Clean-Coordinator\n" +
					"name: Clean Coordinator\n" +
					"orchestrator_meta:\n" +
					"  category: orchestration\n" +
					"delegation:\n" +
					"  can_delegate: true\n" +
					"capabilities:\n" +
					"  tools: [delegate, coordination_store, skill_load, todowrite]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			Expect(violations).To(BeEmpty())
		})
	})

	Context("when a non-orchestration manifest declares bash and the other forbidden tools", func() {
		It("returns no category-forbidden-tool violation (the rule is scoped to orchestration / coordination)", func() {
			fs := manifestFS(map[string]string{
				"agents/Real-Engineer.md": "---\n" +
					"id: Real-Engineer\n" +
					"name: Real Engineer\n" +
					"orchestrator_meta:\n" +
					"  category: implementation\n" +
					"capabilities:\n" +
					"  tools: [bash, read, write, edit, grep, glob, autoresearch_run]\n" +
					"---\n",
			})

			violations, err := agent.ValidateManifestSet(fs, "agents")
			Expect(err).NotTo(HaveOccurred())
			ruleNames := violationRules(violations)
			Expect(ruleNames).NotTo(ContainElement("category-forbidden-tool"),
				"implementation agents need bash + filesystem surfaces; the rule must not fire on them")
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
