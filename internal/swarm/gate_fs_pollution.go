package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FSPollutionGateKind is the registered gate kind name for the
// filesystem pollution guard. Mirrors EvidenceGroundingGateKind: a
// single constant so the production wiring (App.buildSwarmGateRunner)
// and manifests reference the same string without typo drift.
const FSPollutionGateKind = "builtin:fs-pollution-guard"

// fsPollutionDefaultVaultRoots are the sanctioned out-of-repo write
// roots. KB-Curator persists knowledge to the user's vault; everything
// else outside the active worktree is pollution.
var fsPollutionDefaultVaultRoots = []string{
	"vaults/baphled",
	".vaults/",
}

// fsWriteReport mirrors the per-write shape members publish to the
// coordination store under the gate's output key. The member's
// terminal output is a JSON document of {"writes": [...]}; the runner
// validates every reported path against the allow-list policy.
type fsWriteReport struct {
	Writes []fsWrite `json:"writes"`
}

// fsWrite is one reported filesystem write. Role carries the member's
// swarm role (e.g. "kb-curator") so vault paths can be scoped to the
// KB-Curator only.
type fsWrite struct {
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
	Role        string `json:"role"`
}

// fsPollutionRunner implements GateRunner for kind:
// "builtin:fs-pollution-guard". It enforces that swarm members never
// persist coordination or scratch data to the filesystem — inter-agent
// data must flow through the coordination store.
//
// Policy: a reported write is sanctioned when its resolved path is
// inside repoRoot (worktree source) or, for members carrying the
// kb-curator role, under one of the vault roots. Everything else
// (scratch notes, JSON dumps, cwd reports, /tmp handoff files) is a
// violation. Multiple violations aggregate into one *GateError.
type fsPollutionRunner struct {
	repoRoot   string
	vaultRoots []string
	stat       func(path string) (os.FileInfo, error)
}

// NewFSPollutionRunner returns the production fs-pollution-guard
// runner anchored at repoRoot. An empty repoRoot falls back to the
// process working directory, matching NewEvidenceGroundingRunner's
// no-config path.
//
// Expected:
//   - repoRoot is the absolute filesystem path of the active worktree.
//     May be empty (uses CWD).
//   - vaultRoots lists sanctioned out-of-repo write roots for the
//     KB-Curator role; empty selects fsPollutionDefaultVaultRoots.
//
// Returns:
//   - A GateRunner whose Run verifies every reported write is
//     sanctioned.
//
// Side effects:
//   - On nil/empty repoRoot, calls os.Getwd at construction.
func NewFSPollutionRunner(repoRoot string, vaultRoots []string) GateRunner {
	if repoRoot == "" {
		if cwd, err := os.Getwd(); err == nil {
			repoRoot = cwd
		}
	}
	if len(vaultRoots) == 0 {
		vaultRoots = fsPollutionDefaultVaultRoots
	}
	return &fsPollutionRunner{
		repoRoot:   repoRoot,
		vaultRoots: vaultRoots,
		stat:       os.Stat,
	}
}

// Run is the GateRunner entry point. It reads the member output from
// the coord-store, decodes it as an fs-write report, and verifies each
// reported path against the allow-list policy. Violations aggregate
// into a single *GateError naming every offending path.
//
// Expected:
//   - gate.Kind == FSPollutionGateKind.
//   - args.CoordStore is non-nil; nil short-circuits to a typed gate
//     failure.
//   - The payload parses as JSON. A payload without a "writes" array
//     passes (no writes reported).
//
// Returns:
//   - nil when every reported write is sanctioned.
//   - A *GateError listing every violating write.
//
// Side effects:
//   - Reads exactly one key from args.CoordStore.
func (r *fsPollutionRunner) Run(_ context.Context, gate GateSpec, args GateArgs) error {
	if args.CoordStore == nil {
		return newGateFailure(gate, args, "coordination store unavailable", nil)
	}
	payload, err := readMemberOutput(gate, args)
	if err != nil {
		return newGateFailure(gate, args, err.Error(), err)
	}

	var doc fsWriteReport
	if err := json.Unmarshal(payload, &doc); err != nil {
		return newGateFailure(gate, args, "decoding fs-write report: "+err.Error(), err)
	}

	violations := r.checkWrites(doc.Writes)
	if len(violations) == 0 {
		return nil
	}
	return newGateFailure(gate, args, formatFSPollutionReport(violations), nil)
}

// isSanctioned reports whether a reported write is permitted. Policy
// is content-type driven so path placement alone cannot launder a
// scratch write: "source" is sanctioned only under repoRoot; "vault"
// only under a vault root and only for the kb-curator role; any other
// content type (coordination, scratch, report, ...) is a violation in
// violation because that data belongs in the coordination store.
//
// Expected:
//   - resolved is the absolute, cleaned filesystem path of the write.
//   - contentType is the write's declared content_type (lower-cased).
//   - role is the reporting member's role (lower-cased).
//
// Returns:
//   - true only when the content-type/path/role triple is sanctioned.
//
// Side effects:
//   - None.
func (r *fsPollutionRunner) isSanctioned(resolved, contentType, role string) bool {
	switch contentType {
	case "source":
		return resolved == r.repoRoot || strings.HasPrefix(resolved+string(filepath.Separator), r.repoRoot+string(filepath.Separator))
	case "vault":
		if role != "kb-curator" {
			return false
		}
		for _, root := range r.vaultRoots {
			if fsPathWithinRoot(resolved, root) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// fsPathWithinRoot reports whether path is under root, tolerating the
// ~-relative and bare-name forms the vault roots are configured in.
// Each root is expanded (~/ prefix, bare name → $HOME/<name>) and
// matched as a path prefix.
//
// Expected:
//   - path is an absolute filesystem path.
//   - root is a configured vault root (possibly ~-relative or bare).
//
// Returns:
//   - true when path sits under the expanded root.
//
// Side effects:
//   - None.
func fsPathWithinRoot(path, root string) bool {
	expanded := expandVaultRoot(root)
	return strings.HasPrefix(path+string(filepath.Separator), expanded+string(filepath.Separator))
}

// home returns the user home directory, or "" when unavailable.
//
// Returns:
//   - The $HOME value, or "".
//
// Side effects:
//   - None.
func home() string {
	return os.Getenv("HOME")
}

// expandVaultRoot expands a configured vault root to an absolute path.
// A "~/" prefix resolves against $HOME; a bare name resolves to
// "$HOME/<name>"; an absolute path is returned unchanged.
//
// Expected:
//   - root is a configured vault root string.
//
// Returns:
//   - The expanded absolute path. When $HOME is unset the raw root is
//     returned unchanged (the prefix match then simply fails).
//
// Side effects:
//   - None.
func expandVaultRoot(root string) string {
	if strings.HasPrefix(root, "~/") && home() != "" {
		return filepath.Join(home(), strings.TrimPrefix(root, "~/"))
	}
	if !filepath.IsAbs(root) && !strings.HasPrefix(root, "~") && home() != "" {
		return filepath.Join(home(), root)
	}
	return root
}

// checkWrites walks every reported write and collects violations,
// resolving each path against the process CWD when relative.
//
// Expected:
//   - writes is the slice from the member's fs-write report.
//
// Returns:
//   - Violations in input order.
//
// Side effects:
//   - None (stat is reserved for future symlink resolution).
func (r *fsPollutionRunner) checkWrites(writes []fsWrite) []fsWrite {
	var out []fsWrite
	for _, w := range writes {
		if w.Path == "" {
			continue
		}
		resolved := w.Path
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(r.repoRoot, resolved)
		}
		resolved = filepath.Clean(resolved)
		if !r.isSanctioned(resolved, strings.ToLower(w.ContentType), strings.ToLower(w.Role)) {
			out = append(out, w)
		}
	}
	return out
}

// formatFSPollutionReport renders the aggregated violations as a
// single human-readable failure reason, sorted by input order.
//
// Expected:
//   - violations is non-empty (caller guards).
//
// Returns:
//   - The formatted reason string.
//
// Side effects:
//   - None.
func formatFSPollutionReport(violations []fsWrite) string {
	sorted := make([]fsWrite, len(violations))
	copy(sorted, violations)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	var b strings.Builder
	fmt.Fprintf(&b, "%d polluting filesystem write(s) reported; inter-agent data must flow through the coordination store:", len(sorted))
	for _, v := range sorted {
		fmt.Fprintf(&b, "\n  - %s (content_type=%s, role=%s)", v.Path, v.ContentType, v.Role)
	}
	return b.String()
}

// fsPollutionPatterns are the untracked-file name patterns the
// pre-commit pollution check blocks. Each is matched case-blindly
// against the file base name. Kept conservative to avoid blocking
// legitimate feature files: generic report/handoff/scratch/notes
// stems plus stray JSON at the repo root.
var fsPollutionPatterns = []string{
	"report", "handoff", "scratch", "notes", "summary", "handover",
}

// PreCommitPollutionCheck scans repoRoot for untracked files whose
// base names match the pollution patterns (e.g. stray-report.json,
// scratch-notes.md). It backs the .git-hooks/pre-commit guard and is
// exported so the BDD glue and the hook script share one policy.
//
// Expected:
//   - repoRoot is the absolute path of the repository to scan.
//   - untracked is the list of untracked file paths relative to
//     repoRoot (typically from `git ls-files --others`). When nil the
//     check is skipped (no git metadata), mirroring the hook's
//     behaviour outside a work tree.
//
// Returns:
//   - true when at least one untracked pollution file was found, with
//     a reason naming every offender.
//   - false, "" when the tree is clean.
//
// Side effects:
//   - None.
func PreCommitPollutionCheck(repoRoot string, untracked []string) (bool, string) {
	if untracked == nil {
		return false, ""
	}
	var offenders []string
	for _, rel := range untracked {
		base := strings.ToLower(filepath.Base(rel))
		if !fsNameMatchesPollution(base) {
			continue
		}
		if filepath.Dir(filepath.Clean(rel)) != "." {
			continue
		}
		offenders = append(offenders, rel)
	}
	if len(offenders) == 0 {
		return false, ""
	}
	sort.Strings(offenders)
	return true, "untracked pollution files present at repo root: " + strings.Join(offenders, ", ")
}

// fsNameMatchesPollution reports whether a base name (any case)
// matches the pollution patterns either as a stem prefix/suffix or as
// the exact stem (stray-report.json, scratch-notes.md, notes.md).
//
// Expected:
//   - base is the file base name including extension, any case.
//
// Returns:
//   - true when the name matches a pollution pattern.
//
// Side effects:
//   - None.
func fsNameMatchesPollution(base string) bool {
	stem := strings.ToLower(base)
	if i := strings.LastIndex(stem, "."); i > 0 {
		stem = stem[:i]
	}
	for _, p := range fsPollutionPatterns {
		if stem == p || strings.HasPrefix(stem, p+"-") || strings.HasSuffix(stem, "-"+p) {
			return true
		}
	}
	return false
}
