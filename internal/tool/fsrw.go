package tool

// Filesystem session tools fs.read / fs.write (M2-P1): relative paths
// confined to a per-session workdir through os.Root (no traversal, absolute
// paths or symlink escapes) with byte-capped transfers.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// FSReadName is the fs.read tool name.
	FSReadName = "fs.read"
	// FSWriteName is the fs.write tool name.
	FSWriteName = "fs.write"
)

// Typed fs error codes surfaced in tool.result events.
const (
	// ErrCodePathOutsideWorkdir maps ErrPathOutsideWorkdir.
	ErrCodePathOutsideWorkdir = "fs_path_outside_workdir"
	// ErrCodeNotFound maps ErrFileNotFound.
	ErrCodeNotFound = "fs_not_found"
	// ErrCodeOversize maps ErrOversize.
	ErrCodeOversize = "fs_oversize"
)

// Byte caps for reads (memory) and writes (pre-touch).
const (
	// DefaultMaxReadBytes caps fs.read at 1 MiB.
	DefaultMaxReadBytes int64 = 1 << 20
	// DefaultMaxWriteBytes caps fs.write at 1 MiB.
	DefaultMaxWriteBytes int64 = 1 << 20
)

// Typed fs tool errors, mapped to ErrCode* by errorCodeFor.
var (
	// ErrPathOutsideWorkdir reports escape from the workdir.
	ErrPathOutsideWorkdir = errors.New("tool: path outside session workdir")
	// ErrFileNotFound reports a missing read target.
	ErrFileNotFound = errors.New("tool: file not found")
	// ErrOversize reports a transfer over the cap.
	ErrOversize = errors.New("tool: content exceeds size cap")
)

// errorCodeFor maps a tool error to its tool.result error.code;
// unmapped errors keep the generic tool_error code.
func errorCodeFor(err error) string {
	switch {
	case errors.Is(err, ErrPathOutsideWorkdir):
		return ErrCodePathOutsideWorkdir
	case errors.Is(err, ErrFileNotFound):
		return ErrCodeNotFound
	case errors.Is(err, ErrOversize):
		return ErrCodeOversize
	case errors.Is(err, ErrOldTextNotUnique):
		return ErrCodeOldTextNotUnique
	case errors.Is(err, ErrOldTextNotFound):
		return ErrCodeOldTextNotFound
	case errors.Is(err, ErrExecNotEnabled):
		return ErrCodeExecNotEnabled
	case errors.Is(err, ErrExecNotAllowed):
		return ErrCodeExecNotAllowed
	case errors.Is(err, ErrExecDeniedSubcommand):
		return ErrCodeExecDeniedSubcommand
	case errors.Is(err, ErrExecTimeout):
		return ErrCodeExecTimeout
	case errors.Is(err, ErrExecOversize):
		return ErrCodeExecOversize
	default:
		return "tool_error"
	}
}

// WithMaxReadBytes / WithMaxWriteBytes override the byte caps.
func WithMaxReadBytes(n int64) FSOption { return func(o *fsOptions) { o.maxRead = n } }

// WithMaxWriteBytes overrides the write byte cap.
func WithMaxWriteBytes(n int64) FSOption { return func(o *fsOptions) { o.maxWrite = n } }

// rootEscapeMessage is the text os.Root puts in the error for a path (or a
// symlink met on the way) that leaves the root. The standard library keeps the
// sentinel unexported, so it is captured once from a probe (rootEscapeSentinel);
// the message is matched only if that capture fails. Specs pin both.
const rootEscapeMessage = "path escapes from parent"

var (
	rootEscapeOnce sync.Once
	rootEscapeErr  error
)

// rootEscapeSentinel returns the os.Root escape sentinel, taken from the error
// of a ".." probe on a root over the temp directory, or nil if the probe fails.
func rootEscapeSentinel() error {
	rootEscapeOnce.Do(func() {
		root, err := os.OpenRoot(os.TempDir())
		if err != nil {
			return
		}
		defer func() { _ = root.Close() }()
		_, perr := root.Stat("..")
		rootEscapeErr = errors.Unwrap(perr)
	})
	return rootEscapeErr
}

// isRootEscape reports whether err is an os.Root escape error.
func isRootEscape(err error) bool {
	if sentinel := rootEscapeSentinel(); sentinel != nil {
		return errors.Is(err, sentinel)
	}
	return err != nil && strings.Contains(err.Error(), rootEscapeMessage)
}

// openConfined opens an os.Root on the (absolute) workdir for one invocation.
// Empty, absolute or missing-workdir input is ErrPathOutsideWorkdir. The caller
// must Close the root. Every later operation resolves symlinks and ".." inside
// the root physically, so no lexical check can be bypassed.
func openConfined(workdir, rel string) (*os.Root, error) {
	if rel == "" || filepath.IsAbs(rel) || workdir == "" {
		return nil, ErrPathOutsideWorkdir
	}
	// A relative workdir (`--workdir .`) is anchored to the current directory.
	abs, err := filepath.Abs(workdir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrPathOutsideWorkdir
	}
	return root, err
}

// mapRootErr turns an os.Root escape error into ErrPathOutsideWorkdir and
// leaves every other error unchanged.
func mapRootErr(err error) error {
	if isRootEscape(err) {
		return fmt.Errorf("%w: %v", ErrPathOutsideWorkdir, err)
	}
	return err
}

// parentOf returns rel without its last element, WITHOUT lexically cleaning it:
// filepath.Dir would fold "link/../x" to "." although ".." follows the link's
// physical directory, so the root must see the dots.
func parentOf(rel string) string {
	slashed := strings.TrimRight(filepath.ToSlash(rel), "/")
	i := strings.LastIndex(slashed, "/")
	if i < 0 {
		return "."
	}
	return slashed[:i]
}

// readBounded reads rel through root: missing is ErrFileNotFound, a file over
// max bytes is ErrOversize.
func readBounded(root *os.Root, rel string, limit int64) ([]byte, error) {
	info, err := root.Stat(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrFileNotFound, rel)
		}
		return nil, mapRootErr(err)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%w: %s is %d bytes (cap %d)", ErrOversize, rel, info.Size(), limit)
	}
	b, err := root.ReadFile(rel)
	return b, mapRootErr(err)
}

// NewFSRead returns fs.read rooted at workdir; cap defaults to
// DefaultMaxReadBytes (override: WithMaxReadBytes).
func NewFSRead(workdir string, opts ...FSOption) Tool {
	o := fsOptions{maxRead: DefaultMaxReadBytes}
	for _, opt := range opts {
		opt(&o)
	}
	return &fsReadTool{workdir: workdir, max: o.maxRead}
}

// Name implements Tool for fs.read.
func (t *fsReadTool) Name() string { return FSReadName }

// Invoke reads args {path} and returns {path,content,bytes}. Missing ->
// ErrFileNotFound, big -> ErrOversize.
func (t *fsReadTool) Invoke(_ context.Context, args json.RawMessage) (any, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("tool: fs.read args: %w", err)
	}
	root, err := openConfined(t.workdir, a.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	b, err := readBounded(root, a.Path, t.max)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": a.Path, "content": string(b), "bytes": len(b)}, nil
}

// NewFSWrite returns fs.write rooted at workdir; cap defaults to
// DefaultMaxWriteBytes (override: WithMaxWriteBytes).
func NewFSWrite(workdir string, opts ...FSOption) Tool {
	o := fsOptions{maxWrite: DefaultMaxWriteBytes}
	for _, opt := range opts {
		opt(&o)
	}
	return &fsWriteTool{workdir: workdir, max: o.maxWrite}
}

// Name implements Tool for fs.write.
func (t *fsWriteTool) Name() string { return FSWriteName }

// Invoke reads args {path,content} and returns {path,bytes}. Oversize is
// rejected before any file is touched.
func (t *fsWriteTool) Invoke(_ context.Context, args json.RawMessage) (any, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("tool: fs.write args: %w", err)
	}
	if int64(len(a.Content)) > t.max {
		return nil, fmt.Errorf("%w: %s is %d bytes (cap %d)", ErrOversize, a.Path, len(a.Content), t.max)
	}
	root, err := openConfined(t.workdir, a.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(parentOf(a.Path), 0o750); err != nil {
		return nil, mapRootErr(err)
	}
	if err := root.WriteFile(a.Path, []byte(a.Content), 0o600); err != nil {
		return nil, mapRootErr(err)
	}
	return map[string]any{"path": a.Path, "bytes": len(a.Content)}, nil
}
