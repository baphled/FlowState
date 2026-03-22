// Package memory provides a knowledge graph MCP server for AI agent memory.
//
// This package implements a mem0-compatible knowledge graph with entities,
// relations, and observations. It is designed to be extractable as a
// standalone module — it imports NO internal FlowState packages.
//
// The package provides:
//   - Entity, Relation, and KnowledgeGraph types
//   - In-memory graph operations (CRUD)
//   - JSONL file persistence
//   - MCP tool handlers for all 9 memory tools
package memory
