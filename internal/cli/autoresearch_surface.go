// Surface-type detection for `flowstate autoresearch run`
// (Slice 4 of the autoresearch plan v3.1; rules pinned in § 4.4).
//
// The detected type controls the manifest-validate gate: it fires
// only when type=manifest. For type ∈ {skill, source} the gate is
// a no-op and the trial proceeds straight to scoring.
//
// Detection rules (applied in order — first match wins):
//
//  1. **Path heuristic.** If the surface lives under cfg.AgentDir
//     or any cfg.AgentDirs entry, type=manifest. Cheap and fast
//     because the operator's primary agent registry is the
//     canonical home for manifests.
//  2. **Frontmatter probe.** Else if the surface is an .md file
//     with YAML frontmatter that carries either capabilities.tools
//     or delegation.delegation_allowlist, type=manifest. These two
//     keys are the verified manifest-only markers; schema_version
//     was rejected as a marker because it appears on plan and ADR
//     notes too.
//  3. **Skill heuristic.** Else if the surface is under a skills/
//     directory and is named SKILL.md, type=skill.
//  4. **Else** — type=source.
//
// The helper is small and side-effect-free aside from the
// frontmatter read (capped at 8 KiB so a malformed file cannot
// stall the detector).

package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SurfaceType enumerates the three surface shapes the harness
// distinguishes. Stored on the manifest record and on each trial
// record so an operator can audit which gate fired.
type SurfaceType string

const (
	SurfaceTypeManifest SurfaceType = "manifest"
	SurfaceTypeSkill    SurfaceType = "skill"
	SurfaceTypeSource   SurfaceType = "source"
)

// frontmatterReadLimit caps the bytes consumed when probing a file
// for YAML frontmatter. Manifest frontmatter at the head of an .md
// file fits comfortably within 8 KiB; reading beyond that risks
// pathological inputs stalling the detector.
const frontmatterReadLimit = 8 * 1024

// detectSurfaceType classifies surface against the rules in § 4.4.
//
// Expected:
//   - surface is an absolute or relative path to an existing file.
//   - agentDirs is the union of cfg.AgentDir and cfg.AgentDirs
//     (caller assembles this so the helper stays config-agnostic).
//
// Returns:
//   - The detected SurfaceType.
//   - An error only if the surface path cannot be resolved to an
//     absolute form. A frontmatter read failure on rule 2 is NOT
//     fatal — the helper falls through to rules 3/4.
//
// Side effects:
//   - May read up to frontmatterReadLimit bytes from the surface
//     when rule 2 is consulted.
func detectSurfaceType(surface string, agentDirs []string) (SurfaceType, error) {
	absSurface, err := filepath.Abs(surface)
	if err != nil {
		return "", fmt.Errorf("resolving surface path: %w", err)
	}

	// Rule 1 — path heuristic.
	for _, dir := range agentDirs {
		if dir == "" {
			continue
		}
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		if pathContains(absDir, absSurface) {
			return SurfaceTypeManifest, nil
		}
	}

	// Rule 2 — frontmatter probe (only relevant for .md files).
	if strings.EqualFold(filepath.Ext(absSurface), ".md") {
		hit, _ := frontmatterIsManifest(absSurface)
		// Frontmatter read errors deliberately fall through —
		// a missing or malformed frontmatter block is not a
		// detection failure; rule 4 will pick the file up as
		// source.
		if hit {
			return SurfaceTypeManifest, nil
		}
	}

	// Rule 3 — skill heuristic.
	if isSkillPath(absSurface) {
		return SurfaceTypeSkill, nil
	}

	// Rule 4 — fallback.
	return SurfaceTypeSource, nil
}

// pathContains reports whether child lives under parent. Both
// arguments are expected to be cleaned absolute paths. The
// comparison is byte-wise after a trailing separator is appended
// to parent so that /agents-extra is not treated as a prefix of
// /agents.
func pathContains(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return true
	}
	if !strings.HasSuffix(parent, string(filepath.Separator)) {
		parent += string(filepath.Separator)
	}
	return strings.HasPrefix(child, parent)
}

// isSkillPath returns true when surface lives inside a `skills`
// directory (any depth) and is named SKILL.md. Per § 4.4 rule 3
// the skill heuristic is intentionally narrow — arbitrary .md
// files under skills/ are NOT auto-classified as skill bodies;
// only the canonical SKILL.md filename qualifies.
func isSkillPath(surface string) bool {
	if filepath.Base(surface) != "SKILL.md" {
		return false
	}
	dir := filepath.Dir(surface)
	for {
		if filepath.Base(dir) == "skills" {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// frontmatterIsManifest opens surface, reads up to
// frontmatterReadLimit bytes, extracts the leading YAML
// frontmatter block (delimited by `---` lines), and reports
// whether either capabilities.tools or
// delegation.delegation_allowlist is present.
//
// Returns:
//   - true when one of the manifest-only markers is found.
//   - false otherwise (no frontmatter, malformed YAML, neither
//     marker present).
//   - An error only on file I/O failures — callers may treat
//     errors as a soft "not a manifest" signal.
func frontmatterIsManifest(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, frontmatterReadLimit)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return false, err
	}
	content := string(buf[:n])

	frontmatter, ok := extractFrontmatter(content)
	if !ok {
		return false, nil
	}

	var probe struct {
		Capabilities struct {
			Tools any `yaml:"tools"`
		} `yaml:"capabilities"`
		Delegation struct {
			DelegationAllowlist any `yaml:"delegation_allowlist"`
		} `yaml:"delegation"`
	}
	if unmarshalErr := yaml.Unmarshal([]byte(frontmatter), &probe); unmarshalErr != nil {
		// Malformed YAML — treat as "not a manifest". The full
		// loader will surface a clearer error if this surface
		// is ever validated.
		return false, nil
	}
	if probe.Capabilities.Tools != nil {
		return true, nil
	}
	if probe.Delegation.DelegationAllowlist != nil {
		return true, nil
	}
	return false, nil
}

// extractFrontmatter pulls the YAML block delimited by leading
// and trailing `---` lines from the head of content. Returns the
// frontmatter body (without the delimiters) and a boolean
// indicating whether a well-formed delimiter pair was found.
func extractFrontmatter(content string) (string, bool) {
	// Frontmatter must begin on the very first line.
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return "", false
	}
	// Strip the opening delimiter (handle either line ending).
	rest := strings.TrimPrefix(content, "---\n")
	rest = strings.TrimPrefix(rest, "---\r\n")

	// Find the closing delimiter — `\n---` followed by a line
	// terminator OR end of buffer. We handle both LF and CRLF.
	end := -1
	candidates := []string{"\n---\n", "\n---\r\n", "\n---"}
	for _, marker := range candidates {
		if idx := strings.Index(rest, marker); idx >= 0 {
			if end == -1 || idx < end {
				end = idx
			}
		}
	}
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}
