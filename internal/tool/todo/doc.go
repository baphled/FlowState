// Package todo provides session-scoped tools for managing todo lists.
//
// This package implements four todo tools for FlowState, which allow agents
// to maintain and evolve a per-session task list during a conversation.
// Responsibilities include:
//   - Defining the Item data model with content, status, and priority fields
//   - Providing a Store interface for reading and writing per-session item lists
//   - MemoryStore: an in-process, volatile implementation used in tests and as a fallback
//   - FileStore: a JSON-on-disk implementation that survives process restarts;
//     each session's list is written atomically via a tmp-file rename to
//     <dataDir>/todos/<sessionID>.json
//   - Implementing the todowrite tool (whole-list creation, blocked once a list exists)
//   - Implementing the todo_update tool (single-item patch by index)
//   - Implementing the todo_append tool (add item to end of list)
//   - Implementing the todo_insert tool (insert item at a specific index)
package todo
