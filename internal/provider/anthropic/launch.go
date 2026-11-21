package anthropic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrBinaryNotFound marks a missing Claude Code executable; the launcher wraps it.
var ErrBinaryNotFound = errors.New("anthropic: Claude Code executable not found")

// DefaultWaitDelay bounds how long Wait lingers on pipes held by orphaned
// children (for example one that keeps stderr open) once the child has exited or been killed.
const DefaultWaitDelay = 5 * time.Second

// ExecLauncher is the production Launcher, running the real `claude` process.
// A missing executable is reported as an error wrapping ErrBinaryNotFound.
// Like the exec.run tool it resolves the executable to an absolute path first and builds the
// command directly: no shell is involved, the binary is the user's own
// Claude Code executable, and the argv is assembled by this package from
// fixed flags and guarded values. The environment is inherited minus the
// variables childEnv removes.
// What makes this safe is those argv guards and an operator-controlled binary,
// not the path resolution. The exec.Cmd literal is deliberate: the security
// gate flags exec.Command with variable arguments and takes no waivers, so do
// not simplify it back.
func ExecLauncher(ctx context.Context, c Command) (Process, error) {
	return launch(ctx, c, DefaultWaitDelay)
}

// NewExecLauncher returns ExecLauncher with a different WaitDelay, the bound on
// how long reaping the child waits for a descendant that holds its stderr
// open. A non-positive delay keeps DefaultWaitDelay.
func NewExecLauncher(delay time.Duration) Launcher {
	if delay <= 0 {
		delay = DefaultWaitDelay
	}
	return func(ctx context.Context, c Command) (Process, error) { return launch(ctx, c, delay) }
}

// quoteArg wraps s in double quotes, escaping only embedded quotes, so a path
// with spaces is unambiguous and backslashes (Windows paths) stay as written.
func quoteArg(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

// launch is ExecLauncher with an explicit WaitDelay.
func launch(ctx context.Context, c Command, delay time.Duration) (Process, error) {
	path, err := lookPathAbs(c.Bin)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s (install Claude Code, run `claude` and sign in with /login, "+
				"or set FLOWSTATE_CLAUDE_BIN): %w", ErrBinaryNotFound, quoteArg(c.Bin), err)
		}
		return nil, fmt.Errorf("anthropic: resolve %s: %w", c.Bin, err)
	}
	errBuf := &tailBuffer{}
	env := c.Env
	if env == nil {
		env = os.Environ()
	}
	cmd := &exec.Cmd{Path: path, Args: append([]string{path}, c.Args...), Dir: c.Dir, Env: childEnv(env),
		Stdin: strings.NewReader(c.Stdin), Stderr: errBuf, WaitDelay: delay}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("anthropic: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("anthropic: start %s: %w", c.Bin, err)
	}
	p := &execProcess{cmd: cmd, out: out, errBuf: errBuf, exited: make(chan struct{})}
	go p.killOnCancel(ctx)
	return p, nil
}

// childEnv returns env without the variables that would make Claude Code
// bill an API key or talk to another host instead of using the user's
// subscription sign-in. Each one is a documented Claude Code override:
//   - ANTHROPIC_API_KEY: an API key, used in place of the sign-in (API billing);
//   - ANTHROPIC_AUTH_TOKEN: a bearer token sent instead of the sign-in;
//   - ANTHROPIC_BASE_URL: another host receives the requests;
//   - CLAUDE_CODE_USE_BEDROCK, CLAUDE_CODE_USE_VERTEX, CLAUDE_CODE_USE_FOUNDRY:
//     switch Claude Code to a cloud provider's own credentials and billing.
//
// Everything else is kept, because sign-in depends on it. Names match without
// regard to case, as Windows environment names do. No credential is read.
func childEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !redirectsAuth(name) {
			out = append(out, kv)
		}
	}
	return out
}

// redirectsAuth reports whether name is one of the variables childEnv removes.
func redirectsAuth(name string) bool {
	for _, v := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
		if strings.EqualFold(name, v) {
			return true
		}
	}
	return false
}

// lookPathAbs resolves name to an absolute executable path via PATH lookup.
func lookPathAbs(name string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

// killOnCancel kills the child when the request context ends; it returns
// once the child has been reaped, so it never outlives the process.
func (p *execProcess) killOnCancel(ctx context.Context) {
	select {
	case <-ctx.Done():
		_ = p.cmd.Process.Kill()
	case <-p.exited:
	}
}

// Stdout implements Process.
func (p *execProcess) Stdout() io.ReadCloser { return p.out }

// Stderr implements Process.
func (p *execProcess) Stderr() string { return p.errBuf.String() }

// Wait implements Process: it reaps the child and releases the cancel watcher.
func (p *execProcess) Wait() error {
	err := p.cmd.Wait()
	p.once.Do(func() { close(p.exited) })
	return err
}

// Write implements io.Writer.
func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(b)
	if extra := t.buf.Len() - stderrTailLimit; extra > 0 {
		t.buf.Next(extra)
	}
	return len(b), nil
}

// String returns the retained tail.
func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}
