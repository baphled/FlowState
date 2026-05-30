package swarm

import (
	"context"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/coordination"
)

// chainIDPlaceholder is the template token a manifest's output_key uses
// to defer chain-namespace resolution to runtime. When a gate's
// OutputKey contains it (e.g. "{chainID}/analysis"), the result-schema
// runner substitutes GateArgs.ChainID and reads the member's real
// "<chainID>/<suffix>" key directly — bypassing the legacy
// "<chainPrefix>/<target>/<output>" shape. This mirrors the
// "{chainID}" convention the wave validator already honours
// (internal/app/harness_adapter.go::coordWaveValidator).
const chainIDPlaceholder = "{chainID}"

// reviewerOutputKey is the canonical coord-store sub-key the
// plan-reviewer agent writes its verdict under (see
// coordination/persisting_store.go and the planner workflow).
// Retained as a fallback for manifests that pre-date the explicit
// GateSpec.OutputKey field so existing planning-loop runs do not
// regress; the planning-loop manifest itself now pins
// `output_key: review` so the fallback is exercise-only.
const reviewerOutputKey = "review"

// resultSchemaRunner implements GateRunner for kind:
// "builtin:result-schema". It validates the most-recent value the
// target member wrote to the coordination_store against a JSON Schema
// looked up in the in-process registry by gate.SchemaRef.
//
// Key resolution (see candidateKeys for the full ordering):
//   - When gate.OutputKey carries the "{chainID}" template, the key is
//     the template with GateArgs.ChainID substituted — a full
//     "<chainID>/<suffix>" key matching what planning-loop members
//     actually write. An empty ChainID triggers a suffix-scan fallback.
//   - Otherwise the legacy "<chainPrefix>/<target>/<output-key>" shape
//     applies, with output-key priority gate.OutputKey > the legacy
//     plan-reviewer convention > DefaultMemberOutputKey ("output").
type resultSchemaRunner struct{}

// NewResultSchemaRunner returns the production result-schema runner.
// It carries no state; the same instance is safe to share across
// goroutines.
//
// Returns:
//   - A GateRunner whose Run validates JSON output against a
//     registered schema.
//
// Side effects:
//   - None.
func NewResultSchemaRunner() GateRunner {
	return resultSchemaRunner{}
}

// Run is the GateRunner entry point. It looks up the schema, reads
// the member output from the coord-store, decodes it as JSON, and
// validates against the schema. Every failure path returns a
// *GateError so the swarm runner can halt with a structured surface.
//
// Expected:
//   - gate.Kind == "builtin:result-schema" (the dispatcher only routes
//     this runner for that kind).
//   - gate.SchemaRef is non-empty; an empty value short-circuits to a
//     "missing schema_ref" gate failure.
//   - args.CoordStore is non-nil in production wiring; nil short-
//     circuits to a "coordination store unavailable" gate failure.
//
// Returns:
//   - nil when validation passes.
//   - A *GateError describing the first failing precondition or the
//     schema validation failure.
//
// Side effects:
//   - Reads exactly one key from args.CoordStore. No writes.
func (resultSchemaRunner) Run(ctx context.Context, gate GateSpec, args GateArgs) error {
	if err := preflightGate(gate, args); err != nil {
		return err
	}
	resolved, ok := LookupSchema(gate.SchemaRef)
	if !ok {
		return newGateFailure(gate, args, fmt.Sprintf("schema_ref %q is not registered", gate.SchemaRef), nil)
	}
	payload, err := readMemberOutput(gate, args)
	if err != nil {
		return newGateFailure(gate, args, err.Error(), err)
	}
	// plan-document-v1 is validated by the publisher-render mirror, NOT the
	// raw JSON schema. The member gate must ACCEPT exactly the bodies the
	// post-swarm publisher (parsePlan / renderStructuredPlan) can render into a
	// non-empty plan document — a RAW MARKDOWN body ("# Implementation Plan…",
	// the plan-writer's ideal output), a {"markdown"/"plan":...} envelope, OR
	// the structured {title, content:{executive_summary, phased_slices}} shape
	// — and REJECT only genuinely non-renderable bodies (a contentless spec
	// blob, an empty body). A pure JSON schema cannot express "renderable into
	// a plan" without drifting from the renderer; routing through
	// ValidatePlanDocumentBody makes member-gate acceptance equivalent to
	// publisher renderability by construction.
	//
	// This branch is HOISTED ABOVE the generic decodeJSONInstance below
	// BECAUSE the plan-writer's happy-path output is raw Markdown, which is not
	// valid JSON: a `#`-leading body would otherwise short-circuit at the JSON
	// decode with "decoding member output as JSON: invalid character '#'…",
	// halting the swarm before this Markdown-tolerant predicate ever runs (the
	// publisher renders that same body fine, so the gate was the sole rejecter).
	// ValidatePlanDocumentBody needs only the RAW payload — it decodes JSON
	// internally when the body IS JSON (envelope/structured), so envelopes and
	// structured bodies still validate. The generic JSON decode is therefore
	// scoped to the OTHER result-schema gates (evidence-bundle-v1,
	// external-refs-v1, analysis-bundle-v1, review-verdict-v1), which are
	// genuinely JSON and must still fail a non-JSON body with the usual
	// "decoding member output as JSON" reason.
	if gate.SchemaRef == PlanDocumentV1Name {
		if err := ValidatePlanDocumentBody(payload); err != nil {
			return newGateFailure(gate, args, fmt.Sprintf("plan-document validation failed: %s", err.Error()), err)
		}
		return nil
	}
	// Planning-loop prose bundles (evidence / external-refs / analysis) are
	// consumed as RAW TEXT by the next LLM member — the coordination tool
	// returns string(val) verbatim into the consumer's tool-result, and the
	// analyst reads codebase-findings/external-refs as prose while the
	// plan-writer reads the analysis as prose. No Go code unmarshals these
	// bundles in the planning loop, so JSON-schema-validating them rejected
	// good prose/Markdown for no consumer benefit (the swarm's chronic
	// false-failure source). The gate's job for these schemas is "did the
	// member produce SOME content", not "is it this JSON struct": a non-empty,
	// non-trivial body passes; an empty / whitespace-only / empty-object body
	// still FAILS so the narrated-nothing synthesis-hang case is caught.
	// Scoped to exactly the three planning-loop prose schema names — every
	// OTHER result-schema gate (section-v1, code-review-verdict-v1, …) stays
	// on the strict decode+validate default path below.
	if isProseTolerantSchema(gate.SchemaRef) {
		if err := validateNonEmptyMemberOutput(payload); err != nil {
			return newGateFailure(gate, args, err.Error(), err)
		}
		return nil
	}
	// The plan-reviewer verdict DOES drive a machine decision, but the real
	// consumers grep a verdict TOKEN, not the JSON `verdict` enum:
	// coordination.containsApprovalVerdict and app.App.PersistApprovedPlan
	// both strings.Contains the uppercase "APPROVE", while the schema required
	// a lowercase `verdict: approve` nobody reads. Validate the SAME signal
	// the approve/reject loop keys on — the member output must carry one of
	// the recognised verdict tokens (APPROVE / REJECT / REVISE / ABORT,
	// centralised in internal/coordination so the gate and persisting store
	// cannot drift). A body with no recognised verdict token FAILS so the lead
	// re-prompts the reviewer for an explicit verdict.
	if gate.SchemaRef == ReviewVerdictV1Name {
		if err := validateVerdictToken(payload); err != nil {
			return newGateFailure(gate, args, err.Error(), err)
		}
		return nil
	}
	instance, err := decodeJSONInstance(payload)
	if err != nil {
		return newGateFailure(gate, args, err.Error(), err)
	}
	if err := resolved.Validate(instance); err != nil {
		return newGateFailure(gate, args, fmt.Sprintf("schema validation failed: %s", err.Error()), err)
	}
	return nil
}

// isProseTolerantSchema reports whether the schema_ref names one of the
// planning-loop bundles whose member output is consumed as RAW TEXT by the
// next LLM member (evidence-bundle-v1 / external-refs-v1 / analysis-bundle-v1).
// For these the result-schema gate validates presence + non-emptiness rather
// than a JSON struct, because no Go code typed-parses them. Any other schema
// (including section-v1 and code-review-verdict-v1, which ARE typed-parsed by
// other swarms' publishers) is NOT prose-tolerant and stays on the strict
// decode+validate path.
//
// Expected:
//   - schemaRef is the gate's SchemaRef.
//
// Returns:
//   - True for the three planning-loop prose schemas; false otherwise.
//
// Side effects:
//   - None.
func isProseTolerantSchema(schemaRef string) bool {
	switch schemaRef {
	case EvidenceBundleV1Name, ExternalRefsV1Name, AnalysisBundleV1Name:
		return true
	default:
		return false
	}
}

// validateNonEmptyMemberOutput is the prose-tolerant predicate: the member
// must have produced SOME substantive content. It accepts any non-trivial
// body (prose, Markdown, or JSON — JSON is just a non-empty body here) and
// rejects the narrated-nothing cases: an empty payload, a whitespace-only
// payload, or an empty JSON object/array ("{}" / "[]") which carries no
// findings for the next member to read.
//
// Expected:
//   - payload is the raw bytes the member wrote to the coord-store.
//
// Returns:
//   - nil when the body is substantive.
//   - An error naming the empty-output failure otherwise.
//
// Side effects:
//   - None.
func validateNonEmptyMemberOutput(payload []byte) error {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" || trimmed == "{}" || trimmed == "[]" {
		return fmt.Errorf("member produced no substantive output (empty or contentless body): %s", noOutputDirective)
	}
	return nil
}

// validateVerdictToken is the review-verdict predicate: the member output
// must carry one of the recognised verdict tokens the approve/reject loop
// keys on (APPROVE / REJECT / REVISE / ABORT). This validates the SAME signal
// the real consumers read (coordination.containsApprovalVerdict /
// app.App.PersistApprovedPlan grep the token), not the JSON `verdict` enum
// nobody parses.
//
// Expected:
//   - payload is the raw review body the plan-reviewer wrote.
//
// Returns:
//   - nil when a recognised verdict token is present.
//   - An error naming the missing-verdict failure otherwise.
//
// Side effects:
//   - None.
func validateVerdictToken(payload []byte) error {
	if !coordination.ContainsRecognisedVerdict(payload) {
		return fmt.Errorf(
			"review output carries no recognised verdict token (expected one of %s) — "+
				"re-delegate the reviewer with an explicit instruction to emit a VERDICT line",
			strings.Join(coordination.RecognisedVerdictTokens, " / "),
		)
	}
	return nil
}

// preflightGate enforces the runner-level invariants before any work
// hits the coord-store: a missing schema_ref or a nil store both
// produce typed *GateError surfaces so the swarm runner can halt
// uniformly without sniffing the underlying cause.
//
// Expected:
//   - gate is the GateSpec being dispatched.
//   - args carries the runtime state.
//
// Returns:
//   - nil when both schema_ref and store are populated.
//   - A *GateError with the first failing precondition otherwise.
//
// Side effects:
//   - None.
func preflightGate(gate GateSpec, args GateArgs) error {
	if gate.SchemaRef == "" {
		return newGateFailure(gate, args, "missing schema_ref on builtin:result-schema gate", nil)
	}
	if args.CoordStore == nil {
		return newGateFailure(gate, args, "coordination store unavailable", nil)
	}
	return nil
}

// readMemberOutput pulls the most-recent member output from the
// coord-store, probing each candidate key in priority order. The first
// hit wins; a miss on a key advances to the next candidate so older
// manifests (no explicit output_key, plan-reviewer convention) keep
// working alongside the new explicit-key path.
//
// When the gate's OutputKey carries the "{chainID}" template and no
// concrete chainID is available (args.ChainID == ""), the resolver
// cannot pin a single key. It then falls back to a suffix-scan over the
// store — accepting any key ending in "/<suffix>" — exactly as
// coordWaveValidator.MissingForChain does for the bootstrap case where
// the lead has not yet allocated a chainID.
//
// Expected:
//   - args.CoordStore is non-nil (preflightGate has already checked).
//   - gate.Target is the agent id whose output is being validated.
//
// Returns:
//   - The byte payload and nil on success.
//   - nil and a wrapped error when no candidate key exists.
//
// Side effects:
//   - Calls args.CoordStore.Exists / Get / List; no writes.
func readMemberOutput(gate GateSpec, args GateArgs) ([]byte, error) {
	keys := candidateKeys(gate, args)
	for _, key := range keys {
		exists, err := args.CoordStore.Exists(key)
		if err != nil {
			return nil, fmt.Errorf("probing coord-store key %q: %w", key, err)
		}
		if !exists {
			continue
		}
		payload, err := args.CoordStore.Get(key)
		if err != nil {
			return nil, fmt.Errorf("reading coord-store key %q: %w", key, err)
		}
		return payload, nil
	}
	if suffix, ok := chainIDSuffixScan(gate, args); ok {
		payload, found, err := suffixScanForOutput(args.CoordStore, suffix)
		if err != nil {
			return nil, err
		}
		if found {
			return payload, nil
		}
		return nil, fmt.Errorf("no member output found at %q (any chain): %s", suffix, noOutputDirective)
	}
	return nil, fmt.Errorf("no member output found at %v: %s", keys, noOutputDirective)
}

// noOutputDirective is appended to every "no member output found" gate
// failure. The lead receives this reason verbatim as its delegate-tool
// error result (the swarm runner halts fail-fast; delegation.go returns
// the *GateError to the caller). It is the swarm-gate-path counterpart of
// the harness wave-fan-in directive: a member that narrated a write but
// produced no coord-store output is the synthesis-hang signature, and a
// passive "key absent" message lets the lead narrate again. This text
// directs the lead to RE-DELEGATE the write with an explicit
// perform-the-write instruction. See internal/plan/harness/waves.go
// buildWaveFeedback for the harness-side directive.
const noOutputDirective = "the member did not write its output — it likely narrated the write but emitted no tool call. " +
	"Re-delegate this member with an explicit instruction to perform the coordination_store write (do not narrate it), " +
	"naming the concrete chainID and target key."

// chainIDSuffixScan reports the bare key suffix to suffix-scan for when
// the gate's OutputKey is "{chainID}"-templated but no concrete chainID
// is available. Returns ("", false) when the gate does not warrant a
// scan (no template, or a chainID was supplied so the templated key
// already resolved to a concrete probe above).
//
// Expected:
//   - gate is the GateSpec being dispatched.
//   - args carries the runtime ChainID (possibly empty).
//
// Returns:
//   - The suffix (e.g. "analysis") and true when a scan is warranted.
//   - "" and false otherwise.
//
// Side effects:
//   - None.
func chainIDSuffixScan(gate GateSpec, args GateArgs) (string, bool) {
	if args.ChainID != "" {
		return "", false
	}
	if !strings.Contains(gate.OutputKey, chainIDPlaceholder) {
		return "", false
	}
	suffix := strings.TrimPrefix(gate.OutputKey, chainIDPlaceholder+"/")
	if suffix == "" || suffix == gate.OutputKey {
		return "", false
	}
	return suffix, true
}

// suffixScanForOutput walks the store and returns the first value whose
// key ends in "/<suffix>". Mirrors coordWaveValidator.suffixPresent but
// returns the payload so the schema validation can run on it. The scan
// can over-approve across stale chains; the planning-loop only reaches
// this path before the lead allocates a chainID, which the prompt
// discipline makes a transient window.
//
// Expected:
//   - store is non-nil.
//   - suffix is the bare sub-key (no leading slash).
//
// Returns:
//   - The payload and true on a hit.
//   - nil and false when no key matches.
//   - A wrapped error if the store List fails.
//
// Side effects:
//   - Calls store.List / store.Get; no writes.
func suffixScanForOutput(store coordination.Store, suffix string) ([]byte, bool, error) {
	keys, err := store.List("")
	if err != nil {
		return nil, false, fmt.Errorf("listing coord-store for suffix %q: %w", suffix, err)
	}
	target := "/" + suffix
	for _, k := range keys {
		if strings.HasSuffix(k, target) {
			payload, getErr := store.Get(k)
			if getErr != nil {
				return nil, false, fmt.Errorf("reading coord-store key %q: %w", k, getErr)
			}
			return payload, true, nil
		}
	}
	return nil, false, nil
}

// CandidateKeys exposes the result-schema runner's coord-store key
// resolution to in-package callers OUTSIDE this file (the engine's
// reply-salvage step). The salvage writes the member's final reply into
// the SAME key the post-member gate reads from, so it must resolve the
// key with EXACTLY this runner's logic — duplicating the {chainID}
// substitution / joinKey shape in the engine would silently drift the
// write key from the read key. The first element is the canonical
// (highest-priority) key the gate probes; salvage targets that one.
//
// Expected:
//   - gate is the post-member result-schema gate whose OutputKey to
//     resolve; args carries ChainPrefix + ChainID for substitution.
//
// Returns:
//   - The same slice candidateKeys returns; nil when a {chainID}-
//     templated key has no concrete ChainID to substitute.
//
// Side effects:
//   - None (pure resolution).
func CandidateKeys(gate GateSpec, args GateArgs) []string {
	return candidateKeys(gate, args)
}

// candidateKeys lists the coord-store keys the result-schema runner
// will probe for the gate target's terminal output, in priority order.
// The list is stable so tests can pin the lookup ordering.
//
// Resolution priority:
//   - If gate.OutputKey carries the "{chainID}" template, the candidate
//     is the template with args.ChainID substituted — a full
//     "<chainID>/<suffix>" key with NO injected <target> segment,
//     because the planning-loop members write to "<chainID>/<suffix>"
//     directly (see internal/swarm/schemas.go and
//     internal/app/agents/*.md). When args.ChainID is empty the template
//     cannot resolve to a concrete key, so candidateKeys returns nil and
//     readMemberOutput falls through to the suffix-scan path.
//   - Otherwise if gate.OutputKey is set, that key is the only
//     candidate under the legacy "<prefix>/<target>/<output_key>" shape.
//     The manifest is authoritative; we do not fall back to a
//     convention because a wrong-key read would silently validate the
//     wrong data.
//   - Otherwise the legacy plan-reviewer convention probes
//     "<prefix>/plan-reviewer/review" first then
//     "<prefix>/plan-reviewer/output" so existing manifests keep
//     working.
//   - Otherwise (any other member with no explicit OutputKey) the
//     single candidate is "<prefix>/<target>/output".
//
// Expected:
//   - gate.Target is non-empty in production wiring; an empty Target
//     yields keys with the "<chainPrefix>//<sub>" shape, which the
//     coord-store will report as missing.
//   - args.ChainPrefix may be empty.
//
// Returns:
//   - A slice of candidate keys; may be nil when a {chainID}-templated
//     key has no concrete chainID to substitute.
//
// Side effects:
//   - None.
func candidateKeys(gate GateSpec, args GateArgs) []string {
	if strings.Contains(gate.OutputKey, chainIDPlaceholder) {
		if args.ChainID == "" {
			return nil
		}
		return []string{strings.ReplaceAll(gate.OutputKey, chainIDPlaceholder, args.ChainID)}
	}
	if gate.OutputKey != "" {
		return []string{joinKey(args.ChainPrefix, gate.Target, gate.OutputKey)}
	}
	if gate.Target == legacyReviewerMemberID {
		return []string{
			joinKey(args.ChainPrefix, gate.Target, reviewerOutputKey),
			joinKey(args.ChainPrefix, gate.Target, DefaultMemberOutputKey),
		}
	}
	return []string{joinKey(args.ChainPrefix, gate.Target, DefaultMemberOutputKey)}
}

// legacyReviewerMemberID is the member id whose convention-based
// fallback we keep for backwards compatibility with manifests that
// pre-date the explicit GateSpec.OutputKey field. Hoisted to a named
// constant so the candidateKeys branch reads as a deliberate legacy
// concession rather than a hard-coded surprise.
const legacyReviewerMemberID = "plan-reviewer"

// joinKey builds a coord-store key from the parts, skipping empty
// segments so an empty chainPrefix yields "<memberID>/<sub>" rather
// than a leading slash. The store is permissive about key shape but
// downstream tooling (List() prefix walks, log filters) prefers a
// stable form.
//
// Expected:
//   - parts contains at least the memberID and sub-key segments.
//
// Returns:
//   - The joined key.
//
// Side effects:
//   - None.
func joinKey(parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out == "" {
			out = p
			continue
		}
		out += "/" + p
	}
	return out
}

// newGateFailure constructs the canonical *GateError surface for a
// failing builtin:result-schema run. Callers always go through this
// helper so the GateName / GateKind / scope fields are populated
// consistently.
//
// Expected:
//   - gate is the GateSpec being dispatched.
//   - args carries the runtime state.
//   - reason is the user-facing message.
//   - cause may be nil when no underlying error exists.
//
// Returns:
//   - A populated *GateError.
//
// Side effects:
//   - None.
func newGateFailure(gate GateSpec, args GateArgs, reason string, cause error) *GateError {
	return &GateError{
		GateName: gate.Name,
		GateKind: gate.Kind,
		When:     gate.When,
		SwarmID:  args.SwarmID,
		MemberID: args.MemberID,
		Reason:   reason,
		Cause:    cause,
	}
}
