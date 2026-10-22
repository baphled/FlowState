// Package lifecycle provides turn lifecycle stage execution scaffolding.
//
// This package introduces the turn lifecycle abstraction, which enables pluggable
// middleware stages around the core turn execution path (context_assembly, pre_stream,
// stream, pre_tool_exec, tool_exec, post_process). Each stage operates on a typed
// context value and supports hook chains for before/after behaviour, with error-based
// short-circuit support for gates and validation hooks. The lifecycle package is used
// by the engine to enforce cross-cutting concerns like token budget checks, pre-stream
// validation, and post-processing.
package lifecycle
