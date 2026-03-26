// Package coordination provides a key-value store for sharing context between
// agents during delegation chains.
//
// This package handles the storage and retrieval of cross-agent context,
// including:
//   - Requirements captured during the planning phase
//   - Interview transcripts from the analysis phase
//   - Codebase findings from the exploration phase
//   - External references from the research phase
//   - Analysis evidence from the synthesis phase
//   - Generated plans from the writing phase
//   - Review verdicts from the evaluation phase
//
// The Store interface defines the contract for persistence, while FileStore
// provides a JSON-based implementation that persists to the XDG data directory.
package coordination
