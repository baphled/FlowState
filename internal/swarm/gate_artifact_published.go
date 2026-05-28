package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ArtifactPublishedGateKind is the registered gate kind name for the
// honesty gate that verifies a planning loop actually produced its
// artifact before the loop may be declared complete. Exported so the
// production registration (App.buildSwarmGateRunner) and the manifest
// validator share the same authoritative literal.
const ArtifactPublishedGateKind = "builtin:artifact-published"

// planPublicationSuffix is the coord-store sub-key a lead writes its
// (self-reported) publication record under: the bug this gate exists to
// catch is a fabricated record claiming a vault path the file never
// reached. Resolved as "<chainID>/<suffix>".
const planPublicationSuffix = "plan_publication"

// statFunc reports whether a file exists at path. Injected at
// construction so the gate is testable without touching the real
// filesystem; production wiring passes osStat.
type statFunc func(path string) (bool, error)

// osStat is the production statFunc backed by os.Stat. A non-IsNotExist
// error is surfaced so a permission fault does not masquerade as
// "artifact absent".
func osStat(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// planPublication is the self-reported publication record shape. Only the
// vault_path is load-bearing for the honesty gate — published_at and any
// other fields are advisory and tolerated by DisallowUnknownFields being
// OFF here (a lead may add fields without breaking the gate).
type planPublication struct {
	VaultPath string `json:"vault_path"`
}

// artifactPublishedRunner implements GateRunner for kind
// "builtin:artifact-published". It gates loop completion on VERIFIED
// artifact existence rather than a self-reported record:
//
//  1. The coord-store "<chainID>/<plan-suffix>" key (gate.OutputKey
//     template) MUST be non-empty — the plan body must actually exist in
//     the coordination store.
//  2. A "<chainID>/plan_publication" record MUST exist. Its absence means
//     the loop never reached the vault — the deterministic post-swarm
//     publisher (swarm.PublishPlanToVault) writes this record only after
//     a real file lands, so a missing record IS the headline user bug:
//     "no plan in Obsidian". An absent record FAILS the gate.
//  3. The claimed vault_path MUST be under the resolved plan_output_dir
//     AND the file MUST actually exist at that path. A claim with no real
//     file — or a file written outside the output dir — FAILS the gate.
//
// Rationale (Defect 4 + the "no plan in Obsidian" bug): an approved plan
// sitting only in the coord-store used to PASS this gate because the
// publication record was OPTIONAL. That let the loop be declared complete
// while nothing ever wrote the plan to the vault. The deterministic
// publisher now writes the plan + record before this gate fires, so the
// gate makes both UNCONDITIONALLY required. Prompt instructions do not
// reliably fire (Defect 2), so the publish AND the verification are both
// code-level. A file stat cannot be talked past.
type artifactPublishedRunner struct {
	// outputDir is the resolved plan_output_dir. A claimed vault_path
	// must be under this directory to count as an honest publication.
	// Empty disables the path-containment check (the coord-store plan
	// key check still applies).
	outputDir string
	stat      statFunc
}

// NewArtifactPublishedRunner returns the honesty-gate runner.
//
// Expected:
//   - outputDir is the resolved plan_output_dir (absolute). Empty
//     disables the path-containment check.
//   - stat reports file existence; pass nil to default to os.Stat.
//
// Returns:
//   - A GateRunner that fails when a planning loop claims completion
//     without a verifiable artifact.
//
// Side effects:
//   - None at construction; Run reads the coord-store and stats files.
func NewArtifactPublishedRunner(outputDir string, stat statFunc) GateRunner {
	if stat == nil {
		stat = osStat
	}
	return artifactPublishedRunner{outputDir: outputDir, stat: stat}
}

// Run validates that the planning loop produced a real artifact.
//
// Expected:
//   - gate.Kind == artifactPublishedGateKind.
//   - gate.OutputKey resolves the plan key (typically "{chainID}/plan").
//   - args.CoordStore is non-nil; args.ChainID is the lead-allocated id.
//
// Returns:
//   - nil when the plan key is non-empty AND a publication record exists
//     AND the claimed vault file exists under the output dir.
//   - A *GateError naming the specific honesty failure otherwise. A
//     MISSING publication record now fails the gate (it used to pass) —
//     this is the "no plan in Obsidian" regression guard.
//
// Side effects:
//   - Reads up to two coord-store keys; stats at most one file. No writes.
func (r artifactPublishedRunner) Run(_ context.Context, gate GateSpec, args GateArgs) error {
	if args.CoordStore == nil {
		return newGateFailure(gate, args, "coordination store unavailable", nil)
	}

	planKey, body, err := r.resolvePlanBody(gate, args)
	if err != nil {
		return newGateFailure(gate, args, err.Error(), nil)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return newGateFailure(gate, args,
			fmt.Sprintf("plan artifact at %q is empty — the loop cannot be complete without a plan", planKey), nil)
	}

	// The plan key holds SOMETHING — but is it a coherent plan DOCUMENT or
	// garbage? The incident: the canonical "<chainID>/plan" key held a JSON
	// agent-spec blob, not a markdown plan, and the loop shipped 200 lines of
	// rendered garbage to the vault. Validate the SHAPE deterministically: a
	// JSON spec object, a heading-less prose dump, or a trivial stub is NOT a
	// publishable plan. This fails the honesty gate with the actionable
	// reason so the loop honest-fails instead of declaring completion on a
	// non-plan artifact. This mirrors the publisher's refusal (defence in
	// depth): even if some other writer landed a file outside the publisher,
	// a non-plan canonical key fails completion here.
	planBody := resolveValidationBody(body)
	if ok, reason := isPlanDocument(planBody); !ok {
		return newGateFailure(gate, args,
			fmt.Sprintf("plan artifact at %q is not a publishable plan: %s", planKey, reason), nil)
	}

	// The plan body exists. A publication record is now REQUIRED: the
	// deterministic post-swarm publisher writes it only after a real
	// vault file lands, so its absence means the plan never reached the
	// vault — the headline "no plan in Obsidian" bug. An approved plan
	// sitting only in the coord-store is NOT a complete loop.
	claim, ok := r.publicationClaim(args)
	if !ok || claim.VaultPath == "" {
		return newGateFailure(gate, args,
			"loop did not publish the plan to the vault — no plan_publication record "+
				"(an approved plan in the coordination store is not a published plan)", nil)
	}
	return r.verifyVaultClaim(gate, args, claim.VaultPath)
}

// resolvePlanBody returns the coord-store key holding the plan and its
// bytes.
//
// Resolution order, mirroring the result-schema runner's
// candidateKeys/suffix-scan duality:
//   - When gate.OutputKey carries a {chainID} template AND args.ChainID
//     is set, the concrete "<chainID>/plan" key is read directly.
//   - When the template is present but no concrete chainID is available
//     (the post-swarm dispatch path does not thread the lead's free-form
//     chainID — runSwarmGates leaves GateArgs.ChainID empty), the runner
//     suffix-scans the store for any "*/plan" key. This mirrors
//     coordWaveValidator.suffixPresent and the result-schema runner's
//     chainIDSuffixScan so the honesty gate works as a post-swarm gate
//     without new chainID threading.
//   - A non-templated explicit OutputKey is read verbatim.
func (r artifactPublishedRunner) resolvePlanBody(gate GateSpec, args GateArgs) (string, []byte, error) {
	if strings.Contains(gate.OutputKey, chainIDPlaceholder) {
		if args.ChainID != "" {
			key := strings.ReplaceAll(gate.OutputKey, chainIDPlaceholder, args.ChainID)
			body, err := args.CoordStore.Get(key)
			if err != nil {
				return key, nil, fmt.Errorf(
					"no plan artifact at %q — the loop cannot be complete without a plan", key)
			}
			return key, body, nil
		}
		suffix := strings.TrimPrefix(gate.OutputKey, chainIDPlaceholder+"/")
		key, body, found, err := r.suffixScan(args, suffix)
		if err != nil {
			return "", nil, err
		}
		if !found {
			return chainIDPlaceholder + "/" + suffix, nil, fmt.Errorf(
				"no plan artifact found at %q (any chain) — the loop cannot be complete without a plan",
				chainIDPlaceholder+"/"+suffix)
		}
		return key, body, nil
	}
	if gate.OutputKey != "" {
		body, err := args.CoordStore.Get(gate.OutputKey)
		if err != nil {
			return gate.OutputKey, nil, fmt.Errorf(
				"no plan artifact at %q — the loop cannot be complete without a plan", gate.OutputKey)
		}
		return gate.OutputKey, body, nil
	}
	return "", nil, fmt.Errorf("artifact-published gate requires an output_key naming the plan coord-store key")
}

// suffixScan walks the store for the first key ending in "/<suffix>",
// returning its value. Mirrors suffixScanForOutput in the result-schema
// runner. Over-approves across stale chains, which is acceptable for a
// post-swarm completion check: the gate's job is "did SOME plan land",
// and the publication-claim check below catches a stale-chain false hit
// when a vault path was claimed.
func (r artifactPublishedRunner) suffixScan(args GateArgs, suffix string) (string, []byte, bool, error) {
	keys, err := args.CoordStore.List("")
	if err != nil {
		return "", nil, false, fmt.Errorf("listing coord-store for suffix %q: %w", suffix, err)
	}
	target := "/" + suffix
	for _, k := range keys {
		if strings.HasSuffix(k, target) {
			body, getErr := args.CoordStore.Get(k)
			if getErr != nil {
				return "", nil, false, fmt.Errorf("reading coord-store key %q: %w", k, getErr)
			}
			return k, body, true, nil
		}
	}
	return "", nil, false, nil
}

// publicationClaim reads and decodes the lead's self-reported
// "<chainID>/plan_publication" record, when present. A missing key or a
// malformed record is treated as "no claim" (false) — the absence of a
// claim is honest; only a claim that contradicts the filesystem fails.
//
// When args.ChainID is empty (post-swarm dispatch), the record is located
// by suffix-scanning for any "*/plan_publication" key — the same fallback
// resolvePlanBody uses for the plan key.
func (r artifactPublishedRunner) publicationClaim(args GateArgs) (planPublication, bool) {
	raw, ok := r.publicationRecordBytes(args)
	if !ok {
		return planPublication{}, false
	}
	var claim planPublication
	if err := json.Unmarshal(raw, &claim); err != nil {
		return planPublication{}, false
	}
	return claim, true
}

// publicationRecordBytes returns the raw publication-record bytes,
// resolving the key directly when a chainID is available and by
// suffix-scan otherwise.
func (r artifactPublishedRunner) publicationRecordBytes(args GateArgs) ([]byte, bool) {
	if args.ChainID != "" {
		key := args.ChainID + "/" + planPublicationSuffix
		exists, err := args.CoordStore.Exists(key)
		if err != nil || !exists {
			return nil, false
		}
		raw, err := args.CoordStore.Get(key)
		if err != nil {
			return nil, false
		}
		return raw, true
	}
	_, raw, found, err := r.suffixScan(args, planPublicationSuffix)
	if err != nil || !found {
		return nil, false
	}
	return raw, true
}

// verifyVaultClaim checks that a claimed vault_path is under the resolved
// output dir AND that the file actually exists. Either failure is a
// fabricated-publication signal.
func (r artifactPublishedRunner) verifyVaultClaim(gate GateSpec, args GateArgs, vaultPath string) error {
	if r.outputDir != "" && !pathUnder(r.outputDir, vaultPath) {
		return newGateFailure(gate, args, fmt.Sprintf(
			"publication record claims vault_path %q, which is outside the resolved plan_output_dir %q — "+
				"a plan written outside the output dir is not an honest publication",
			vaultPath, r.outputDir), nil)
	}
	present, err := r.stat(vaultPath)
	if err != nil {
		return newGateFailure(gate, args, fmt.Sprintf(
			"could not verify claimed vault_path %q: %s", vaultPath, err.Error()), err)
	}
	if !present {
		return newGateFailure(gate, args, fmt.Sprintf(
			"publication record claims vault_path %q but no file exists there — the loop is NOT complete "+
				"(the publication record is self-reported and was not verified against the filesystem)",
			vaultPath), nil)
	}
	return nil
}

// pathUnder reports whether candidate is the directory dir itself or a
// descendant of it, comparing cleaned absolute paths so "/a/b/../b/c"
// resolves under "/a/b". A candidate that escapes via ".." returns false.
func pathUnder(dir, candidate string) bool {
	cleanDir := filepath.Clean(dir)
	rel, err := filepath.Rel(cleanDir, filepath.Clean(candidate))
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}
