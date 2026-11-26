package app

import (
	"encoding/json"

	"github.com/baphled/flowstate/internal/tool"
)

// HookPoint names one lifecycle point of ADR-012's closed, versioned list.
type HookPoint string

// The version-1 hook points. A point is fireable only when the loader's
// accepted set names it (see AcceptedHooks).
const (
	// HookSessionStart fires once per root run, before the first provider request.
	HookSessionStart HookPoint = "session.start"
	// HookUserPrompt fires once per root run with the run's task.
	HookUserPrompt HookPoint = "user.prompt"
	// HookToolBefore fires per tool call, after the allowlist gate.
	HookToolBefore HookPoint = "tool.before"
	// HookToolAfter fires per executed tool call, after the tool returned.
	HookToolAfter HookPoint = "tool.after"
	// HookTurnAfter fires after each provider turn and its tool results.
	HookTurnAfter HookPoint = "turn.after"
	// HookCompactionBefore fires before an automatic compaction.
	HookCompactionBefore HookPoint = "compaction.before"
	// HookCompactionAfter fires after context.compacted is appended.
	HookCompactionAfter HookPoint = "compaction.after"
	// HookSessionEnd fires once per run that fired session.start.
	HookSessionEnd HookPoint = "session.end"
)

// HookKind is a handler kind: an argv command or a System One decision.
type HookKind string

// The handler kinds a hook file may name. Only command loads in this build.
const (
	// HookKindCommand runs an argv, never a shell.
	HookKindCommand HookKind = "command"
	// HookKindDecision consults System One and maps its answer through rules.
	HookKindDecision HookKind = "decision"
)

// HookScope is the --hooks value: which hook files and profile hooks apply.
type HookScope string

// The --hooks scopes; HookScopeUser is the default.
const (
	// HookScopeUser loads the user file and user-profile hooks.
	HookScopeUser HookScope = "user"
	// HookScopeProject loads the project file and project-profile hooks.
	HookScopeProject HookScope = "project"
	// HookScopeBoth loads both files and every profile's hooks.
	HookScopeBoth HookScope = "both"
	// HookScopeNone loads nothing.
	HookScopeNone HookScope = "none"
)

// HookSource says where a hook, or the profile that carries it, came from.
type HookSource string

// The hook and profile sources.
const (
	// HookSourceUser is the operator's own configuration directory.
	HookSourceUser HookSource = "user"
	// HookSourceProject is repository-controlled content.
	HookSourceProject HookSource = "project"
)

// Hook failure policies for on_failure.
const (
	// HookOnFailureDeny makes a hook failure a deny.
	HookOnFailureDeny = "deny"
	// HookOnFailureAllow makes a hook failure advisory.
	HookOnFailureAllow = "allow"
)

// Hook file limits and defaults (ADR-012).
const (
	// HooksFileVersion is the only accepted file version.
	HooksFileVersion = 1
	// MaxHooksFileBytes is the largest hook file accepted.
	MaxHooksFileBytes = 256 << 10
	// DefaultHookTimeoutMS applies when a hook omits timeout_ms.
	DefaultHookTimeoutMS = 10_000
	// MaxHookTimeoutMS is the largest hook timeout.
	MaxHookTimeoutMS = 60_000
)

// HookAccepted is the set of points the build can fire, owned by the firing
// code. A zero value accepts nothing, so a loader given no set fails closed.
type HookAccepted struct {
	// Points lists the fireable points.
	Points []HookPoint
	// Transform reports whether "transform": true may load.
	Transform bool
}

// Hook is one validated hook. Origin and Source record where it was written.
type Hook struct {
	// ID is the unique id the log records.
	ID string `json:"id"`
	// Point is the lifecycle point.
	Point HookPoint `json:"point"`
	// Match lists tool-name globs; empty means every tool.
	Match []string `json:"match,omitempty"`
	// Kind is the handler kind.
	Kind HookKind `json:"kind"`
	// Argv is a command hook's argv.
	Argv []string `json:"argv,omitempty"`
	// Executable is the resolved path of a command hook's argv[0].
	Executable string `json:"executable,omitempty"`
	// Env lists the environment names a command hook may receive.
	Env []string `json:"env,omitempty"`
	// TimeoutMS is the hook timeout.
	TimeoutMS int `json:"timeout_ms"`
	// OnFailure is deny or allow.
	OnFailure string `json:"on_failure"`
	// Transform marks a tool.before hook that may rewrite arguments.
	Transform bool `json:"transform,omitempty"`
	// Origin is the file that declared the hook.
	Origin string `json:"-"`
	// Source is the directory class of Origin.
	Source HookSource `json:"-"`
}

// LoadedHooks is the effective hook set of one run: user file, then project
// file, in file order.
type LoadedHooks struct {
	// Scope is the --hooks value that applied.
	Scope HookScope
	// Hooks is the effective set in firing order.
	Hooks []Hook
}

// HooksRequest asks LoadHooks for the effective set.
type HooksRequest struct {
	// Scope is the parsed --hooks value; empty is treated as none.
	Scope HookScope
	// ProjectRoot is where .flowstate/hooks.json is found; empty is the current
	// directory. It is never the per-run --workdir.
	ProjectRoot string
	// Accepted is the fireable set from the firing code.
	Accepted HookAccepted
}

// hookDocument is a hook file as written.
type hookDocument struct {
	Version int             `json:"version"`
	Hooks   json.RawMessage `json:"hooks"`
}

// hookSpec is one hook as written, before defaults and validation.
type hookSpec struct {
	ID        string   `json:"id"`
	Point     string   `json:"point"`
	Match     []string `json:"match"`
	Kind      string   `json:"kind"`
	Argv      []string `json:"argv"`
	Env       []string `json:"env"`
	TimeoutMS *int     `json:"timeout_ms"`
	OnFailure string   `json:"on_failure"`
	Transform bool     `json:"transform"`
}

// hookContext says how one document's hooks are validated.
type hookContext struct {
	accepted HookAccepted
	dir      string
	origin   string
	source   HookSource
}

// hookGate is the tool.before gate of one tool call (ADR-012 packet 3a). It implements tool.CallGate. Its
// events go through the parent writer the loop holds, never the call's correlated sink.
type hookGate struct {
	hooks  []Hook
	events tool.EventSink
	runner tool.HookRunner
	input  hookInput
}

// hookInput is the call context a gate puts in every hook's stdin document.
type hookInput struct {
	session string
	runID   string
	agent   string
	depth   int
	cwd     string
	callID  string
}
