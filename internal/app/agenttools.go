package app

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// NewSessionToolRegistry builds an independently owned builtin catalog rooted at
// workdir. It registers echo and confined filesystem tools, plus exec.run only
// when its existing environment opt-in is enabled. Registration grants no tool
// permission and creates no files. Compose the registry before concurrent use;
// callers must not mutate it while a loop or definition reader uses it.
func NewSessionToolRegistry(workdir string) (*tool.Registry, error) {
	registry := tool.NewRegistry()
	builtins := []tool.Tool{tool.NewEcho(), tool.NewFSRead(workdir), tool.NewFSWrite(workdir), tool.NewFSEdit(workdir)}
	if tool.ExecEnabled() {
		builtins = append(builtins, tool.NewExecRun(workdir))
	}
	for _, implementation := range builtins {
		if err := registry.Register(implementation); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// SessionToolDefinitions returns fresh provider-neutral metadata for exactly the
// explicit allowlist, preserving its order. An empty list advertises nothing.
// Missing registries, unknown or duplicate names, and custom implementations
// without a qualified builtin schema fail with no partial result. This function
// never registers tools or grants permissions. Schemas describe object arguments;
// the execution loop and tools retain permission, argument and resource checks.
// Callers own the returned JSON bytes; registry mutation must not race this read.
func SessionToolDefinitions(registry *tool.Registry, allow []string) ([]provider.ToolDefinition, error) {
	if registry == nil {
		return nil, fmt.Errorf("app: tool definitions require a registry")
	}
	definitions := make([]provider.ToolDefinition, 0, len(allow))
	seen := make(map[string]bool, len(allow))
	for _, name := range allow {
		if seen[name] {
			return nil, fmt.Errorf("app: duplicate tool definition %q", name)
		}
		seen[name] = true
		implementation, err := registry.Get(name)
		if err != nil {
			return nil, err
		}
		definition, err := sessionBuiltinDefinition(implementation)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

// sessionBuiltinDefinition qualifies both name and implementation: a custom tool
// cannot acquire a builtin's schema by reusing its name. Literal schemas track
// the builtin JSON decoders, including optional zero-value fields.
func sessionBuiltinDefinition(implementation tool.Tool) (provider.ToolDefinition, error) {
	var builtin tool.Tool
	var description, schema string
	switch implementation.Name() {
	case tool.Echo:
		builtin = tool.NewEcho()
		description = "Return the supplied argument object."
		schema = `{"type":"object","additionalProperties":true}`
	case tool.FSReadName:
		builtin = tool.NewFSRead("")
		description = "Read a bounded file at a path relative to the session workdir."
		schema = `{"type":"object","properties":{"path":{"type":"string","minLength":1}},"required":["path"]}`
	case tool.FSWriteName:
		builtin = tool.NewFSWrite("")
		description = "Write bounded content to a path relative to the session workdir; omitted content writes an empty file."
		schema = `{"type":"object","properties":{"path":{"type":"string","minLength":1},"content":{"type":"string","default":""}},"required":["path"]}`
	case tool.FSEditName:
		builtin = tool.NewFSEdit("")
		description = "Replace unique old_text in a confined file, or all matches when all is true; omitted new_text deletes the match."
		schema = `{"type":"object","properties":{"path":{"type":"string","minLength":1},"old_text":{"type":"string","minLength":1},"new_text":{"type":"string","default":""},"all":{"type":"boolean","default":false}},"required":["path","old_text"]}`
	case tool.ExecRunName:
		builtin = tool.NewExecRun("")
		description = "Run an allowed binary and argument vector in the session workdir under execution policy and environment opt-in."
		schema = `{"type":"object","properties":{"cmd":{"type":"string","minLength":1},"args":{"type":"array","items":{"type":"string"}}},"required":["cmd"]}`
	default:
		return provider.ToolDefinition{}, fmt.Errorf("app: no qualified schema for tool %q", implementation.Name())
	}
	value := reflect.ValueOf(implementation)
	if reflect.TypeOf(implementation) != reflect.TypeOf(builtin) || (value.Kind() == reflect.Pointer && value.IsNil()) {
		return provider.ToolDefinition{}, fmt.Errorf("app: no qualified builtin implementation for tool %q", implementation.Name())
	}
	return provider.ToolDefinition{Name: implementation.Name(), Description: description, Parameters: json.RawMessage(schema)}, nil
}
