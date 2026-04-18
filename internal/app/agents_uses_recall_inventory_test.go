// Package app provides the P13 inventory guard: every bundled agent manifest
// under internal/app/agents/*.md must declare uses_recall explicitly. The
// guard prevents silent defaults — P13 flips the recall default to off, so
// any new agent that should opt in must set the flag deliberately.
package app

import (
	"io/fs"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAgentManifests_DeclareUsesRecallExplicitly walks the embedded agent
// filesystem and asserts each manifest sets uses_recall either true or
// false. Missing entries fail the test so the author has to make a
// conscious choice rather than inheriting the (opt-out) default.
//
// It also records, for the benefit of the P13 rollout, which agents opt
// in — the list is logged so reviewers see the recall perimeter at a
// glance when this test runs under -v.
func TestAgentManifests_DeclareUsesRecallExplicitly(t *testing.T) {
	entries, err := fs.ReadDir(agentsFS, "agents")
	if err != nil {
		t.Fatalf("read agents dir: %v", err)
	}

	optedIn := make([]string, 0)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := "agents/" + e.Name()
		data, err := fs.ReadFile(agentsFS, path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		frontmatter, err := extractAgentFrontmatter(string(data))
		if err != nil {
			t.Fatalf("extract frontmatter from %s: %v", path, err)
		}
		var probe struct {
			ID         string `yaml:"id"`
			UsesRecall *bool  `yaml:"uses_recall"`
		}
		if err := yaml.Unmarshal([]byte(frontmatter), &probe); err != nil {
			t.Fatalf("parse frontmatter %s: %v", path, err)
		}
		if probe.UsesRecall == nil {
			t.Errorf("%s: uses_recall is not set — add uses_recall: true|false to the frontmatter (P13)", path)
			continue
		}
		if *probe.UsesRecall {
			optedIn = append(optedIn, probe.ID)
		}
	}

	t.Logf("P13: %d agents opted in to RecallBroker: %v", len(optedIn), optedIn)
}

// extractAgentFrontmatter returns the YAML block between leading "---\n"
// delimiters. The helper is colocated with the inventory test so it
// stays independent of internal/agent/loader.go which applies defaults
// and validation we do not want interfering with the audit.
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
