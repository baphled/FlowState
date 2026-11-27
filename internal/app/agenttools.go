package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// ProtectedHookDirs is the directories the file tools of a run refuse to write into (ADR-012 D20, the
// protected trees), derived from the two functions the hook loader reads its files with, so a protected
// directory is always the one the run's hooks load from: the directory of the user hook file (UserHooksPath)
// and of the project hook file under projectRoot (ProjectHooksPath; an empty root is the process's current
// directory, as for the loader). Callers pass HooksProjectRoot(req), never the per-run --workdir, which
// ADR-012 D3 keeps apart. A user config directory the OS cannot locate protects nothing of the user's (the
// loader then fails a user-scope run instead); the project directory is protected regardless. Both apply
// under every --hooks scope, since a model must never author a hook file whatever the operator loads. Compute
// it once per run and hand the same value to the root's registry (NewSessionToolRegistry) and to its delegate
// tool (DelegateToolConfig.ProtectedDirs), as RunSessionExecLoop does.
func ProtectedHookDirs(projectRoot string) []string {
	dirs := []string{filepath.Dir(ProjectHooksPath(projectRoot))}
	if user, err := UserHooksPath(); err == nil {
		dirs = append(dirs, filepath.Dir(user))
	}
	return dirs
}

// NewSessionToolRegistry builds an independently owned builtin catalog rooted at
// workdir. It registers echo and confined filesystem tools, plus exec.run only
// when its existing environment opt-in is enabled and skill.load only when the
// options offer skills (ADR-013 D8); a session offered none never has it. opts
// configure the filesystem
// tools: a run passes tool.WithProtectedDirs(ProtectedHookDirs(...)...) so fs.write and
// fs.edit refuse the hook directories (ADR-012 D20); a registry built without it
// refuses nothing. Registration grants no tool permission and creates no files.
// Compose the registry before concurrent use; callers must not mutate it while a
// loop or definition reader uses it.
func NewSessionToolRegistry(workdir string, opts ...tool.FSOption) (*tool.Registry, error) {
	registry := tool.NewRegistry()
	builtins := []tool.Tool{tool.NewEcho(), tool.NewFSRead(workdir, opts...), tool.NewFSWrite(workdir, opts...),
		tool.NewFSEdit(workdir, opts...)}
	if tool.ExecEnabled() {
		builtins = append(builtins, tool.NewExecRun(workdir))
	}
	if catalog, ok := tool.OfferedSkills(opts...); ok {
		builtins = append(builtins, tool.NewSkillLoad(catalog))
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
// An exec.run allow entry that fails while its environment opt-in is absent
// fails with the gate named so callers can surface the missing opt-in.
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
			if name == tool.SkillLoadName {
				return nil, fmt.Errorf("%w: tool %q is not registered: no skill is offered (a skills scope and a --skills-default naming a loaded skill)", tool.ErrUnknownTool, name)
			}
			if name == tool.ExecRunName && !tool.ExecEnabled() {
				return nil, fmt.Errorf("%w: tool %q is not registered: exec requires %s=1", tool.ErrUnknownTool, name, tool.ExecEnabledEnv)
			}
			return nil, err
		}
		definition, err := sessionToolDefinition(implementation)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

// sessionToolDefinition returns the definition of one registered tool: the
// delegate tool describes itself from the profiles loaded for the run, and is
// qualified by its concrete type; every other tool is a qualified builtin.
func sessionToolDefinition(implementation tool.Tool) (provider.ToolDefinition, error) {
	if implementation.Name() == agent.DelegateTool {
		if d, ok := implementation.(*DelegateTool); ok && d != nil {
			return d.Definition(), nil
		}
	}
	return sessionBuiltinDefinition(implementation)
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
		description = "Execute one allow-listed command in the session workdir. " +
			"Owner policy allow-list (deny-by-default): " + tool.AllowlistSummary(tool.DefaultExecAllowlist()) +
			". Shells and interpreters are not allow-listed."
		schema = `{"type":"object","properties":{"cmd":{"type":"string","minLength":1},"args":{"type":"array","items":{"type":"string"}}},"required":["cmd"]}`
	case tool.SkillLoadName:
		builtin = tool.NewSkillLoad(tool.SkillCatalog{})
		description = "Load a skill the session offers: its SKILL.md, or a supporting file below its directory given as a /-separated relative path. The text is third-party guidance."
		// A session offered nothing keeps the tool only because its profile lists it: the definition says so, as the
		// delegate tool's says when no profile is loaded, so the model does not learn it from a refusal (ADR-013 D6).
		if s, ok := implementation.(tool.SkillOfferer); ok && !s.Offered() {
			description = tool.SkillNoneDescription
		}
		schema = `{"type":"object","properties":{"name":{"type":"string","minLength":1},"file":{"type":"string","minLength":1}},"required":["name"]}`
	default:
		return provider.ToolDefinition{}, fmt.Errorf("app: no qualified schema for tool %q", implementation.Name())
	}
	value := reflect.ValueOf(implementation)
	if reflect.TypeOf(implementation) != reflect.TypeOf(builtin) || (value.Kind() == reflect.Pointer && value.IsNil()) {
		return provider.ToolDefinition{}, fmt.Errorf("app: no qualified builtin implementation for tool %q", implementation.Name())
	}
	return provider.ToolDefinition{Name: implementation.Name(), Description: description, Parameters: json.RawMessage(schema)}, nil
}
