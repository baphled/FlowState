// Package context provides session persistence and context window management for FlowState agents.
//
// This package implements:
//   - Session persistence and loading (FileSessionStore, SessionStore)
//   - Context window assembly within token budget constraints (WindowBuilder)
//   - Token budget tracking (TokenBudget, TokenCounter)
//
// Recall Memory (semantic search, message store, embedding store) has been moved
// to the internal/recall package, which implements the Recall tier of the
// Letta/MemGPT memory taxonomy.
package context
