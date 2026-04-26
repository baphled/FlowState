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
	}
	for _, seed := range seeds {
		if err := RegisterSchema(seed.name, seed.schema); err != nil {
			return err
		}
	}
	return nil
}
