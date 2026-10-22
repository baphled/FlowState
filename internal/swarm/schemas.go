package swarm

import (
	"github.com/google/jsonschema-go/jsonschema"
)

// ReviewVerdictV1Name is the SchemaRef the planning-loop swarm
// references on its post-member gate (see internal/app/swarms/
// planning-loop.yml). Pinned as a constant so the seed registration
// and any test that re-registers an alternative shape stay aligned.
const ReviewVerdictV1Name = "review-verdict-v1"

// EvidenceBundleV1Name is the SchemaRef the planning-loop's
// post-member gate uses to validate the explorer agent's output
// (see internal/app/agents/explorer.md — "Coordination Store
// Integration / Key: {chainID}/codebase-findings").
const EvidenceBundleV1Name = "evidence-bundle-v1"

// ExternalRefsV1Name is the SchemaRef the planning-loop's post-member
// gate uses to validate the librarian agent's output (see
// internal/app/agents/librarian.md — "Coordination Store / path
// {chainID}/external-refs").
const ExternalRefsV1Name = "external-refs-v1"

// AnalysisBundleV1Name is the SchemaRef the planning-loop's
// post-member gate uses to validate the analyst agent's synthesis
// (see internal/app/agents/analyst.md — "Output Protocol / Write
// your final analysis to {chainID}/analysis").
const AnalysisBundleV1Name = "analysis-bundle-v1"

// PlanDocumentV1Name is the SchemaRef the planning-loop's
// post-member gate uses to validate the plan-writer agent's plan
// (see internal/app/agents/plan-writer.md — "Coordination Store
// (chain-local handoff) / coordination_store write
// {chainID}/plan <markdown_content>"). The plan-writer hands the
// plan to the chain-local store as a small wrapper carrying the
// markdown body so downstream readers can sanity-check shape
// without re-parsing the markdown.
const PlanDocumentV1Name = "plan-document-v1"

// CodeReviewVerdictV1Name is the SchemaRef the bug-triage swarm
// (and any future review-flavoured swarm) uses on its post-member
// gate to validate Code-Reviewer's structured output (see
// ~/.config/flowstate/swarms/bug-triage.yml). The shape captures a
// reviewer's verdict plus optional grounding so downstream synthesis
// can quote concerns and references without re-parsing prose.
const CodeReviewVerdictV1Name = "code-review-verdict-v1"

// VaultFindingsV1Name is the SchemaRef for the vault-explorer
// agent's terminal output inside the mental-health-swarm.
// Validates that the member wrote structured findings with at
// least a summary and a findings array.
const VaultFindingsV1Name = "vault-findings-v1"

// TrackerAnalysisV1Name is the SchemaRef for the tracker-analyst
// agent's terminal output inside the mental-health-swarm.
// Validates that the member wrote structured analysis with at
// least a summary and a metrics array.
const TrackerAnalysisV1Name = "tracker-analysis-v1"

// SectionV1Name is the SchemaRef the section-decomposed planning
// sub-swarm (internal/app/swarms/plan-sme-swarm.yml) uses on the
// per-member post-member gate to validate each SME's plan-section
// output. Every section specialist writes a distinct
// `{chainID}/sections/<name>` coord-store key (architecture / testing
// / security in v1) carrying a structured section wrapper; the
// deterministic publisher (Pass 2) fans those keys in and assembles
// them under the OMO spine. The schema is intentionally GENERAL — it
// describes "a plan section", not a specific one — so the same name
// gates every section member regardless of which `<name>` it owns.
// See [[ADR - Engine-Owned Workflow Mechanics]] § "Application —
// Section-Decomposed Planning via SME Sub-Swarm".
const SectionV1Name = "section-v1"

// ReviewVerdictV1Schema returns the Phase 1 placeholder schema for
// review-verdict-v1.
//
// Phase 1 shape (placeholder; the canonical schema can land
// independently and re-register under the same name):
//
//   - object root.
//   - required string property "verdict" — one of "approve" /
//     "revise" / "abort".
//   - optional string property "reasoning".
//
// The placeholder is a documented contract so the planning-loop
// reference swarm has a working post-member gate; the moment a
// real-world review verdict has additional required fields, replace
// this constructor (and re-Register the resolved value) without
// touching the runner.
//
// Returns:
//   - A fresh *jsonschema.Schema. Callers Resolve before registering.
//
// Side effects:
//   - None.
func ReviewVerdictV1Schema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"verdict": {
				Type: "string",
				Enum: []any{"approve", "revise", "abort"},
			},
			"reasoning": {Type: "string"},
		},
		Required: []string{"verdict"},
	}
}

// EvidenceBundleV1Schema returns the Phase 1 schema for the
// explorer agent's terminal output. The shape is intentionally
// permissive on the entry-level fields (only `file` is required)
// so a working explorer's natural variations don't get rejected;
// the wrapper-level `findings` array is the only truly load-bearing
// invariant — without it downstream synthesis has nothing to chew
// on.
//
// Phase 1 shape:
//
//   - object root with required `findings` array.
//   - each finding entry is an object with required `file` (the
//     codebase path the finding cites) and permissive optional
//     fields for line / pattern / context / implication / summary
//     / relevance, all of which the agent prompt mentions but none
//     of which are universally present in every finding type.
//
// `additionalProperties` is left unset on every level so downstream
// agents can attach extra metadata without re-cutting the schema —
// JSON Schema's default permits unknown properties.
//
// Returns:
//   - A fresh *jsonschema.Schema. Callers Resolve before registering.
//
// Side effects:
//   - None.
func EvidenceBundleV1Schema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"findings": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"file":        {Type: "string"},
						"line":        {Type: "integer"},
						"pattern":     {Type: "string"},
						"context":     {Type: "string"},
						"implication": {Type: "string"},
						"summary":     {Type: "string"},
						"relevance":   {Type: "string"},
					},
					Required: []string{"file"},
				},
			},
		},
		Required: []string{"findings"},
	}
}

// ExternalRefsV1Schema returns the Phase 1 schema for the librarian
// agent's terminal output. The agent prompt names a wrapper holding
// an array of references; each reference must at minimum cite a URL
// (without a URL the entry has no traceability). Title and other
// metadata are recommended by the prompt but not strictly required.
//
// Phase 1 shape:
//
//   - object root with required `references` array.
//   - each reference entry has required `url` and optional
//     descriptive fields the agent prompt mentions (title, type,
//     relevance score, key excerpt, synthesis).
//
// Returns:
//   - A fresh *jsonschema.Schema. Callers Resolve before registering.
//
// Side effects:
//   - None.
func ExternalRefsV1Schema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"references": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"url":       {Type: "string"},
						"title":     {Type: "string"},
						"type":      {Type: "string"},
						"relevance": {},
						"excerpt":   {Type: "string"},
						"synthesis": {Type: "string"},
					},
					Required: []string{"url"},
				},
			},
		},
		Required: []string{"references"},
	}
}

// AnalysisBundleV1Schema returns the Phase 1 schema for the
// analyst agent's synthesis output. The agent prompt names a
// rich JSON shape (summary / patterns / best_practices / gaps /
// risks / recommendations / metadata) but the load-bearing
// fields the plan-writer actually consumes downstream are
// `key_findings` (a digest of patterns + gaps + best practices)
// and `recommendations`. Keep the schema permissive on the
// rest — alternative analyst prompts that emit a slightly
// different field set should still pass.
//
// Phase 1 shape:
//
//   - object root with required `key_findings` and
//     `recommendations` arrays.
//   - both arrays accept either string entries or rich object
//     entries (the agent prompt shows both forms across
//     sections).
//
// Returns:
//   - A fresh *jsonschema.Schema. Callers Resolve before registering.
//
// Side effects:
//   - None.
func AnalysisBundleV1Schema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"key_findings": {
				Type: "array",
				Items: &jsonschema.Schema{
					Types: []string{"string", "object"},
				},
			},
			"recommendations": {
				Type: "array",
				Items: &jsonschema.Schema{
					Types: []string{"string", "object"},
				},
			},
			"summary":        {Type: "string"},
			"patterns":       {Type: "array"},
			"best_practices": {Type: "array"},
			"gaps":           {Type: "array"},
			"risks":          {Type: "array"},
			"metadata":       {Type: "object"},
		},
		Required: []string{"key_findings", "recommendations"},
	}
}

// PlanDocumentV1Schema returns the Phase 1 schema for the
// plan-writer agent's terminal output. The plan-writer is unique
// among planning members because its primary artefact is a
// markdown blob, not a JSON tree — but the coordination_store
// hand-off still wraps it as an object so the gate runner has
// something parseable to validate. The wrapper just needs to
// carry the markdown body under `markdown` (or the alias
// `plan`); other metadata fields the writer chooses to attach
// (id, title, status) are accepted but not required.
//
// Renderable-markdown requirement (the contract gap fix): the
// member gate MUST reject a body that carries no renderable plan
// prose. The live failure was a model (gpt-4o) emitting a
// structured body nested under a `content` OBJECT
// (executive_summary + phased_slices) with NO top-level
// `markdown`/`plan` string. The all-optional schema PASSED it,
// so the member gate let it through, and the post-swarm publisher
// — which renders the `markdown` field of the envelope — had
// nothing to render and failed the whole (expensive) run at the
// honesty gate. Requiring a non-empty `markdown` OR `plan` string
// makes the member gate catch the non-renderable shape, so the
// forced-tool-choice retry (PostMemberGateMaxAttempts +
// appendGateDirective) re-prompts the writer for a compliant
// {"markdown": "..."} body BEFORE the run reaches publish.
//
// Phase 1 shape:
//
//   - object root that MUST carry a non-empty `markdown` OR a
//     non-empty `plan` string (agent prompts inconsistently use
//     either key); whichever is present is the renderable plan
//     body. A body with neither (or an empty-string value) is
//     REJECTED — it has nothing the publisher can render.
//   - additional metadata strings (id / title / status) and any
//     extra fields (e.g. a structured `content` object) are
//     accepted but NOT sufficient on their own: the schema stays
//     permissive about EXTRA fields, only the renderable-markdown
//     requirement is enforced.
//
// Returns:
//   - A fresh *jsonschema.Schema. Callers Resolve before registering.
//
// Side effects:
//   - None.
func PlanDocumentV1Schema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"markdown": {Type: "string"},
			"plan":     {Type: "string"},
			"id":       {Type: "string"},
			"title":    {Type: "string"},
			"status":   {Type: "string"},
		},
		// At least one of `markdown` / `plan` must be present AND a
		// non-empty string. AnyOf requires the named key (so the
		// metadata-only and content-object bodies fail) and pins
		// minLength:1 on it (so an empty-string value fails too).
		AnyOf: []*jsonschema.Schema{
			{
				Required: []string{"markdown"},
				Properties: map[string]*jsonschema.Schema{
					"markdown": {Type: "string", MinLength: schemaIntPtr(1)},
				},
			},
			{
				Required: []string{"plan"},
				Properties: map[string]*jsonschema.Schema{
					"plan": {Type: "string", MinLength: schemaIntPtr(1)},
				},
			},
		},
	}
}

// schemaIntPtr returns a pointer to n for jsonschema's *int constraint
// fields (MinLength etc.). Kept local so the schema constructors stay
// declarative without a sprinkling of throwaway address-of locals.
//
// Expected: parameters for schemaIntPtr.
// Returns: result of schemaIntPtr.
// Side effects: None.
func schemaIntPtr(n int) *int {
	return &n
}

// CodeReviewVerdictV1Schema returns the Phase 2 schema for
// Code-Reviewer's structured output. The shape is deliberately
// permissive: `additionalProperties` is left unset so reviewers
// can attach extra annotations (rule ids, links, custom labels)
// without re-cutting the schema. Only `verdict` and `summary` are
// load-bearing — without those two there is nothing for the lead's
// synthesis to act on.
//
// Phase 2 shape:
//
//   - object root with required string `verdict` (enum:
//     "approve" / "request_changes" / "needs_more_evidence" /
//     "abstain") and required string `summary`.
//   - optional `concerns` (string array of categorised concerns).
//   - optional `severity_breakdown` (object with int counts for
//     critical / major / minor / nit; mirrors the bug-findings-v1
//     severity vocabulary so reviewers can fold counts in trivially).
//   - optional `references` array of {file, line?, snippet?} entries
//     so the reviewer can ground each verdict in the codebase.
//   - optional `confidence` enum (high / medium / low) for downstream
//     gating logic ("auto-approve only when confidence == high").
//
// The verdict enum's design choices:
//   - "approve" / "request_changes" mirror GitHub's review verbs so
//     operators reading logs immediately recognise the semantics.
//   - "needs_more_evidence" is distinct from "request_changes"
//     because the upstream symptom is "explorer/librarian didn't
//     surface enough to assess", not "the code itself is wrong".
//   - "abstain" lets the reviewer step out without forcing a false
//     positive on edge cases (e.g. domains the reviewer prompt
//     doesn't cover).
//
// Returns:
//   - A fresh *jsonschema.Schema. Callers Resolve before registering.
//
// Side effects:
//   - None.
func CodeReviewVerdictV1Schema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"verdict":            codeReviewVerdictEnum(),
			"summary":            {Type: "string"},
			"concerns":           codeReviewConcernsArray(),
			"severity_breakdown": codeReviewSeverityBreakdown(),
			"references":         codeReviewReferencesArray(),
			"confidence":         codeReviewConfidenceEnum(),
		},
		Required: []string{"verdict", "summary"},
	}
}

// codeReviewVerdictEnum returns the verdict-property sub-schema.
// Pulled out so the top-level constructor stays scannable.
//
// Returns:
//   - A *jsonschema.Schema constraining `verdict` to the four
//     supported terminal values.
//
// Side effects:
//   - None.
func codeReviewVerdictEnum() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "string",
		Enum: []any{"approve", "request_changes", "needs_more_evidence", "abstain"},
	}
}

// codeReviewConfidenceEnum returns the confidence-property
// sub-schema. Confidence is optional; when present it must be one
// of high / medium / low so downstream gating ("only auto-merge on
// high confidence") has a stable vocabulary.
//
// Returns:
//   - A *jsonschema.Schema constraining `confidence` to the three
//     supported levels.
//
// Side effects:
//   - None.
func codeReviewConfidenceEnum() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "string",
		Enum: []any{"high", "medium", "low"},
	}
}

// codeReviewConcernsArray returns the concerns-property sub-schema:
// an array of free-form strings the reviewer wants to flag without
// committing to a per-concern object shape. A future revision can
// promote this to a richer record type once the reviewer prompt
// settles on a canonical concern vocabulary.
//
// Returns:
//   - A *jsonschema.Schema describing a string array.
//
// Side effects:
//   - None.
func codeReviewConcernsArray() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:  "array",
		Items: &jsonschema.Schema{Type: "string"},
	}
}

// codeReviewSeverityBreakdown returns the severity_breakdown
// sub-schema: an object whose keys mirror the bug-findings-v1
// severity vocabulary (critical / major / minor / nit) so a
// reviewer that already classified findings can publish a count
// per bucket without inventing a new vocabulary. All four counts
// are optional integers; absent keys default to zero by convention
// at the consumer.
//
// Returns:
//   - A *jsonschema.Schema describing the four-int breakdown.
//
// Side effects:
//   - None.
func codeReviewSeverityBreakdown() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"critical": nonNegativeInt(),
			"major":    nonNegativeInt(),
			"minor":    nonNegativeInt(),
			"nit":      nonNegativeInt(),
		},
	}
}

// nonNegativeInt returns a fresh integer schema constrained to >= 0.
// The resolver in jsonschema-go requires the schema graph to form a
// tree (no shared sub-schema pointers); each call returns a new value
// so the four severity buckets stay distinct nodes.
//
// Returns:
//   - A *jsonschema.Schema for non-negative integers.
//
// Side effects:
//   - None.
func nonNegativeInt() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "integer", Minimum: floatPtr(0)}
}

// codeReviewReferencesArray returns the references sub-schema: an
// array of {file, line?, snippet?} objects so each concern can be
// grounded in a repo path. Only `file` is required because not
// every reference resolves to a single line (e.g. "the entire
// auth/ package is over-coupled") and the snippet is a courtesy
// for human readers rather than a load-bearing field.
//
// Returns:
//   - A *jsonschema.Schema describing the references array.
//
// Side effects:
//   - None.
func codeReviewReferencesArray() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "array",
		Items: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"file":    {Type: "string"},
				"line":    {Type: "integer", Minimum: floatPtr(1)},
				"snippet": {Type: "string"},
			},
			Required: []string{"file"},
		},
	}
}

// SectionV1Schema returns the Phase 1 schema for a single
// plan-section produced by an SME section specialist in the
// plan-sme-swarm. The shape is deliberately GENERAL so one schema
// name gates every section regardless of which `<name>` the member
// owns (architecture / testing / security in v1, more later) — the
// section's identity lives in the coord-store KEY
// (`{chainID}/sections/<name>`), not in the schema.
//
// Phase 1 shape:
//
//   - object root.
//   - required string `section` — the canonical section name the
//     specialist owns (e.g. "architecture"); the deterministic
//     publisher (Pass 2) uses this to order / title the assembled
//     output and to sanity-check that the body landed under the
//     matching key.
//   - required string `title` — a human-readable heading for the
//     section as it should appear in the rendered plan.
//   - required string `body` — the section's substance as markdown.
//     This is the load-bearing content the publisher stitches under
//     the OMO spine; a section with an empty body has nothing to
//     contribute, so it is required rather than optional.
//   - required `key_points` array of strings — a digest of the
//     section's headline takeaways so downstream readers (and the
//     plan-writer's lightweight spine) can cross-reference the
//     section without re-parsing the markdown body.
//
// `additionalProperties` is left unset (JSON Schema's permissive
// default) so a specialist can attach extra section-specific metadata
// (risk tables, owners, references) without re-cutting the schema.
//
// Returns:
//   - A fresh *jsonschema.Schema. Callers Resolve before registering.
//
// Side effects:
//   - None.
func SectionV1Schema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"section": {Type: "string"},
			"title":   {Type: "string"},
			"body":    {Type: "string"},
			"key_points": {
				Type:  "array",
				Items: &jsonschema.Schema{Type: "string"},
			},
		},
		Required: []string{"section", "title", "body", "key_points"},
	}
}

// VaultFindingsV1Schema returns the Phase 1 schema for the vault-explorer
// agent's terminal output inside the mental-health-swarm. The agent
// writes findings as structured JSON to the coord-store under
// `{chainID}/vault-explorer/findings`; this schema validates that the
// shape is parseable and carries the load-bearing fields.
//
// Phase 1 shape:
//
//   - object root with required `summary` (string) and `findings` array.
//   - each finding entry has required `area` (string), `pattern` (string),
//     `confidence` (enum: high/medium/low), and `implication` (string);
//     optional `evidence` array of strings provides supporting citations.
//   - `search_strategies_used` (string array) and `limitations` (string)
//     are optional metadata.
//
// Returns:
//   - A fresh *jsonschema.Schema. Callers Resolve before registering.
//
// Side effects:
//   - None.
func VaultFindingsV1Schema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"summary": {Type: "string"},
			"task":    {Type: "string"},
			"findings": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"area":        {Type: "string"},
						"pattern":     {Type: "string"},
						"confidence":  {Type: "string", Enum: []any{"high", "medium", "low"}},
						"evidence":    {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
						"implication": {Type: "string"},
					},
					Required: []string{"area", "pattern", "confidence", "implication"},
				},
			},
			"search_strategies_used": {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
			"limitations":            {Type: "string"},
		},
		Required: []string{"summary", "findings"},
	}
}

// TrackerAnalysisV1Schema returns the Phase 1 schema for the tracker-analyst
// agent's terminal output inside the mental-health-swarm. The agent writes
// structured analysis to `{chainID}/tracker-analyst/analysis`; this schema
// validates the shape is parseable and carries the load-bearing fields.
//
// Phase 1 shape:
//
//   - object root with required `summary` (string) and `metrics` array.
//   - `date_range` is an object with optional `from` and `to` strings.
//   - each metric entry has required `name` (string); optional `current_value`,
//     `previous_value`, `change`, `assessment`, `confidence`, and
//     `data_points` (integer).
//   - `correlations` and `alerts` arrays accept any type.
//   - `data_quality` is an object with optional `completeness_pct`,
//     `date_gaps` (string array), and `notes` (string).
//
// Returns:
//   - A fresh *jsonschema.Schema. Callers Resolve before registering.
//
// Side effects:
//   - None.
func TrackerAnalysisV1Schema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"summary": {Type: "string"},
			"date_range": {
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"from": {Type: "string"},
					"to":   {Type: "string"},
				},
			},
			"metrics": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"name":           {Type: "string"},
						"current_value":  {},
						"previous_value": {},
						"change":         {Type: "string"},
						"assessment":     {Type: "string"},
						"confidence":     {Type: "string"},
						"data_points":    {Type: "integer"},
					},
					Required: []string{"name"},
				},
			},
			"correlations": {Type: "array"},
			"alerts":       {Type: "array"},
			"data_quality": {
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"completeness_pct": {},
					"date_gaps":        {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
					"notes":            {Type: "string"},
				},
			},
		},
		Required: []string{"summary", "metrics"},
	}
}

// DispatchRecordV1Name is the SchemaRef the meta-swarm's post-swarm
// gate uses to validate the coordinator's structured dispatch record
// (see internal/app/swarms/meta-swarm.yml). Routing was previously
// pure model judgement with zero gates — the least deterministic
// point of the three-tier orchestration. The record pins the chosen
// sub-swarm, the reason, and the task summary so a post-hoc audit
// can verify the routing decision was defensible.
const DispatchRecordV1Name = "dispatch-record-v1"

// DDVerdictV1Name is the SchemaRef the due-diligence-swarm uses on
// its specialist post-member gates (bull/bear-flavoured analysts,
// Tech-Lead, Security-Engineer). It replaces the ext:keyword-coverage
// pseudo-gates — substring matching on words like "verdict" or "low"
// is trivially satisfied by boilerplate and cannot distinguish a real
// verdict from filler. The schema enforces a structured verdict with
// an explicit confidence percentage so the swarm-level confidence
// threshold gate reads a number, not prose.
const DDVerdictV1Name = "dd-verdict-v1"

// CriticVerdictV1Name is the SchemaRef the a-team swarm uses to
// mechanically enforce its "critic is mandatory" contract (see
// internal/app/agents/critic.md). Previously the adversarial
// engagement rule was prose-only; this schema requires at least one
// substantive objection with a classification, so a clean-pass
// rubber stamp fails the gate.
const CriticVerdictV1Name = "critic-verdict-v1"

// BoardDecisionV1Name is the SchemaRef the board-room swarm's
// post-swarm gate uses to validate the Chair's final structured
// decision at `board-room/{chainID}/decision`. The decision shape
// matches the one the chair manifest already documents (decision /
// rationale / dissent / conditions), so the gate closes the
// largest single-judgement surface without changing the agent's
// promised output.
const BoardDecisionV1Name = "board-decision-v1"

// FinalSynthesisV1Name is the SchemaRef shared by the lead-synthesis
// post-swarm gates of a-team and mental-health-swarm. It requires a
// summary and at least one takeaway so a lead that narrates nothing
// fails honestly instead of shipping an empty synthesis.
const FinalSynthesisV1Name = "final-synthesis-v1"

// floatPtr is a tiny helper for the *float64 fields the jsonschema-go
// library uses for numeric bounds. Pulled out so the schema bodies
// above stay readable.
//
// Expected:
//   - v is the literal float bound to publish.
//
// Returns:
//   - A heap-allocated *float64 wrapping v.
//
// Side effects:
//   - None.
func floatPtr(v float64) *float64 { return &v }

// DispatchRecordV1Schema returns the schema for the meta-swarm
// coordinator's dispatch record. Shape:
//
//   - object root.
//   - required string `chosen_swarm` — the sub-swarm id dispatched to.
//   - required string `route_reason` — why that sub-swarm fits.
//   - required string `task_summary` — what the user asked for,
//     restated in one or two sentences.
//
// Returns:
//   - A fresh *jsonschema.Schema. Callers Resolve before registering.
//
// Side effects:
//   - None.
func DispatchRecordV1Schema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"chosen_swarm": {Type: "string", MinLength: intPtr(1)},
			"route_reason": {Type: "string", MinLength: intPtr(1)},
			"task_summary": {Type: "string", MinLength: intPtr(1)},
		},
		Required: []string{"chosen_swarm", "route_reason", "task_summary"},
	}
}

// DDVerdictV1Schema returns the schema for a due-diligence
// specialist's structured verdict. Shape:
//
//   - object root.
//   - required string `verdict` — positive / negative / mixed /
//     inconclusive.
//   - required integer `confidence_pct` 0–100 — the number the
//     swarm-level confidence-threshold gate reads.
//   - required `findings` array of {title, severity, evidence}
//     objects — each finding must carry at least one evidence
//     citation so verdicts stay grounded.
//   - optional string `recommendation`.
//
// Returns:
//   - A fresh *jsonschema.Schema. Callers Resolve before registering.
//
// Side effects:
//   - None.
func DDVerdictV1Schema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"verdict": {
				Type: "string",
				Enum: []any{"positive", "negative", "mixed", "inconclusive"},
			},
			"confidence_pct": {Type: "integer", Minimum: floatPtr(0), Maximum: floatPtr(100)},
			"findings": {
				Type:     "array",
				MinItems: intPtr(1),
				Items: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"title":    {Type: "string", MinLength: intPtr(1)},
						"severity": {Type: "string", Enum: []any{"critical", "high", "medium", "low", "info"}},
						"evidence": {Type: "string", MinLength: intPtr(1)},
					},
					Required: []string{"title", "severity", "evidence"},
				},
			},
			"recommendation": {Type: "string"},
		},
		Required: []string{"verdict", "confidence_pct", "findings"},
	}
}

// CriticVerdictV1Schema returns the schema for the a-team critic's
// structured critique. The load-bearing field is `objections`: an
// array with minItems 1 whose every entry must carry a
// `classification` of breaks-strategy or material-risk — the
// manifest's "a clean pass is a failure" rule, made mechanical.
// Shape:
//
//   - object root.
//   - required string `summary`.
//   - required `objections` array (minItems 1) of
//     {assumption, argument, classification} objects where
//     classification is breaks-strategy / material-risk /
//     worth-noting, plus a schema-level contains-style contract
//     enforced via the `engaged` boolean echo below.
//   - required boolean `engaged` — must be true; the critic echoes
//     its own engagement attestation after the red-flag check.
//
// Returns:
//   - A fresh *jsonschema.Schema. Callers Resolve before registering.
//
// Side effects:
//   - None.
func CriticVerdictV1Schema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"summary": {Type: "string", MinLength: intPtr(1)},
			"objections": {
				Type:     "array",
				MinItems: intPtr(1),
				Items: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"assumption":     {Type: "string", MinLength: intPtr(1)},
						"argument":       {Type: "string", MinLength: intPtr(1)},
						"classification": {Type: "string", Enum: []any{"breaks-strategy", "material-risk", "worth-noting"}},
					},
					Required: []string{"assumption", "argument", "classification"},
				},
			},
			"engaged": {Type: "boolean", Enum: []any{true}},
		},
		Required: []string{"summary", "objections", "engaged"},
	}
}

// BoardDecisionV1Schema returns the schema for the board-room
// Chair's final decision, matching the JSON shape the chair
// manifest already documents at `board-room/{chainID}/decision`
// (decision / confidence / dissents / conditions /
// dealbreaker_risks). Preserved dissent is the protocol's core
// contract, so `dissents` is required (it may be an empty array
// only when the vote was unanimous).
//
// Returns:
//   - A fresh *jsonschema.Schema. Callers Resolve before registering.
//
// Side effects:
//   - None.
func BoardDecisionV1Schema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"decision": {
				Type: "string",
				Enum: []any{"invest", "pass", "conditional"},
			},
			"confidence": {Type: "integer", Minimum: floatPtr(1), Maximum: floatPtr(5)},
			"dissents": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"analyst_role":             {Type: "string", MinLength: intPtr(1)},
						"decision":                 {Type: "string", MinLength: intPtr(1)},
						"key_reasons":              {Type: "array", MinItems: intPtr(1), Items: &jsonschema.Schema{Type: "string"}},
						"most_compelling_evidence": {Type: "string"},
					},
					Required: []string{"analyst_role", "decision", "key_reasons"},
				},
			},
			"conditions":        {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
			"dealbreaker_risks": {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
		},
		Required: []string{"decision", "confidence", "dissents"},
	}
}

// FinalSynthesisV1Schema returns the shared schema for a lead's
// final synthesis output (a-team, mental-health-swarm). Shape:
//
//   - object root.
//   - required string `summary`.
//   - required `takeaways` array of strings (minItems 1).
//   - optional `next_steps` array of strings.
//
// Returns:
//   - A fresh *jsonschema.Schema. Callers Resolve before registering.
//
// Side effects:
//   - None.
func FinalSynthesisV1Schema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"summary":   {Type: "string", MinLength: intPtr(1)},
			"takeaways": {Type: "array", MinItems: intPtr(1), Items: &jsonschema.Schema{Type: "string"}},
			"next_steps": {
				Type:  "array",
				Items: &jsonschema.Schema{Type: "string"},
			},
		},
		Required: []string{"summary", "takeaways"},
	}
}

// intPtr is a companion to floatPtr for the *int fields the
// jsonschema-go library uses for array cardinality bounds.
//
// Expected:
//   - v is the literal integer bound to publish.
//
// Returns:
//   - A heap-allocated *int wrapping v.
//
// Side effects:
//   - None.
func intPtr(v int) *int { return &v }

// SeedDefaultSchemas registers every Phase 1 builtin schema with the
// in-process registry and marks those that are prose-tolerant. The CLI /
// app construction calls this once at startup so the planning-loop
// swarm's post-member gates have schemas to look up.
//
// Schemas marked prose-tolerant have their post-member gate validate
// presence + non-emptiness rather than strict JSON structure — because
// no Go code typed-parses their output; it is consumed as raw text by
// the next LLM member. Add new prose-tolerant schemas by setting
// proseTolerant: true in the seed list below — no separate list to
// maintain.
//
// Returns:
//   - nil on success.
//   - The first registration error otherwise. Errors here are
//     programmer mistakes (a malformed seed schema); the caller
//     surfaces them and refuses to start rather than running with a
//     half-seeded registry.
//
// Side effects:
//   - Calls RegisterSchema for each Phase 1 builtin and
//     MarkSchemaProseTolerant for those with proseTolerant: true.
func SeedDefaultSchemas() error {
	seeds := []struct {
		name          string
		schema        *jsonschema.Schema
		proseTolerant bool
	}{
		{ReviewVerdictV1Name, ReviewVerdictV1Schema(), false},
		{EvidenceBundleV1Name, EvidenceBundleV1Schema(), true},
		{ExternalRefsV1Name, ExternalRefsV1Schema(), true},
		{AnalysisBundleV1Name, AnalysisBundleV1Schema(), true},
		{PlanDocumentV1Name, PlanDocumentV1Schema(), false},
		{CodeReviewVerdictV1Name, CodeReviewVerdictV1Schema(), false},
		{SectionV1Name, SectionV1Schema(), false},
		{VaultFindingsV1Name, VaultFindingsV1Schema(), true},
		{TrackerAnalysisV1Name, TrackerAnalysisV1Schema(), true},
		{DispatchRecordV1Name, DispatchRecordV1Schema(), false},
		{DDVerdictV1Name, DDVerdictV1Schema(), false},
		{CriticVerdictV1Name, CriticVerdictV1Schema(), false},
		{BoardDecisionV1Name, BoardDecisionV1Schema(), false},
		{FinalSynthesisV1Name, FinalSynthesisV1Schema(), false},
	}
	for _, seed := range seeds {
		if err := RegisterSchema(seed.name, seed.schema); err != nil {
			return err
		}
		if seed.proseTolerant {
			if err := MarkSchemaProseTolerant(seed.name); err != nil {
				return err
			}
		}
	}
	return nil
}
