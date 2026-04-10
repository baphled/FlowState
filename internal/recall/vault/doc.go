// Package vault provides an MCP-backed recall source for FlowState using Obsidian vaults.
//
// Expected:
//   - Implements the recall.Source interface for retrieving observations from an Obsidian vault via MCP.
//   - Integrates with FlowState's recall system, supporting context-aware queries and agent-specific recall.
//   - Handles errors gracefully and returns results in a consistent format.
//
// Returns:
//   - Observations retrieved from the configured vault, mapped to recall.Observation instances.
//   - Errors are handled internally; callers receive empty results on failure.
//
// Side effects:
//   - Issues MCP tool calls to the configured server and vault.
//   - May log errors or warnings if MCP calls fail or return unexpected data.
package vault
