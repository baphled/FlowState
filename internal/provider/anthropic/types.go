package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"time"
)

// DefaultBin is the executable name resolved on PATH when no binary is configured.
const DefaultBin = "claude"

// WorkDirPrefix is the base-name prefix of every private working directory
// the default factory creates; only such directories are ever removed.
const WorkDirPrefix = "flowstate-claude-"

// SystemPromptFile is the name, inside the request's working directory, of the
// file that carries a non-empty system prompt.
const SystemPromptFile = "system-prompt.txt"

// DefaultModel is the Claude Code alias used when a request names no model.
const DefaultModel = "sonnet"

const (
	// MaxLineBytes is the default cap on one stdout line (16 MiB).
	MaxLineBytes = 16 << 20
	// MaxSystemPromptBytes caps the system prompt (256 KiB). It travels in a
	// file inside the request's private working directory, not in argv, so
	// the command-line length limit does not apply.
	MaxSystemPromptBytes = 256 << 10
	// DefaultResultGrace is how long a child may linger after its result line before it is killed.
	DefaultResultGrace = 5 * time.Second
)

// Command describes one headless Claude Code invocation.
type Command struct {
	// Bin is the executable name or path.
	Bin string
	// Args is the complete argument vector, excluding Bin. It never carries the prompt.
	Args []string
	// Dir is the process working directory.
	Dir string
	// Env is the complete child environment: the caller's environment without
	// the variables that would redirect Claude Code away from the user's sign-in.
	Env []string
	// Stdin is the full prompt text written to the process's standard input.
	Stdin string
}

// Process is one started Claude Code invocation.
type Process interface {
	// Stdout is the line-delimited stream-json output. Close must unblock a
	// pending read: the provider closes it when the turn is cancelled. The
	// reader must tolerate Close being called more than once, concurrently
	// with Read, and after Wait.
	Stdout() io.ReadCloser
	// Stderr returns a bounded tail of the process's standard error; call after Wait.
	Stderr() string
	// Wait reaps the process and reports a non-zero exit as an error.
	Wait() error
}

// Launcher starts a Command. Implementations must kill the process when ctx is cancelled.
type Launcher func(ctx context.Context, cmd Command) (Process, error)

// Option configures a Subscription.
type Option func(*Subscription)

// WorkDirFactory creates a fresh private working directory for one request. It
// must return an absolute, clean path. The caller owns the directory: the
// provider never removes one a factory supplied.
type WorkDirFactory func() (string, error)

// Subscription streams chat turns through the user's own unmodified Claude Code binary.
type Subscription struct {
	bin     string
	launch  Launcher
	workDir WorkDirFactory // nil selects the default factory
	grace   time.Duration
	maxLine int
}

// wireLine is the subset of a Claude Code stream-json line the adapter reads.
type wireLine struct {
	Type       string       `json:"type"`
	Event      *wireEvent   `json:"event"`
	Message    *wireMessage `json:"message"`
	Error      string       `json:"error"`
	Subtype    string       `json:"subtype"`
	IsError    bool         `json:"is_error"`
	Result     string       `json:"result"`
	StopReason string       `json:"stop_reason"`
	// APIErrorStatus stays raw so a number or a string (or anything else) cannot drop the line.
	APIErrorStatus json.RawMessage `json:"api_error_status"`
}

type wireEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

type wireMessage struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// parser turns Claude Code stream-json lines into provider events. It is
// single-use and not safe for concurrent use.
type parser struct {
	textSeen   bool // a text delta was emitted for the current turn
	authFailed bool // an assistant line reported authentication_failed
	done       bool // a result line was seen
}

// lineReader reads lines of at most limit bytes.
type lineReader struct {
	r     *bufio.Reader
	limit int
}
