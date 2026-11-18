package tool

import (
	"context"
	"encoding/json"
	"strings"
)

// echo is the M1 reference tool: it returns its arguments back as the
// result, proving the full loop (gate, eventing, propagation) without
// any external side effects.
type echo struct{}

// Echo is the built-in echo tool name.
const Echo = "echo"

// NewEcho returns the echo tool implementation.
func NewEcho() Tool { return echo{} }

// Name implements Tool.
func (echo) Name() string { return Echo }

// Invoke implements Tool: the result is the parsed argument object,
// or an empty object when no arguments are supplied.
func (echo) Invoke(_ context.Context, args json.RawMessage) (any, error) {
	if len(strings.TrimSpace(string(args))) == 0 {
		return map[string]any{}, nil
	}
	var v any
	if err := json.Unmarshal(args, &v); err != nil {
		return nil, err
	}
	return v, nil
}
