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
// Phase 1 shape:
//
//   - object root with optional `markdown` and `plan` strings
//     (agent prompts inconsistently use either key); a stricter
//     rev can enforce mutually-exclusive presence once the
//     writer's contract is firmer.
//   - additional metadata strings (id / title / status) accepted
//     but not required.
//
// The choice to leave required minimal is intentional: today's
// plan-writer prompt does not pin a single key name. The wrapper
// itself is the load-bearing invariant — a bare markdown string
// written directly to the coord-store fails JSON decoding before
// the schema runs, so this layer just confirms the wrapper.
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
	}
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

// SeedDefaultSchemas registers every Phase 1 builtin schema with the
// in-process registry. The CLI / app construction calls this once at
// startup so the planning-loop swarm's post-member gates have schemas
// to look up.
//
// Returns:
//   - nil on success.
//   - The first registration error otherwise. Errors here are
//     programmer mistakes (a malformed seed schema); the caller
//     surfaces them and refuses to start rather than running with a
//     half-seeded registry.
//
// Side effects:
//   - Calls RegisterSchema for each Phase 1 builtin.
func SeedDefaultSchemas() error {
	seeds := []struct {
		name   string
		schema *jsonschema.Schema
	}{
		{ReviewVerdictV1Name, ReviewVerdictV1Schema()},
		{EvidenceBundleV1Name, EvidenceBundleV1Schema()},
		{ExternalRefsV1Name, ExternalRefsV1Schema()},
		{AnalysisBundleV1Name, AnalysisBundleV1Schema()},
		{PlanDocumentV1Name, PlanDocumentV1Schema()},
		{CodeReviewVerdictV1Name, CodeReviewVerdictV1Schema()},
	}
	for _, seed := range seeds {
		if err := RegisterSchema(seed.name, seed.schema); err != nil {
			return err
		}
	}
	return nil
}
