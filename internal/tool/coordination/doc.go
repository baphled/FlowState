// Package coordination provides a tool implementing the tool.Tool interface
// for accessing the coordination key-value store during agent execution.
//
// This package handles:
//   - Exposing get, set, list, and delete operations as a single tool.
//   - Translating tool.Input arguments into coordination store operations.
//   - Returning structured tool.Result responses for each operation.
//
// The tool is registered as "coordination_store" and accepts an "operation"
// parameter to select the desired action. It receives a coordination.Store
// via dependency injection in its constructor.
package coordination
