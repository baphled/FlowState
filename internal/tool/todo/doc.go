// Package todo provides a session-scoped tool for managing todo lists.
//
// This package implements the todowrite tool for FlowState, which allows agents
// to maintain a per-session task list during a conversation. Responsibilities include:
//   - Defining the TodoItem data model with content, status, and priority fields
//   - Providing a Store interface and in-memory MemoryStore implementation
//   - Implementing the Tool that stores and retrieves todos scoped by session ID
package todo
