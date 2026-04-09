// Package memory provides a knowledge graph MCP server for AI agent memory.
//
// This package implements a mem0-compatible knowledge graph with entities,
// relations, and observations. It is designed to be extractable as a
// standalone module — it imports NO internal FlowState packages.
//
// Entity, Relation, and ObservationTag Conventions:
//
// Entity Types (exactly 4 allowed):
//   - Agent: Represents an autonomous actor (AI agent, human, etc)
//   - Project: Represents a project, product, or initiative
//   - Concept: Represents an abstract idea, pattern, or principle
//   - Tool: Represents a concrete tool, library, or technology
//
// Relation Types (exactly 5 allowed):
//   - uses: Source entity uses the target entity
//   - implements: Source entity implements the target entity
//   - related_to: Source and target entities are related
//   - depends_on: Source entity depends on the target entity
//   - created_by: Source entity was created by the target entity
//
// Observation Tags (exactly 6 allowed):
//   - DISCOVERY: New knowledge or finding
//   - CHANGE: Modification or update
//   - IMPLICATION: Consequence or effect
//   - BEHAVIOR: Observed behaviour or property
//   - CAPABILITY: Ability or supported feature
//   - LIMITATION: Known constraint or shortcoming
//
// Validation:
//   - Only the above entity types, relation types, and observation tags are accepted.
//   - Validation functions are provided in entity_conventions.go.
//   - Any other value is rejected as invalid.
//
// See entity_conventions.go for implementation and validation logic.
package memory
