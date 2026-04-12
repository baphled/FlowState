package tool

import "errors"

// ErrToolNotFound is returned when a tool lookup fails because the
// requested tool name is not registered. Callers can use errors.Is to
// distinguish "unknown tool" from a tool execution failure.
var ErrToolNotFound = errors.New("tool not found")
