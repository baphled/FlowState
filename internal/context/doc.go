// Package context provides conversation context management and session persistence.
//
// This package implements:
//   - Context stores for managing conversation history
//   - Token budget tracking and window management
//   - Session persistence and loading
//   - Context query tools for searching and summarising history
//
// The context system supports semantic search over conversation history
// using embeddings, enabling efficient retrieval of relevant context
// within token budget constraints.
package context
