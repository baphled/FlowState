package claudecode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"strings"
	"time"
)

// waitDelay bounds how long Wait lingers on pipes held by orphaned children after a kill.
const waitDelay = 5 * time.Second

// execLauncher is the production Launcher: it runs the user's own claude
// binary with the environment inherited unchanged.
func execLauncher(ctx context.Context, c Command) (Process, error) {
	cmd := exec.CommandContext(ctx, c.Bin, c.Args...)
	cmd.Dir = c.Dir
	cmd.Stdin = strings.NewReader(c.Stdin)
	cmd.WaitDelay = waitDelay
	errBuf := &tailBuffer{}
	cmd.Stderr = errBuf
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude-code: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %q (install Claude Code, run `claude` and sign in with /login, "+
				"or set FLOWSTATE_CLAUDE_BIN): %w", ErrBinaryNotFound, c.Bin, err)
		}
		return nil, fmt.Errorf("claude-code: start %q: %w", c.Bin, err)
	}
	return &execProcess{cmd: cmd, out: out, errBuf: errBuf}, nil
}

// Stdout implements Process.
func (p *execProcess) Stdout() io.Reader { return p.out }

// Stderr implements Process.
func (p *execProcess) Stderr() string { return p.errBuf.String() }

// Wait implements Process.
func (p *execProcess) Wait() error { return p.cmd.Wait() }

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
