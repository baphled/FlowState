package swarm

import (
	"github.com/google/jsonschema-go/jsonschema"
)

// ReviewVerdictV1Name is the SchemaRef the planning-loop swarm
// references on its post-member gate (see internal/app/swarms/
// planning-loop.yml). Pinned as a constant so the seed registration
// and any test that re-registers an alternative shape stay aligned.
const ReviewVerdictV1Name = "review-verdict-v1"

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

// SeedDefaultSchemas registers every Phase 1 builtin schema with the
// in-process registry. The CLI / app construction calls this once at
// startup so the planning-loop swarm's post-member gate has a
// schema to look up.
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
	return RegisterSchema(ReviewVerdictV1Name, ReviewVerdictV1Schema())
}
