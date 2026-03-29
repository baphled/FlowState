package external

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/baphled/flowstate/internal/plugin/manifest"
)

// PluginProcess represents a running external plugin process.
type PluginProcess struct {
	r    io.ReadCloser
	w    io.WriteCloser
	done <-chan struct{}
	cmd  *exec.Cmd
}

// NewPluginProcess creates a new PluginProcess with the given read/write streams and done channel.
func NewPluginProcess(r io.ReadCloser, w io.WriteCloser, done <-chan struct{}) *PluginProcess {
	return &PluginProcess{
		r:    r,
		w:    w,
		done: done,
	}
}

// Done returns a channel that is closed when the process exits.
func (p *PluginProcess) Done() <-chan struct{} {
	return p.done
}

// Kill terminates the plugin process immediately.
func (p *PluginProcess) Kill() error {
	if rc, ok := p.r.(io.Closer); ok {
		if err := rc.Close(); err != nil {
			slog.Debug("close reader error", "error", err)
		}
	}
	if wc, ok := p.w.(io.Closer); ok {
		if err := wc.Close(); err != nil {
			slog.Debug("close writer error", "error", err)
		}
	}
	return nil
}

// Read implements io.Reader.
func (p *PluginProcess) Read(b []byte) (int, error) {
	return p.r.Read(b)
}

// Write implements io.Writer.
func (p *PluginProcess) Write(b []byte) (int, error) {
	return p.w.Write(b)
}

// Spawner manages the lifecycle of external plugin processes.
type Spawner struct{}

// NewSpawner creates a new Spawner.
func NewSpawner() *Spawner {
	return &Spawner{}
}

// Spawn starts a new plugin process from the given manifest.
func (s *Spawner) Spawn(ctx context.Context, m *manifest.Manifest) (*PluginProcess, error) {
	if m.Command == "" {
		return nil, errors.New("manifest command is empty")
	}
	cmd := exec.CommandContext(ctx, m.Command, m.Args...)
	pr1, pw1 := io.Pipe()
	cmd.Stdin = pr1
	pr2, pw2 := io.Pipe()
	cmd.Stdout = pw2
	cmd.Stderr = os.Stderr

	done := make(chan struct{})

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start process: %w", err)
	}

	go func() {
		if err := cmd.Wait(); err != nil {
			slog.Debug("process wait error", "error", err)
		}
		close(done)
	}()

	proc := &PluginProcess{
		r:    pr2,
		w:    pw1,
		done: done,
		cmd:  cmd,
	}

	return proc, nil
}

// StopProcess stops a running plugin process by sending SIGTERM, waiting, then SIGKILL.
//
// The name parameter is unused but kept for interface compatibility.
func (s *Spawner) StopProcess(_ string, p *PluginProcess) error {
	if p.cmd != nil && p.cmd.Process != nil {
		if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			slog.Debug("signal error", "error", err)
		}

		done := make(chan struct{})
		go func() {
			if err := p.cmd.Wait(); err != nil {
				slog.Debug("process wait error", "error", err)
			}
			close(done)
		}()

		select {
		case <-done:
			return nil
		case <-time.After(5 * time.Second):
			if err := p.cmd.Process.Kill(); err != nil {
				slog.Debug("process kill error", "error", err)
			}
		}
	}
	return p.Kill()
}
