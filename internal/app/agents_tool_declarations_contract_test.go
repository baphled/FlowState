// agents_tool_declarations_contract_test.go provides contract guards
// over bundled agent manifests under internal/app/agents/*.md. These
// tests pin the rule that any tool the prompt — or a runtime hook
// injecting into the prompt — instructs the agent to call must be
// declared in the manifest's capabilities.tools allowlist.
//
// Companion to agents_uses_recall_inventory_test.go (same package);
// shares extractAgentFrontmatter via that file.
package app_test

import (
	"io/fs"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/app"
)

var _ = Describe("AgentManifest tool-declaration contracts", func() {
	// AlwaysActiveSkillsRequireSkillLoadTool: every bundled manifest
	// declaring a non-empty always_active_skills list AND a non-empty
	// capabilities.tools allowlist must also include skill_load. The
	// skill autoloader hook injects "use skill_load(name)" into the
	// system prompt whenever always_active_skills is set; if the
	// manifest does not declare skill_load, the engine's tool-allowlist
	// filter strips it from the provider schema and the model emits
	// tool-call-shaped JSON as plain content with nowhere to put it.
	// Manifests that opt out of the allowlist (empty
	// capabilities.tools) skip the check — engine.go:1315-1317 treats
	// empty as permissive.
	It("requires skill_load on every manifest with always_active_skills + a non-empty capabilities.tools allowlist", func() {
		manifests := app.EmbeddedAgentsFS()
		entries, err := fs.ReadDir(manifests, "agents")
		Expect(err).NotTo(HaveOccurred(), "read agents dir")

		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := "agents/" + e.Name()
			data, err := fs.ReadFile(manifests, path)
			Expect(err).NotTo(HaveOccurred(), "read %s", path)

			frontmatter, err := extractAgentFrontmatter(string(data))
			Expect(err).NotTo(HaveOccurred(), "extract frontmatter from %s", path)

			var probe struct {
				Capabilities struct {
					Tools              []string `yaml:"tools"`
					AlwaysActiveSkills []string `yaml:"always_active_skills"`
				} `yaml:"capabilities"`
			}
			Expect(yaml.Unmarshal([]byte(frontmatter), &probe)).To(Succeed(), "parse frontmatter %s", path)

			if len(probe.Capabilities.AlwaysActiveSkills) == 0 {
				continue
			}
			if len(probe.Capabilities.Tools) == 0 {
				continue
			}
			Expect(probe.Capabilities.Tools).To(ContainElement("skill_load"),
				"%s: declares always_active_skills=%v and capabilities.tools=%v but omits skill_load",
				e.Name(), probe.Capabilities.AlwaysActiveSkills, probe.Capabilities.Tools)
		}
	})

	// ExecutorManifest_DeclaresPlanDiscoveryTools pins the executor
	// prompt's Discover Mode contract: the prompt instructs the agent
	// to enumerate plan files in ~/.local/share/flowstate/plans/ and
	// read their YAML frontmatter. plan_list and plan_read are the
	// in-process implementations of that contract. Without them the
	// model falls back to bash find against the CWD — the same
	// regression motivating commit 66bc3a1 on the planner side.
	It("requires executor.md to declare plan_list and plan_read", func() {
		manifests := app.EmbeddedAgentsFS()
		data, err := fs.ReadFile(manifests, "agents/executor.md")
		Expect(err).NotTo(HaveOccurred(), "read executor.md")

		frontmatter, err := extractAgentFrontmatter(string(data))
		Expect(err).NotTo(HaveOccurred(), "extract frontmatter")

		var probe struct {
			Capabilities struct {
				Tools []string `yaml:"tools"`
			} `yaml:"capabilities"`
		}
		Expect(yaml.Unmarshal([]byte(frontmatter), &probe)).To(Succeed(), "parse frontmatter")

		for _, required := range []string{"plan_list", "plan_read"} {
			Expect(slices.Contains(probe.Capabilities.Tools, required)).To(BeTrue(),
				"executor.md: capabilities.tools=%v but does not include %q",
				probe.Capabilities.Tools, required)
		}
	})
})
