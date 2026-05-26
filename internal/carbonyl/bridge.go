package carbonyl

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Bridge struct {
	cfg       Config
	cmd       *exec.Cmd
	process   *os.Process
	running   atomic.Bool
	stopChan  chan struct{}
	crashChan chan error
	done      chan error
	mu        sync.Mutex
	stopped   bool

	// Carbonyl is a Chromium TUI: stdio defaults to the parent's controlling
	// terminal so the renderer can ioctl on the tty and surface its own errors.
	// Tests override with Discard/nil so fake-binary subprocesses do not pin
	// the test runner's stdio past Cmd.WaitDelay.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func New(cfg Config) *Bridge {
	return &Bridge{
		cfg:       cfg,
		crashChan: make(chan error, 1),
		done:      make(chan error, 1),
		stopChan:  make(chan struct{}),
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
	}
}

func (b *Bridge) Start(ctx context.Context) error {
	if b.running.Load() {
		return fmt.Errorf("carbonyl: bridge already running")
	}

	if err := b.validateConfig(); err != nil {
		return &StartError{Cause: err}
	}

	b.mu.Lock()
	b.stopped = false
	b.stopChan = make(chan struct{})
	b.done = make(chan error, 1)
	b.crashChan = make(chan error, 1)
	b.mu.Unlock()

	// Carbonyl parses --fps/--zoom only with =value syntax; space-separated
	// values are treated as positional targets, which triggers Chromium's
	// "Multiple targets are not supported" error.
	args := []string{
		fmt.Sprintf("--fps=%d", b.cfg.FPS),
		fmt.Sprintf("--zoom=%d", b.cfg.Zoom),
		"--disable-gpu",
		b.cfg.URL,
	}

	b.cmd = exec.CommandContext(ctx, b.cfg.BinaryPath, args...)
	b.cmd.Stdin = b.Stdin
	b.cmd.Stdout = b.Stdout
	b.cmd.Stderr = b.Stderr
	b.cmd.WaitDelay = 2 * time.Second

	if err := b.cmd.Start(); err != nil {
		return &StartError{
			Binary: b.cfg.BinaryPath,
			Cause:  fmt.Errorf("failed to start carbonyl process: %w", err),
		}
	}

	b.process = b.cmd.Process

	processDone := make(chan struct{})
	go func() {
		b.cmd.Wait()
		close(processDone)
	}()

	select {
	case <-processDone:
		return &StartError{
			Binary: b.cfg.BinaryPath,
			Cause:  fmt.Errorf("carbonyl process exited during startup (within %v)", startupGracePeriod),
		}
	case <-time.After(startupGracePeriod):
	}

	b.running.Store(true)

	go b.waitAndNotify(processDone)
	go b.watchdog()
	go b.handleSignals(ctx)

	return nil
}

func (b *Bridge) Stop() error {
	if !b.running.Load() {
		return &NotRunningError{}
	}

	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return &NotRunningError{}
	}
	b.stopped = true
	close(b.stopChan)
	b.mu.Unlock()

	b.running.Store(false)

	if b.process == nil {
		return nil
	}

	if err := b.process.Signal(syscall.SIGTERM); err != nil {
		if err == os.ErrProcessDone {
			return nil
		}
		return fmt.Errorf("carbonyl: failed to send SIGTERM to process %d: %w", b.process.Pid, err)
	}

	deadline := time.NewTimer(gracefulShutdownTimeout)
	defer deadline.Stop()

	select {
	case <-deadline.C:
		if err := b.process.Signal(syscall.SIGKILL); err != nil {
			if err == os.ErrProcessDone {
				return nil
			}
			return fmt.Errorf("carbonyl: failed to send SIGKILL to process %d: %w", b.process.Pid, err)
		}
		return &ProcessCrashError{
			PID:   b.process.Pid,
			Cause: fmt.Errorf("process did not exit within %v, sent SIGKILL", gracefulShutdownTimeout),
		}
	case <-b.done:
		return nil
	}
}

func (b *Bridge) Wait() error {
	if !b.running.Load() {
		return &NotRunningError{}
	}

	return <-b.done
}

func (b *Bridge) IsRunning() bool {
	return b.running.Load()
}

func (b *Bridge) CrashChannel() <-chan error {
	return b.crashChan
}

func (b *Bridge) handleSignals(ctx context.Context) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	select {
	case <-b.stopChan:
		return
	case <-ctx.Done():
		return
	case sig := <-sigChan:
		if b.process != nil {
			b.process.Signal(sig)
		}
	}
}

func (b *Bridge) validateConfig() error {
	if b.cfg.BinaryPath == "" {
		return fmt.Errorf("carbonyl: binary path is required")
	}

	info, err := os.Stat(b.cfg.BinaryPath)
	if err != nil {
		return fmt.Errorf("carbonyl: binary not found at %s: %w", b.cfg.BinaryPath, err)
	}

	if info.IsDir() {
		return fmt.Errorf("carbonyl: path %s is a directory, not a file", b.cfg.BinaryPath)
	}

	if info.Mode()&0111 == 0 {
		return fmt.Errorf("carbonyl: binary %s is not executable", b.cfg.BinaryPath)
	}

	if b.cfg.URL == "" {
		return fmt.Errorf("carbonyl: URL is required")
	}

	if b.cfg.FPS <= 0 {
		return fmt.Errorf("carbonyl: FPS must be positive, got %d", b.cfg.FPS)
	}

	if b.cfg.Zoom <= 0 {
		return fmt.Errorf("carbonyl: zoom must be positive, got %d", b.cfg.Zoom)
	}

	return nil
}

func (b *Bridge) waitAndNotify(processDone <-chan struct{}) {
	<-processDone

	wasRunning := b.running.Swap(false)

	if wasRunning {
		select {
		case b.crashChan <- &ProcessCrashError{
			PID:   b.process.Pid,
			Cause: fmt.Errorf("carbonyl process exited unexpectedly"),
		}:
		default:
		}
	}

	b.done <- nil
}

func (b *Bridge) watchdog() {
	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.stopChan:
			return
		case <-ticker.C:
			if !b.running.Load() {
				return
			}

			if err := b.process.Signal(syscall.Signal(0)); err != nil {
				b.running.Store(false)
				select {
				case b.crashChan <- &ProcessCrashError{
					PID:   b.process.Pid,
					Cause: fmt.Errorf("carbonyl process died unexpectedly: %w", err),
				}:
				default:
				}
				return
			}
		}
	}
}
