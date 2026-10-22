package tool

import "errors"

// ErrToolNotFound is the canonical sentinel for "the engine cannot
// dispatch the requested tool for the active agent". It covers two
// shapes the engine treats uniformly:
//
//   - Registry absence: the tool name is not registered on the engine
//     at all (the fuzzy-suggest fallback at the dispatch site renders
//     a "Did you mean" hint and a list of available tools).
//   - Out-of-manifest: the tool name IS registered but is NOT in the
//     active manifest's effective allowed set (Option A, May 2026 —
//     the runtime gate at executeToolCall returns a "not available to
//     agent" rejection citing the agent identity and the effective
//     toolset).
//
// Callers use errors.Is to distinguish "unknown tool" from a tool
// execution failure. The shared sentinel mirrors the OpenAI Agents
// SDK's ToolNotFoundBehavior="return_error_to_model" pattern: the
// model receives a structured tool_result and self-corrects on the
// next turn. Pre-Option-A the engine carried a separate
// ErrToolNotAllowed sentinel for the out-of-manifest case; the
// distinction was internal-only — the structured tool_result shape was
// identical and no caller switched on which sentinel fired. Retiring
// it keeps the public API minimal.
var ErrToolNotFound = errors.New("tool not found")
