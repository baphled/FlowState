package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
	"sync"
)

// DefaultBin is the executable name resolved on PATH when no binary is configured.
const DefaultBin = "claude"

// DefaultModel is the Claude Code alias used when a request names no model.
const DefaultModel = "sonnet"

// Command describes one headless Claude Code invocation.
type Command struct {
	// Bin is the executable name or path.
	Bin string
	// Args is the complete argument vector, excluding Bin. It never carries the prompt.
	Args []string
	// Dir is the process working directory.
	Dir string
	// Stdin is the full prompt text written to the process's standard input.
	Stdin string
}

// Process is one started Claude Code invocation.
type Process interface {
	// Stdout is the line-delimited stream-json output.
	Stdout() io.Reader
	// Stderr returns a bounded tail of the process's standard error; call after Wait.
	Stderr() string
	// Wait reaps the process and reports a non-zero exit as an error.
	Wait() error
}

// Launcher starts a Command. Implementations must kill the process when ctx is cancelled.
// A missing executable must be reported as an error wrapping ErrBinaryNotFound.
type Launcher func(ctx context.Context, cmd Command) (Process, error)

// Option configures a Provider.
type Option func(*Provider)

// Provider streams chat turns through the user's own unmodified Claude Code binary.
type Provider struct {
	bin    string
	launch Launcher
}

// wireLine is the subset of a Claude Code stream-json line the adapter reads.
type wireLine struct {
	Type           string       `json:"type"`
	Event          *wireEvent   `json:"event"`
	Message        *wireMessage `json:"message"`
	Error          string       `json:"error"`
	IsError        bool         `json:"is_error"`
	Result         string       `json:"result"`
	StopReason     string       `json:"stop_reason"`
	APIErrorStatus int          `json:"api_error_status"`
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

// lineReader reads arbitrarily long lines without a scanner token limit.
type lineReader struct{ r *bufio.Reader }

// execProcess is the Process behind the production launcher.
type execProcess struct {
	cmd    *exec.Cmd
	out    io.Reader
	errBuf *tailBuffer
}

// tailBuffer keeps only the last stderrTailLimit bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}
