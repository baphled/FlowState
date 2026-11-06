package tool

// exec.run types (M2-P3). Types live here per the code contract.

import "time"

const (
	// ExecRunName is the name of the session command tool.
	ExecRunName = "exec.run"
	// DefaultExecTimeout is the per-child execution deadline.
	DefaultExecTimeout = 30 * time.Second
	// DefaultMaxExecOutputBytes is the combined stdout/stderr byte cap.
	DefaultMaxExecOutputBytes = 1 << 20
)

// exec tool error codes surfaced in tool.result events.
const (
	// ErrCodeExecNotEnabled maps ErrExecNotEnabled.
	ErrCodeExecNotEnabled = "exec_not_enabled"
	// ErrCodeExecNotAllowed maps ErrExecNotAllowed.
	ErrCodeExecNotAllowed = "exec_not_allowed"
	// ErrCodeExecDeniedSubcommand maps ErrExecDeniedSubcommand.
	ErrCodeExecDeniedSubcommand = "exec_denied_subcommand"
	// ErrCodeExecTimeout maps ErrExecTimeout.
	ErrCodeExecTimeout = "exec_timeout"
	// ErrCodeExecOversize maps ErrExecOversize.
	ErrCodeExecOversize = "exec_oversize"
)

// ExecBinary is one allow-listed binary: an absolute resolved path
// and its deny-by-default subcommand set. A nil/empty Subcommands
// list permits only the bare invocation with no arguments.
type ExecBinary struct {
	Path        string
	Subcommands []string
}

// execOptions carries the overridable exec knobs.
type execOptions struct {
	binaries  map[string]ExecBinary
	timeout   time.Duration
	maxOutput int64
}

// ExecOption customises the exec tool constructor.
type ExecOption func(*execOptions)

// execTool runs allow-listed binaries in the session workdir.
type execTool struct {
	workdir  string
	binaries map[string]ExecBinary
	timeout  time.Duration
	maxOut   int64
}

// execArgs is the exec.run argument payload: cmd selects the binary
// and args the (subcommand-checked) argument vector. There is
// deliberately no cwd field: the child always runs in the session
// workdir (Principal review).
type execArgs struct {
	Cmd  string   `json:"cmd"`
	Args []string `json:"args"`
}
