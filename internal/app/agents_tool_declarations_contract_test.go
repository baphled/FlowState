// Package app provides contract guards over bundled agent manifests
// under internal/app/agents/*.md. These tests pin the rule that any
// tool the prompt — or a runtime hook injecting into the prompt —
// instructs the agent to call must be declared in the manifest's
// capabilities.tools allowlist.
//
// Companion to agents_uses_recall_inventory_test.go: same seam, same
// stdlib-only approach (testing + io/fs + YAML frontmatter probe), no
// engine or provider dependencies.
package app

import (
	"io/fs"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAgentManifests_AlwaysActiveSkillsRequireSkillLoadTool asserts
// that every bundled manifest declaring a non-empty
// always_active_skills list also lists skill_load in
// capabilities.tools. The skill autoloader hook injects a
// "use skill_load(name)" instruction into the system prompt whenever
// always_active_skills is set; if the manifest does not declare
// skill_load, the engine's tool-allowlist filter strips it from the
// provider schema and the model emits tool-call-shaped JSON as plain
// content with nowhere legitimate to put it. Manifests that opt out
// of the allowlist (empty capabilities.tools) skip the check —
// engine.go:1315-1317 treats empty as permissive.
func TestAgentManifests_AlwaysActiveSkillsRequireSkillLoadTool(t *testing.T) {
	entries, err := fs.ReadDir(agentsFS, "agents")
	if err != nil {
		t.Fatalf("read agents dir: %v", err)
	}

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
			Capabilities struct {
				Tools              []string `yaml:"tools"`
				AlwaysActiveSkills []string `yaml:"always_active_skills"`
			} `yaml:"capabilities"`
		}
		if err := yaml.Unmarshal([]byte(frontmatter), &probe); err != nil {
			t.Fatalf("parse frontmatter %s: %v", path, err)
		}
		if len(probe.Capabilities.AlwaysActiveSkills) == 0 {
			continue
		}
		if len(probe.Capabilities.Tools) == 0 {
			continue
		}
		if !slices.Contains(probe.Capabilities.Tools, "skill_load") {
			t.Errorf("%s: declares always_active_skills=%v and capabilities.tools=%v but omits skill_load",
				e.Name(), probe.Capabilities.AlwaysActiveSkills, probe.Capabilities.Tools)
		}
	}
}

// TestExecutorManifest_DeclaresPlanDiscoveryTools pins the executor
// prompt's Discover Mode contract: the prompt instructs the agent to
// enumerate plan files in ~/.local/share/flowstate/plans/ and read
// their YAML frontmatter. plan_list and plan_read are the in-process
// implementations of that contract. Without them the model falls
// back to bash find against the CWD — the same regression that
// motivated the planner-side fix in commit 66bc3a1.
func TestExecutorManifest_DeclaresPlanDiscoveryTools(t *testing.T) {
	data, err := fs.ReadFile(agentsFS, "agents/executor.md")
	if err != nil {
		t.Fatalf("read executor.md: %v", err)
	}
	frontmatter, err := extractAgentFrontmatter(string(data))
	if err != nil {
		t.Fatalf("extract frontmatter: %v", err)
	}
	var probe struct {
		Capabilities struct {
			Tools []string `yaml:"tools"`
		} `yaml:"capabilities"`
	}
	if err := yaml.Unmarshal([]byte(frontmatter), &probe); err != nil {
		t.Fatalf("parse frontmatter: %v", err)
	}
	for _, required := range []string{"plan_list", "plan_read"} {
		if !slices.Contains(probe.Capabilities.Tools, required) {
			t.Errorf("executor.md: capabilities.tools=%v but does not include %q",
				probe.Capabilities.Tools, required)
		}
	}
}
