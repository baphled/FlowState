package agent

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Violation describes one rule failure surfaced by ValidateManifestSet.
// The triple (Manifest, Rule, Detail) is stable so callers (CLI table
// rendering, CI reports) can pivot on it without re-parsing the
// detail string.
type Violation struct {
	// Manifest is the base name of the offending file (e.g.
	// "Senior-Engineer.md"). Bare name rather than full path so the
	// validator stays portable across embedded fs.FS and on-disk
	// directories.
	Manifest string
	// Rule is the stable identifier of the broken rule. Callers grep
	// CI output for this — keep the set small and the names
	// hyphenated. Current taxonomy:
	//   - tool-canonical
	//   - tools-empty
	//   - delegate-tool-required
	//   - role-write-capability-mismatch
	//   - category-required-tool
	Rule string
	// Detail is a one-line human-readable elaboration. Free-form;
	// CLI renders it after the manifest + rule columns.
	Detail string
}

// ValidateManifestSet walks every .md manifest in dir under root and
// applies the rule table below. Returns the violation slice plus an
// error only when the directory itself cannot be enumerated.
//
// Expected:
//   - root is a non-nil fs.FS. The most common producers are
//     app.EmbeddedAgentsFS() for the bundled set and os.DirFS for
//     on-disk validation.
//   - dir is the directory inside root that holds the manifests
//     (typically "agents").
//
// Returns:
//   - A possibly-empty Violation slice ordered by (manifest, rule).
//   - A non-nil error only when root/dir cannot be enumerated.
//
// Side effects:
//   - Reads files from root.
func ValidateManifestSet(root fs.FS, dir string) ([]Violation, error) {
	entries, err := fs.ReadDir(root, dir)
	if err != nil {
		return nil, fmt.Errorf("reading agents directory %q: %w", dir, err)
	}

	var violations []Violation
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		// fs.ReadFile contract requires unrooted, forward-slash paths
		// per io/fs.ValidPath: callers cannot pass "./X" or "/X".
		// When dir == "." we read the entry name directly; otherwise
		// we join with a single forward slash.
		var path string
		if dir == "" || dir == "." {
			path = e.Name()
		} else {
			path = dir + "/" + e.Name()
		}
		data, readErr := fs.ReadFile(root, path)
		if readErr != nil {
			return nil, fmt.Errorf("reading manifest %q: %w", path, readErr)
		}
		violations = append(violations, validateOneManifest(e.Name(), data)...)
	}

	sort.SliceStable(violations, func(i, j int) bool {
		if violations[i].Manifest != violations[j].Manifest {
			return violations[i].Manifest < violations[j].Manifest
		}
		return violations[i].Rule < violations[j].Rule
	})
	return violations, nil
}

// validateOneManifest parses a manifest's YAML frontmatter and applies
// every rule against it. The function is exported only via
// ValidateManifestSet to keep the per-file parse logic out of the
// caller's hands; tests cover behaviour at the set level.
func validateOneManifest(name string, data []byte) []Violation {
	frontmatter, parseErr := extractFrontmatterOrEmpty(string(data))
	if parseErr != nil {
		return []Violation{{Manifest: name, Rule: "frontmatter-parse", Detail: parseErr.Error()}}
	}

	var probe validatorManifestProbe
	if err := yaml.Unmarshal([]byte(frontmatter), &probe); err != nil {
		return []Violation{{Manifest: name, Rule: "frontmatter-parse", Detail: err.Error()}}
	}

	var out []Violation
	out = append(out, ruleToolCanonical(name, probe)...)
	out = append(out, ruleToolsEmpty(name, probe)...)
	out = append(out, ruleDelegateToolRequired(name, probe)...)
	out = append(out, ruleRoleWriteCapability(name, probe)...)
	out = append(out, ruleCategoryRequired(name, probe)...)
	return out
}

// validatorManifestProbe is the narrow YAML shape the validator
// touches. Decoupled from agent.Manifest because the validator
// deliberately reads raw frontmatter — applyDefaults and the loader's
// embedding-model normalisation must not run, or the rule "category
// X requires tool Y" loses sight of operator intent.
type validatorManifestProbe struct {
	Metadata struct {
		Role string `yaml:"role"`
	} `yaml:"metadata"`
	Capabilities struct {
		Tools []string `yaml:"tools"`
	} `yaml:"capabilities"`
	Delegation struct {
		CanDelegate bool `yaml:"can_delegate"`
	} `yaml:"delegation"`
	OrchestratorMeta struct {
		Category string `yaml:"category"`
	} `yaml:"orchestrator_meta"`
}

// canonicalTools is the static set of tool names the validator
// accepts without further question. It mirrors the names hardcoded
// in internal/engine/engine.go:buildAllowedToolSetFor and the tool
// packages registered under internal/tool/*. Two pseudo-tools live
// here too:
//
//   - "file"      — engine bundle alias expanding to read+write.
//   - "delegate"  — engine bundle alias expanding to delegate +
//     background_output + background_cancel + autoresearch_run.
//
// MCP tools are accepted via the mcp_* prefix rather than being
// enumerated — the MCP set is discovered at runtime and any static
// list would drift the moment an operator wires a new server.
var canonicalTools = map[string]bool{
	// Engine bundle aliases (see buildAllowedToolSetFor).
	"file":     true,
	"delegate": true,
	// Always-on escape hatch (P12).
	"suggest_delegate": true,

	// Core filesystem + shell tools.
	"bash":      true,
	"read":      true,
	"write":     true,
	"edit":      true,
	"multiedit": true,
	"glob":      true,
	"grep":      true,
	"ls":        true,
	"lsp":       true,

	// Coordination + delegation infrastructure.
	"coordination_store":  true,
	"background_output":   true,
	"background_cancel":   true,
	"autoresearch_run":    true,
	"todowrite":           true,

	// Knowledge / skill / plan tools.
	"skill_load":     true,
	"plan_list":      true,
	"plan_read":      true,
	"plan_write":     true,
	"plan_enter":     true,
	"plan_exit":      true,

	// Memory MCP-shaped natives (registered directly in toolset.AppendMemoryTools).
	"search_nodes":  true,
	"open_nodes":    true,
	"chain_search":  true,
	"chain_get_messages": true,

	// Swarm and vault tools.
	"swarm_list":     true,
	"swarm_info":     true,
	"swarm_validate": true,
	"vault_index":    true,
	"vault_sync":     true,

	// Other registered tools.
	"web":          true,
	"websearch":    true,
	"question":     true,
	"apply_patch":  true,
	"batch":        true,
	"display":      true,
	"truncate":     true,
	"invalid":      true,
}

// roleWritePattern matches role prose that promises write capability.
// Used by ruleRoleWriteCapability to catch the "writes documentation"
// agent that ships with read-only tools.
var roleWritePattern = regexp.MustCompile(`(?i)\b(writes?|edits?|curates?)\b`)

// implementationRequired, documentationRequired, qualityRequired,
// infrastructureRequired, orchestrationRequired are the
// category→tools tables enforced by ruleCategoryRequired.
//
// The values were chosen from the actual shipped manifests rather than
// invented up-front: every implementation/documentation manifest
// already declares the bash/read/write/edit/grep/glob set, and the
// validator pins that consensus so the next "shipped with empty
// tools[]" regression surfaces at the CI gate.
var (
	implementationRequired = []string{"bash", "read", "write", "edit", "grep", "glob"}
	documentationRequired  = []string{"bash", "read", "write", "edit", "grep", "glob"}
	qualityRequired        = []string{"bash", "read", "grep", "glob"}
	infrastructureRequired = []string{"bash", "read", "grep", "glob"}
	orchestrationRequired  = []string{"delegate"}
)

// ruleToolCanonical fires on any tool name not in canonicalTools and
// not prefixed mcp_. One violation per offending name keeps the
// detail strings short and the CI output greppable.
func ruleToolCanonical(name string, probe validatorManifestProbe) []Violation {
	var out []Violation
	for _, t := range probe.Capabilities.Tools {
		if canonicalTools[t] || strings.HasPrefix(t, "mcp_") {
			continue
		}
		out = append(out, Violation{
			Manifest: name,
			Rule:     "tool-canonical",
			Detail:   fmt.Sprintf("unknown tool name %q (not in canonical set, not mcp_*-prefixed)", t),
		})
	}
	return out
}

// ruleToolsEmpty fires on an explicit empty list AND on a missing
// frontmatter key. The engine's tool-gating is fail-closed (empty
// Tools → no tools allowed beyond suggest_delegate) so this is the
// load-bearing rule for the "manifest ships stuck" regression.
func ruleToolsEmpty(name string, probe validatorManifestProbe) []Violation {
	if len(probe.Capabilities.Tools) > 0 {
		return nil
	}
	return []Violation{{
		Manifest: name,
		Rule:     "tools-empty",
		Detail:   "capabilities.tools is empty — engine fail-closed gating leaves the agent stuck (see ecbe59d3 / b17038c2)",
	}}
}

// ruleDelegateToolRequired fires when delegation.can_delegate is true
// but the tools allowlist omits delegate. Without delegate the agent
// cannot call its only documented onward-routing tool, so the
// "coordinator" prose contradicts the capability wiring.
func ruleDelegateToolRequired(name string, probe validatorManifestProbe) []Violation {
	if !probe.Delegation.CanDelegate {
		return nil
	}
	for _, t := range probe.Capabilities.Tools {
		if t == "delegate" {
			return nil
		}
	}
	return []Violation{{
		Manifest: name,
		Rule:     "delegate-tool-required",
		Detail:   "delegation.can_delegate=true but tools[] omits \"delegate\" — agent cannot route work onward",
	}}
}

// ruleRoleWriteCapability fires when metadata.role promises write
// capability ("writes X", "edits Y", "curates Z") but neither write
// nor edit appears in tools. This is the regression that motivated
// b17038c2 — Knowledge-Base-Curator's role said "curates" but the
// shipped manifest had no write tool wired.
func ruleRoleWriteCapability(name string, probe validatorManifestProbe) []Violation {
	if !roleWritePattern.MatchString(probe.Metadata.Role) {
		return nil
	}
	hasWrite, hasEdit := false, false
	for _, t := range probe.Capabilities.Tools {
		switch t {
		case "write":
			hasWrite = true
		case "edit":
			hasEdit = true
		case "file":
			// engine bundle expands "file" to read+write, so accept it.
			hasWrite = true
		}
	}
	if hasWrite || hasEdit {
		return nil
	}
	return []Violation{{
		Manifest: name,
		Rule:     "role-write-capability-mismatch",
		Detail:   fmt.Sprintf("metadata.role claims write capability (%q) but tools[] declares neither write nor edit", probe.Metadata.Role),
	}}
}

// ruleCategoryRequired fires when the orchestrator_meta.category is
// in the rules table and one or more required tools are missing.
// Categories outside the table are not enforced — domain/specialist/
// research/exploration/advisor categories vary widely and any blanket
// rule would over-fit.
func ruleCategoryRequired(name string, probe validatorManifestProbe) []Violation {
	cat := strings.TrimSpace(probe.OrchestratorMeta.Category)
	required := requirementsForCategory(cat)
	if required == nil {
		return nil
	}
	declared := make(map[string]bool, len(probe.Capabilities.Tools))
	for _, t := range probe.Capabilities.Tools {
		declared[t] = true
		// Engine bundle aliases expand at runtime; mirror that here
		// so a manifest declaring "file" satisfies "read"+"write".
		switch t {
		case "file":
			declared["read"] = true
			declared["write"] = true
		case "delegate":
			declared["delegate"] = true
		}
	}
	var missing []string
	for _, req := range required {
		if !declared[req] {
			missing = append(missing, req)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []Violation{{
		Manifest: name,
		Rule:     "category-required-tool",
		Detail: fmt.Sprintf(
			"category %q requires %v but tools[] is missing %v",
			cat, required, missing,
		),
	}}
}

// requirementsForCategory returns the static rule table entry for the
// given category, or nil if the category is unenforced.
func requirementsForCategory(category string) []string {
	switch category {
	case "implementation":
		return implementationRequired
	case "documentation":
		return documentationRequired
	case "quality":
		return qualityRequired
	case "infrastructure":
		return infrastructureRequired
	case "orchestration", "coordination":
		return orchestrationRequired
	default:
		return nil
	}
}

// extractFrontmatterOrEmpty is a forgiving variant of the loader's
// extractFrontmatter. It returns ("", nil) on missing frontmatter so
// the validator can decide whether absence is itself a violation
// (rule-table choice) rather than aborting the whole walk.
func extractFrontmatterOrEmpty(content string) (string, error) {
	if !strings.HasPrefix(content, "---") {
		return "", nil
	}
	parts := strings.SplitN(content[3:], "---", 2)
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid frontmatter: missing closing ---")
	}
	return strings.TrimSpace(parts[0]), nil
}
