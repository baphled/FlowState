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

// FSPollutionMemberProfile is the manifest-declared authority record
// for one swarm member: its role and the content types its output
// profile sanctions. The manifest is the sole authority; a report
// that contradicts it fails closed per ADR-0001 gap 1.
type FSPollutionMemberProfile struct {
	// Role is the manifest-declared member role (e.g. "kb-curator").
	Role string
	// ContentTypes lists the content types the member's manifest
	// output profile sanctions (e.g. "source", "vault").
	ContentTypes []string
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
	profiles   map[string]FSPollutionMemberProfile
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
	return NewFSPollutionRunnerWithProfiles(repoRoot, vaultRoots, nil)
}

// NewFSPollutionRunnerWithProfiles returns an fs-pollution-guard
// runner whose role and content-type authority is the supplied
// member-profile map keyed by member id. A nil map means no members
// are attested, so every reported write fails closed (ADR-0001 gap
// 1: unknown declarations are unsanctioned).
//
// Expected:
//   - repoRoot is the absolute worktree path (may be empty for CWD).
//   - vaultRoots lists sanctioned out-of-repo write roots.
//   - profiles maps member ids to their manifest profiles.
//
// Returns:
//   - A GateRunner enforcing manifest-based type authority.
//
// Side effects:
//   - On nil/empty repoRoot, calls os.Getwd at construction.
func NewFSPollutionRunnerWithProfiles(repoRoot string, vaultRoots []string, profiles map[string]FSPollutionMemberProfile) GateRunner {
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
		profiles:   profiles,
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

	violations := r.checkWrites(args.MemberID, doc.Writes)
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

// resolveSymlinkAncestry resolves p through filepath.EvalSymlinks on
// its deepest existing ancestor, re-joining the non-existent tail.
// EvalSymlinks errors on missing final components, so the ancestor is
// walked up until it exists (or the path is exhausted) and the
// remainder re-attached. This closes the symlink-laundering gap from
// ADR-0001: containment is checked against the real target
// location, never a lexical path that merely looks sanctioned.
//
// Expected:
//   - p is an absolute, cleaned filesystem path.
//
// Returns:
//   - The symlink-resolved path; p unchanged when no ancestor exists
//     or resolution fails (fail closed to the lexical path, which the
//     containment check then evaluates as-is).
//
// Side effects:
//   - Stat/EvalSymlinks syscalls per ancestor level.
func resolveSymlinkAncestry(p string) string {
	current := p
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(append([]string{resolved}, tail...)...)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return p
		}
		tail = append([]string{filepath.Base(current)}, tail...)
		current = parent
	}
}

// manifestDeclarationMismatch compares a self-declared write against
// the member's manifest profile, the sole authority for role and
// content_type per ADR-0001 gap 1. Unknown or contradictory
// declarations fail closed with a reason naming the mismatch.
//
// Expected:
//   - profile is the manifest-declared member profile.
//   - w is the member's self-reported write.
//
// Returns:
//   - "" when the declaration is consistent; otherwise a reason
//     naming the contradiction.
//
// Side effects:
//   - None.
func manifestDeclarationMismatch(profile FSPollutionMemberProfile, w fsWrite) string {
	if !strings.EqualFold(w.Role, profile.Role) {
		return fmt.Sprintf("declared role %q, manifest declares %q", w.Role, profile.Role)
	}
	declared := strings.ToLower(w.ContentType)
	for _, ct := range profile.ContentTypes {
		if strings.ToLower(ct) == declared {
			return ""
		}
	}
	return fmt.Sprintf("declared content_type %q not in manifest output profile", w.ContentType)
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
//   - None.
func (r *fsPollutionRunner) checkWrites(memberID string, writes []fsWrite) []fsWrite {
	profile, attested := r.profiles[memberID]
	var out []fsWrite
	for _, w := range writes {
		if w.Path == "" {
			// Fail closed: a write entry without a path cannot be
			// validated, so treat it as a violation rather than
			// silently skipping it.
			out = append(out, w)
			continue
		}
		if !attested {
			out = append(out, fsWrite{Path: w.Path, ContentType: w.ContentType, Role: w.Role})
			continue
		}
		if mismatch := manifestDeclarationMismatch(profile, w); mismatch != "" {
			out = append(out, fsWrite{Path: w.Path, ContentType: w.ContentType, Role: w.Role + "; manifest mismatch: " + mismatch})
			continue
		}
		resolved := w.Path
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(r.repoRoot, resolved)
		}
		resolved = resolveSymlinkAncestry(filepath.Clean(resolved))
		if !r.isSanctioned(resolved, strings.ToLower(w.ContentType), strings.ToLower(profile.Role)) {
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
