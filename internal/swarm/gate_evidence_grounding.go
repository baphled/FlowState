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

// EvidenceGroundingGateKind is the registered gate kind name. Pulled
// out of a string literal so the production-wiring side
// (App.buildSwarmGateRunner) and the manifest-validator side both
// reference the same constant — a typo here used to silently route to
// the "no runner registered" failure branch instead of failing build.
const EvidenceGroundingGateKind = "builtin:evidence-grounding"

// evidenceGroundingRunner implements GateRunner for kind:
// "builtin:evidence-grounding". It enforces that every finding a swarm
// member writes carries an evidence snippet that actually appears in
// the cited file — i.e. the LLM did not hallucinate the snippet or
// misattribute it to the wrong location.
//
// The runner reads the same coord-store payload the result-schema
// runner validates (the bug-findings-v1 shape), so it is a defence-in-
// depth pass that fires *after* the schema gate. Schema gate confirms
// shape; this gate confirms truth.
//
// Failure mode: any finding whose `evidence` string does not appear
// verbatim in the contents of `<repoRoot>/<file>` triggers a
// *GateError. Multiple mismatches aggregate into one error so the lead
// (and the user reviewing the failure) can see every hallucinated
// finding in a single report instead of bisecting through a fail-fast
// loop.
type evidenceGroundingRunner struct {
	// repoRoot is the absolute filesystem path the runner resolves
	// finding.file values against. Captured at construction so a
	// long-running flowstate-serve process always grounds findings in
	// the same root regardless of where individual swarm members were
	// dispatched from.
	repoRoot string

	// readFile is injected for tests that want to point the runner at
	// an in-memory virtual filesystem without staging real files. The
	// production wiring uses os.ReadFile.
	readFile func(path string) ([]byte, error)
}

// NewEvidenceGroundingRunner returns the production evidence-grounding
// runner anchored at repoRoot. An empty repoRoot falls back to the
// process working directory; this matches how `flowstate serve` is
// launched in the user's repo and keeps the no-config path productive.
//
// Expected:
//   - repoRoot is the absolute filesystem path the runner resolves
//     bug-findings file references against. May be empty (uses CWD).
//
// Returns:
//   - A GateRunner whose Run verifies each finding's evidence appears
//     in its cited file.
//
// Side effects:
//   - On nil/empty repoRoot, calls os.Getwd at construction.
func NewEvidenceGroundingRunner(repoRoot string) GateRunner {
	if repoRoot == "" {
		if cwd, err := os.Getwd(); err == nil {
			repoRoot = cwd
		}
	}
	return &evidenceGroundingRunner{
		repoRoot: repoRoot,
		readFile: os.ReadFile,
	}
}

// Run is the GateRunner entry point. It reads the member output from
// the coord-store, decodes it as bug-findings-v1-shaped JSON, and
// verifies each finding's `evidence` string appears in
// `<repoRoot>/<file>`. Multiple ungrounded findings aggregate into a
// single *GateError so the failure report names every hallucination at
// once rather than fail-fast on the first.
//
// Expected:
//   - gate.Kind == EvidenceGroundingGateKind. The dispatcher only
//     routes this runner for that kind.
//   - args.CoordStore is non-nil; nil short-circuits to a typed
//     "coordination store unavailable" gate failure.
//   - The coord-store payload at the resolved key parses as JSON with
//     a `findings` array of objects. A payload that lacks `findings`
//     passes (mirrors result-schema's behaviour where the schema's
//     `required` list is the contract for shape).
//
// Returns:
//   - nil when every finding's evidence is grounded.
//   - A *GateError listing every ungrounded finding when at least one
//     fails. The message names file:line and the first 80 chars of
//     the unmatched evidence so reviewers can locate the hallucination
//     without re-running the swarm.
//
// Side effects:
//   - Reads exactly one key from args.CoordStore.
//   - Reads each cited file via r.readFile (os.ReadFile in production).
//     File-system errors short-circuit to a typed gate failure.
func (r *evidenceGroundingRunner) Run(_ context.Context, gate GateSpec, args GateArgs) error {
	if args.CoordStore == nil {
		return newGateFailure(gate, args, "coordination store unavailable", nil)
	}
	payload, err := readMemberOutput(gate, args)
	if err != nil {
		return newGateFailure(gate, args, err.Error(), err)
	}

	var doc bugFindingsDoc
	if err := json.Unmarshal(payload, &doc); err != nil {
		return newGateFailure(gate, args, fmt.Sprintf("decoding bug-findings payload: %s", err.Error()), err)
	}

	mismatches := r.checkFindings(doc.Findings)
	if len(mismatches) == 0 {
		return nil
	}

	return newGateFailure(gate, args, formatMismatchReport(mismatches), nil)
}

// bugFindingsDoc is the minimal projection of bug-findings-v1 the
// runner needs. Additional fields the schema permits are left
// unmarshalled so the runner stays decoupled from schema growth.
type bugFindingsDoc struct {
	Findings []bugFinding `json:"findings"`
}

// bugFinding mirrors the per-finding shape from
// `~/.config/flowstate/schemas/bug-findings-v1.json`. The runner only
// reads the four fields it needs to ground evidence (`file`, `line`,
// `evidence`, plus `description` for the failure report).
type bugFinding struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
}

// evidenceMismatch captures one ungrounded finding for the aggregated
// failure report. The runner accumulates these and renders the full
// list in a single GateError so reviewers see every hallucination at
// once.
type evidenceMismatch struct {
	index    int
	file     string
	line     int
	reason   string
	evidence string
}

// checkFindings walks every finding and collects mismatches. Findings
// without `evidence` set are NOT mismatches at this layer — that
// requirement is enforced by the upstream result-schema gate, which
// fires before this one. Same for missing `file`: shape is the schema
// gate's contract; this gate's contract is "if it's there, it's true".
//
// Expected:
//   - findings is the slice from the bug-findings-v1 payload.
//
// Returns:
//   - A slice of mismatches in the order they appear in the input.
//
// Side effects:
//   - Reads each unique cited file via r.readFile. A file read error
//     becomes a mismatch with reason="file unreadable: <err>".
func (r *evidenceGroundingRunner) checkFindings(findings []bugFinding) []evidenceMismatch {
	var out []evidenceMismatch
	cache := make(map[string]string)
	for i, f := range findings {
		if f.Evidence == "" || f.File == "" {
			continue
		}
		body, err := r.loadFileCached(f.File, cache)
		if err != nil {
			out = append(out, evidenceMismatch{
				index:    i,
				file:     f.File,
				line:     f.Line,
				reason:   fmt.Sprintf("file unreadable: %s", err.Error()),
				evidence: f.Evidence,
			})
			continue
		}
		if !strings.Contains(body, f.Evidence) {
			out = append(out, evidenceMismatch{
				index:    i,
				file:     f.File,
				line:     f.Line,
				reason:   "evidence snippet does not appear in cited file",
				evidence: f.Evidence,
			})
		}
	}
	return out
}

// loadFileCached reads file under repoRoot, memoising the result so a
// member that cites the same file across N findings only pays the
// disk-read cost once. The cache lifespan is one Run call —
// intentionally short so a manifest hot-reload that edits a file mid-
// session is not masked across gate dispatches.
//
// Expected:
//   - file is a repo-relative path from a finding.
//   - cache survives only the current Run.
//
// Returns:
//   - The file content as a string and nil on success.
//   - "" and a wrapped error when the file cannot be read.
//
// Side effects:
//   - Calls r.readFile at most once per unique file per Run.
func (r *evidenceGroundingRunner) loadFileCached(file string, cache map[string]string) (string, error) {
	if cached, ok := cache[file]; ok {
		return cached, nil
	}
	resolved := filepath.Join(r.repoRoot, file)
	data, err := r.readFile(resolved)
	if err != nil {
		return "", err
	}
	body := string(data)
	cache[file] = body
	return body, nil
}

// formatMismatchReport renders the aggregated mismatches as a single
// human-readable failure reason. Sorted by index so the output order
// mirrors the source payload — reviewers can scan the lead's
// synthesised report and the gate failure side by side without
// re-correlating by file path.
//
// Expected:
//   - mismatches is non-empty (caller guards).
//
// Returns:
//   - The formatted reason string.
//
// Side effects:
//   - None.
func formatMismatchReport(mismatches []evidenceMismatch) string {
	sort.Slice(mismatches, func(i, j int) bool { return mismatches[i].index < mismatches[j].index })
	var b strings.Builder
	fmt.Fprintf(&b, "%d ungrounded finding(s):", len(mismatches))
	for _, m := range mismatches {
		location := m.file
		if m.line > 0 {
			location = fmt.Sprintf("%s:%d", m.file, m.line)
		}
		fmt.Fprintf(&b, "\n  - finding[%d] at %s — %s; evidence=%q",
			m.index, location, m.reason, truncate(m.evidence, 80))
	}
	return b.String()
}

// truncate clips s to at most maxLen runes, suffixing with `...` when
// the original was longer. Pulled out so the failure report stays
// scannable when a member emits a large code snippet — the reviewer
// only needs enough of the prefix to identify the hallucination.
//
// Expected:
//   - s is the snippet string.
//   - maxLen is positive.
//
// Returns:
//   - The original s when short enough; an ellipsis-suffixed prefix
//     otherwise.
//
// Side effects:
//   - None.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
