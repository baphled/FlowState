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
//
// Expected:
//   - r: reader for process stdout
//   - w: writer for process stdin
//   - done: channel that closes when process exits
//
// Returns: a new PluginProcess instance.
//
// Side effects: None.
func NewPluginProcess(r io.ReadCloser, w io.WriteCloser, done <-chan struct{}) *PluginProcess {
	return &PluginProcess{
		r:    r,
		w:    w,
		done: done,
	}
}

// Done returns a channel that is closed when the process exits.
//
// Expected: None.
//
// Returns: a channel that closes when the process exits.
//
// Side effects: None.
func (p *PluginProcess) Done() <-chan struct{} {
	return p.done
}

// Kill terminates the plugin process immediately.
//
// Expected: None.
//
// Returns: an error if closing streams fails, nil otherwise.
//
// Side effects: Closes stdin and stdout pipes.
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
//
// Expected: b must be a valid slice with capacity for reading data.
//
// Returns: the number of bytes read and any error encountered.
//
// Side effects: May read from the process stdout.
func (p *PluginProcess) Read(b []byte) (int, error) {
	return p.r.Read(b)
}

// Write implements io.Writer.
//
// Expected: b contains data to write to process stdin.
//
// Returns: the number of bytes written and any error encountered.
//
// Side effects: Writes to the process stdin.
func (p *PluginProcess) Write(b []byte) (int, error) {
	return p.w.Write(b)
}

// Spawner manages the lifecycle of external plugin processes.
type Spawner struct{}

// NewSpawner creates a new Spawner.
//
// Expected: None.
//
// Returns: a new Spawner instance.
//
// Side effects: None.
func NewSpawner() *Spawner {
	return &Spawner{}
}

// Spawn starts a new plugin process from the given manifest.
//
// Expected:
//   - ctx: context for process execution
//   - m: manifest with Command and Args populated
//
// Returns: a new PluginProcess and nil error, or nil process and error if spawning fails.
//
// Side effects: Starts a new OS process.
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

	pr1.Close()
	pw2.Close()

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
// Expected:
//   - name: unused but kept for interface compatibility
//   - p: the plugin process to stop
//
// Returns: an error if stopping fails, nil otherwise.
//
// Side effects: Sends signals to and may kill the process.
func (s *Spawner) StopProcess(_ string, p *PluginProcess) error {
	if p.cmd != nil && p.cmd.Process != nil {
		if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			slog.Debug("SIGTERM error", "error", err)
		}

		select {
		case <-p.done:
			return nil
		case <-time.After(5 * time.Second):
			if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				slog.Warn("SIGKILL error", "error", err)
			}
			<-p.done
		}
	}
	return p.Kill()
}
