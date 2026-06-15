// Package todo provides a session-scoped tool for managing todo lists.
//
// This package implements the todowrite and todo_update tools for FlowState,
// which allow agents to maintain a per-session task list during a conversation.
// Responsibilities include:
//   - Defining the Item data model with content, status, and priority fields
//   - Providing a Store interface for reading and writing per-session item lists
//   - MemoryStore: an in-process, volatile implementation used in tests and as a fallback
//   - FileStore: a JSON-on-disk implementation that survives process restarts;
//     each session's list is written atomically via a tmp-file rename to
//     <dataDir>/todos/<sessionID>.json
//   - Implementing the todowrite and todo_update tools that persist items via
//     the active Store, scoped by session ID
package todo
