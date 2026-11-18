package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Typed errors for the tool execution loop. The gate is deny-by-default:
// any tool not on the session allowlist is rejected with ErrDenied.
var (
	// ErrUnknownTool is returned when the named tool is not registered.
	ErrUnknownTool = errors.New("tool: unknown tool")

	// ErrDenied is returned when the tool is not on the session
	// allowlist (M1-TOOLS allowlist gate, deny-by-default).
	ErrDenied = errors.New("tool: tool not on session allowlist")

	// ErrEmptyName rejects invocations without a tool name.
	ErrEmptyName = errors.New("tool: tool name must not be empty")

	// ErrToolExists is returned when registering a duplicate tool name.
	ErrToolExists = errors.New("tool: tool already registered")

	// ErrNilTool rejects nil tool implementations.
	ErrNilTool = errors.New("tool: tool must not be nil")
)

// Tool is the provider-agnostic tool port. Implementations execute one
// named capability and return a JSON-serialisable result; errors are
// wrapped by the loop into a tool.result error event.
type Tool interface {
	// Name is the stable tool identifier used in events and allowlists.
	Name() string
	// Invoke executes the tool with raw JSON arguments.
	Invoke(ctx context.Context, args json.RawMessage) (any, error)
}

// Registry holds the known tool implementations for one composition.
// It is the engine-side catalogue; admission is decided by the Loop's
// allowlist, not by registry membership.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry returns an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool. A nil tool or a duplicate name is rejected.
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return ErrNilTool
	}
	name := t.Name()
	if name == "" {
		return ErrEmptyName
	}
	if _, dup := r.tools[name]; dup {
		return fmt.Errorf("%w: %s", ErrToolExists, name)
	}
	r.tools[name] = t
	return nil
}

// Get returns the tool with the given name, or ErrUnknownTool.
func (r *Registry) Get(name string) (Tool, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, name)
	}
	return t, nil
}

// Names lists registered tool names in sorted order.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
