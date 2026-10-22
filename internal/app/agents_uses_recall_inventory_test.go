// agents_uses_recall_inventory_test.go provides the P13 inventory
// guard: every bundled agent manifest under internal/app/agents/*.md
// must declare uses_recall explicitly. The guard prevents silent
// defaults — P13 flips the recall default to off, so any new agent
// that should opt in must set the flag deliberately.
package app_test

import (
	"io/fs"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/app"
)

// AgentManifests_DeclareUsesRecallExplicitly walks the embedded agent
// filesystem and asserts each manifest sets uses_recall either true
// or false. Missing entries fail the test so the author has to make
// a conscious choice rather than inheriting the (opt-out) default.
//
// The agents that opt in are recorded as a Ginkgo report entry so
// reviewers see the recall perimeter at a glance when the suite
// runs under -v.
var _ = Describe("AgentManifests_DeclareUsesRecallExplicitly", func() {
	It("requires every bundled manifest to set uses_recall true|false (P13)", func() {
		manifests := app.EmbeddedAgentsFS()
		entries, err := fs.ReadDir(manifests, "agents")
		Expect(err).NotTo(HaveOccurred(), "read agents dir")

		optedIn := make([]string, 0)
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
				ID         string `yaml:"id"`
				UsesRecall *bool  `yaml:"uses_recall"`
			}
			Expect(yaml.Unmarshal([]byte(frontmatter), &probe)).To(Succeed(), "parse frontmatter %s", path)

			Expect(probe.UsesRecall).NotTo(BeNil(),
				"%s: uses_recall is not set — add uses_recall: true|false to the frontmatter (P13)", path)

			if *probe.UsesRecall {
				optedIn = append(optedIn, probe.ID)
			}
		}

		AddReportEntry("P13_recall_perimeter", optedIn)
	})
})

// extractAgentFrontmatter returns the YAML block between leading
// "---\n" delimiters. The helper is colocated with this inventory
// test so it stays independent of internal/agent/loader.go which
// applies defaults and validation we do not want interfering with
// the audit. Shared with agents_tool_declarations_contract_test.go
// (same package).
func extractAgentFrontmatter(content string) (string, error) {
	if !strings.HasPrefix(content, "---") {
		return "", nil
	}
	parts := strings.SplitN(content[3:], "---", 2)
	if len(parts) < 2 {
		return "", &inventoryError{msg: "invalid frontmatter: missing closing ---"}
	}
	return strings.TrimSpace(parts[0]), nil
}

type inventoryError struct{ msg string }

func (e *inventoryError) Error() string { return e.msg }
