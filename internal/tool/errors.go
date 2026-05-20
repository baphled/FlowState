package tool

import "errors"

// ErrToolNotFound is returned when a tool lookup fails because the
// requested tool name is not registered. Callers can use errors.Is to
// distinguish "unknown tool" from a tool execution failure.
var ErrToolNotFound = errors.New("tool not found")

// ErrToolNotAllowed is returned when a tool dispatch is rejected
// because the tool name is registered on the engine but NOT in the
// active manifest's effective toolset. Sibling sentinel to
// ErrToolNotFound: callers use errors.Is to distinguish "agent
// manifest does not permit this tool" from "tool absent from the
// registry" and from generic execution failure.
//
// Surface: PR7 / Coordinator Over-Execution (May 2026). The runtime
// tool gate at executeToolCall emits this sentinel before the tool's
// Execute body runs. Pre-PR7 the engine relied on
// buildAllowedToolSetFor schema-advertisement filtering alone;
// permissive providers (glm-4.x at zai/openzen) emit out-of-schema
// tool calls anyway and were silently executed. The dispatch-time
// gate closes that gap with this sentinel as the failure signal.
var ErrToolNotAllowed = errors.New("tool not allowed by manifest")
