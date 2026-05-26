package carbonyl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newTestBridge constructs a Bridge with stdio detached from the test runner.
// Production code inherits the controlling terminal so Carbonyl can ioctl on
// the tty; tests use sleep-style fake binaries that would otherwise hold the
// test process's stdio open past Cmd.WaitDelay and trip "I/O incomplete".
func newTestBridge(cfg Config) *Bridge {
	b := New(cfg)
	b.Stdin = nil
	b.Stdout = io.Discard
	b.Stderr = io.Discard
	return b
}

func makeFakeBinary(t *testing.T, name string, script string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, name)

	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0755); err != nil {
		t.Fatalf("failed to create fake binary: %v", err)
	}

	return path
}

func makeLongLivedBinary(t *testing.T) string {
	return makeFakeBinary(t, "fake-carbonyl", "sleep 300")
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.FPS != defaultFPS {
		t.Errorf("DefaultConfig().FPS = %d, want %d", cfg.FPS, defaultFPS)
	}
	if cfg.Zoom != defaultZoom {
		t.Errorf("DefaultConfig().Zoom = %d, want %d", cfg.Zoom, defaultZoom)
	}
	if cfg.Width != 0 {
		t.Errorf("DefaultConfig().Width = %d, want 0", cfg.Width)
	}
	if cfg.Height != 0 {
		t.Errorf("DefaultConfig().Height = %d, want 0", cfg.Height)
	}
}

func TestConfigWithMethods(t *testing.T) {
	cfg := DefaultConfig().
		WithBinary("/usr/local/bin/carbonyl").
		WithURL("http://localhost:5173").
		WithFPS(30).
		WithZoom(120).
		WithSize(200, 50)

	if cfg.BinaryPath != "/usr/local/bin/carbonyl" {
		t.Errorf("WithBinary: got %q", cfg.BinaryPath)
	}
	if cfg.URL != "http://localhost:5173" {
		t.Errorf("WithURL: got %q", cfg.URL)
	}
	if cfg.FPS != 30 {
		t.Errorf("WithFPS: got %d", cfg.FPS)
	}
	if cfg.Zoom != 120 {
		t.Errorf("WithZoom: got %d", cfg.Zoom)
	}
	if cfg.Width != 200 || cfg.Height != 50 {
		t.Errorf("WithSize: got %dx%d", cfg.Width, cfg.Height)
	}
}

func TestConfigApplyDefaults(t *testing.T) {
	cfg := Config{}.ApplyDefaults()

	if cfg.FPS != defaultFPS {
		t.Errorf("ApplyDefaults().FPS = %d, want %d", cfg.FPS, defaultFPS)
	}
	if cfg.Zoom != defaultZoom {
		t.Errorf("ApplyDefaults().Zoom = %d, want %d", cfg.Zoom, defaultZoom)
	}
}

func TestConfigTerminalSize(t *testing.T) {
	cfg := Config{Width: 120, Height: 40}
	w, h := cfg.TerminalSize()
	if w != 120 || h != 40 {
		t.Errorf("TerminalSize: got %dx%d, want 120x40", w, h)
	}

	cfg = Config{}
	w, h = cfg.TerminalSize()
	if w <= 0 || h <= 0 {
		t.Errorf("TerminalSize auto-detect: got %dx%d, want positive values", w, h)
	}
}

func TestStartWithMissingBinary(t *testing.T) {
	cfg := DefaultConfig().WithBinary("/nonexistent/carbonyl").WithURL("http://localhost:5173")
	b := newTestBridge(cfg)

	err := b.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for missing binary")
	}

	var startErr *StartError
	if !errors.As(err, &startErr) {
		t.Errorf("expected *StartError, got %T: %v", err, err)
	}
}

func TestStartWithNonExecutableBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notexec")

	if err := os.WriteFile(path, []byte("not executable"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	cfg := DefaultConfig().WithBinary(path).WithURL("http://localhost:5173")
	b := newTestBridge(cfg)

	err := b.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for non-executable binary")
	}

	var startErr *StartError
	if !errors.As(err, &startErr) {
		t.Errorf("expected *StartError, got %T: %v", err, err)
	}
}

func TestStartWithDirectoryAsBinary(t *testing.T) {
	dir := t.TempDir()

	cfg := DefaultConfig().WithBinary(dir).WithURL("http://localhost:5173")
	b := newTestBridge(cfg)

	err := b.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for directory as binary path")
	}

	var startErr *StartError
	if !errors.As(err, &startErr) {
		t.Errorf("expected *StartError, got %T: %v", err, err)
	}
}

func TestStartWithEmptyURL(t *testing.T) {
	fakeBin := makeLongLivedBinary(t)

	cfg := DefaultConfig().WithBinary(fakeBin).WithURL("")
	b := newTestBridge(cfg)

	err := b.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for empty URL")
	}

	if !strings.Contains(err.Error(), "URL is required") {
		t.Errorf("error should mention URL requirement, got: %v", err)
	}
}

func TestStartWithZeroFPS(t *testing.T) {
	fakeBin := makeLongLivedBinary(t)

	cfg := Config{BinaryPath: fakeBin, URL: "http://localhost:5173", FPS: 0, Zoom: 100}
	b := newTestBridge(cfg)

	err := b.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for zero FPS")
	}

	if !strings.Contains(err.Error(), "FPS must be positive") {
		t.Errorf("error should mention FPS, got: %v", err)
	}
}

func TestStartWithZeroZoom(t *testing.T) {
	fakeBin := makeLongLivedBinary(t)

	cfg := Config{BinaryPath: fakeBin, URL: "http://localhost:5173", FPS: 15, Zoom: 0}
	b := newTestBridge(cfg)

	err := b.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for zero zoom")
	}

	if !strings.Contains(err.Error(), "zoom must be positive") {
		t.Errorf("error should mention zoom, got: %v", err)
	}
}

func TestStartAndStop(t *testing.T) {
	fakeBin := makeLongLivedBinary(t)

	cfg := DefaultConfig().WithBinary(fakeBin).WithURL("http://localhost:5173")
	b := newTestBridge(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !b.IsRunning() {
		t.Fatal("IsRunning() should be true after Start()")
	}

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if b.IsRunning() {
		t.Fatal("IsRunning() should be false after Stop()")
	}
}

func TestDoubleStart(t *testing.T) {
	fakeBin := makeLongLivedBinary(t)

	cfg := DefaultConfig().WithBinary(fakeBin).WithURL("http://localhost:5173")
	b := newTestBridge(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	defer b.Stop()

	err := b.Start(ctx)
	if err == nil {
		t.Fatal("expected error on double Start()")
	}

	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error should mention already running, got: %v", err)
	}
}

func TestStopWhenNotRunning(t *testing.T) {
	cfg := DefaultConfig()
	b := newTestBridge(cfg)

	err := b.Stop()
	if err == nil {
		t.Fatal("expected error when stopping non-running bridge")
	}

	var notRunning *NotRunningError
	if !errors.As(err, &notRunning) {
		t.Errorf("expected *NotRunningError, got %T: %v", err, err)
	}
}

func TestWaitWhenNotRunning(t *testing.T) {
	cfg := DefaultConfig()
	b := newTestBridge(cfg)

	err := b.Wait()
	if err == nil {
		t.Fatal("expected error when waiting on non-running bridge")
	}

	var notRunning *NotRunningError
	if !errors.As(err, &notRunning) {
		t.Errorf("expected *NotRunningError, got %T: %v", err, err)
	}
}

func TestWaitReturnsOnProcessExit(t *testing.T) {
	fakeBin := makeFakeBinary(t, "fake-carbonyl", "sleep 5")

	cfg := DefaultConfig().WithBinary(fakeBin).WithURL("http://localhost:5173")
	b := newTestBridge(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- b.Wait()
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Log("Wait returned nil on clean process exit")
		}
		if b.IsRunning() {
			t.Fatal("IsRunning() should be false after Wait() returns")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Wait did not return within 10s")
	}
}

func TestWatchdogDetectsCrash(t *testing.T) {
	fakeBin := makeFakeBinary(t, "fake-carbonyl", "sleep 5 && exit 1")

	cfg := Config{
		BinaryPath: fakeBin,
		URL:        "http://localhost:5173",
		FPS:        15,
		Zoom:       100,
	}
	b := newTestBridge(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	select {
	case crashErr := <-b.CrashChannel():
		if crashErr == nil {
			t.Fatal("expected non-nil crash error")
		}

		var procErr *ProcessCrashError
		if !errors.As(crashErr, &procErr) {
			t.Errorf("expected *ProcessCrashError, got %T: %v", crashErr, crashErr)
		}

		if b.IsRunning() {
			t.Fatal("IsRunning() should be false after crash detected")
		}

	case <-time.After(20 * time.Second):
		t.Fatal("watchdog did not detect crash within 20s")
	}
}

func TestStartDetectsImmediateExit(t *testing.T) {
	fakeBin := makeFakeBinary(t, "fake-carbonyl", "exit 1")

	cfg := DefaultConfig().WithBinary(fakeBin).WithURL("http://localhost:5173")
	b := newTestBridge(cfg)

	err := b.Start(context.Background())
	if err == nil {
		t.Fatal("expected error when process exits immediately")
	}

	var startErr *StartError
	if !errors.As(err, &startErr) {
		t.Errorf("expected *StartError, got %T: %v", err, err)
	}

	if !strings.Contains(err.Error(), "exited during startup") {
		t.Errorf("error should mention startup failure, got: %v", err)
	}
}

func TestDoubleStop(t *testing.T) {
	fakeBin := makeLongLivedBinary(t)

	cfg := DefaultConfig().WithBinary(fakeBin).WithURL("http://localhost:5173")
	b := newTestBridge(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := b.Stop(); err != nil {
		t.Fatalf("first Stop failed: %v", err)
	}

	err := b.Stop()
	if err == nil {
		t.Fatal("expected error on second Stop()")
	}

	var notRunning *NotRunningError
	if !errors.As(err, &notRunning) {
		t.Errorf("expected *NotRunningError on double stop, got %T: %v", err, err)
	}
}

func TestContextCancellation(t *testing.T) {
	fakeBin := makeLongLivedBinary(t)

	cfg := DefaultConfig().WithBinary(fakeBin).WithURL("http://localhost:5173")
	b := newTestBridge(cfg)

	ctx, cancel := context.WithCancel(context.Background())

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	cancel()

	if b.IsRunning() {
		time.Sleep(2 * time.Second)
	}

	if b.IsRunning() {
		t.Log("Process still running after context cancel — may need manual Stop()")
		b.Stop()
	}
}

func TestIsRunningAtomicTransitions(t *testing.T) {
	fakeBin := makeLongLivedBinary(t)

	cfg := DefaultConfig().WithBinary(fakeBin).WithURL("http://localhost:5173")
	b := newTestBridge(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if b.IsRunning() {
		t.Fatal("new bridge should not be running")
	}

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !b.IsRunning() {
		t.Fatal("bridge should be running after Start()")
	}

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if b.IsRunning() {
		t.Fatal("bridge should not be running after Stop()")
	}
}

func TestErrorTypes(t *testing.T) {
	t.Run("StartError", func(t *testing.T) {
		err := &StartError{Binary: "/usr/bin/carbonyl", Cause: fmt.Errorf("not found")}
		if !strings.Contains(err.Error(), "/usr/bin/carbonyl") {
			t.Errorf("StartError.Error() should contain binary path: %v", err)
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("StartError.Error() should contain cause: %v", err)
		}
		if unwrapped := err.Unwrap(); unwrapped == nil {
			t.Fatal("StartError.Unwrap() should return non-nil")
		}
	})

	t.Run("StartError without binary", func(t *testing.T) {
		err := &StartError{Cause: fmt.Errorf("config error")}
		if !strings.Contains(err.Error(), "config error") {
			t.Errorf("StartError.Error() should contain cause: %v", err)
		}
	})

	t.Run("ProcessCrashError", func(t *testing.T) {
		err := &ProcessCrashError{PID: 12345, Cause: fmt.Errorf("signal: killed")}
		if !strings.Contains(err.Error(), "12345") {
			t.Errorf("ProcessCrashError.Error() should contain PID: %v", err)
		}
		if !strings.Contains(err.Error(), "signal: killed") {
			t.Errorf("ProcessCrashError.Error() should contain cause: %v", err)
		}
		if unwrapped := err.Unwrap(); unwrapped == nil {
			t.Fatal("ProcessCrashError.Unwrap() should return non-nil")
		}
	})

	t.Run("NotRunningError", func(t *testing.T) {
		err := &NotRunningError{}
		if !strings.Contains(err.Error(), "not running") {
			t.Errorf("NotRunningError.Error() should mention not running: %v", err)
		}
	})
}

func TestBridgeArgs(t *testing.T) {
	dir := t.TempDir()
	outputFile := filepath.Join(dir, "args.log")
	path := filepath.Join(dir, "fake-carbonyl")

	scriptContent := fmt.Sprintf("#!/bin/sh\necho \"ARGS: $*\" >> %s\nsleep 300\n", outputFile)
	if err := os.WriteFile(path, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("failed to create fake binary: %v", err)
	}

	cfg := Config{
		BinaryPath: path,
		URL:        "http://localhost:5173",
		FPS:        20,
		Zoom:       130,
	}
	b := newTestBridge(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	b.Stop()

	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("failed to read args log: %v", err)
	}

	args := string(data)
	if !strings.Contains(args, "--fps") {
		t.Errorf("args should contain --fps, got: %s", args)
	}
	if !strings.Contains(args, "20") {
		t.Errorf("args should contain FPS value 20, got: %s", args)
	}
	if !strings.Contains(args, "--zoom") {
		t.Errorf("args should contain --zoom, got: %s", args)
	}
	if !strings.Contains(args, "130") {
		t.Errorf("args should contain zoom value 130, got: %s", args)
	}
	if !strings.Contains(args, "--disable-gpu") {
		t.Errorf("args should contain --disable-gpu, got: %s", args)
	}
	if !strings.Contains(args, "http://localhost:5173") {
		t.Errorf("args should contain URL, got: %s", args)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	fakeBin := makeLongLivedBinary(t)

	cfg := DefaultConfig().WithBinary(fakeBin).WithURL("http://localhost:5173")
	b := newTestBridge(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	before := runtime.NumGoroutine()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()

	delta := after - before
	if delta > 2 {
		t.Errorf("potential goroutine leak: %d goroutines before, %d after (delta=%d)", before, after, delta)
	}
}

func TestNewReturnsCorrectDefaults(t *testing.T) {
	cfg := DefaultConfig()
	b := newTestBridge(cfg)

	if b.IsRunning() {
		t.Fatal("new bridge should not be running")
	}
}

func TestForceKillAfterGracePeriod(t *testing.T) {
	fakeBin := makeFakeBinary(t, "fake-carbonyl", "trap '' TERM\nsleep 300")

	cfg := DefaultConfig().WithBinary(fakeBin).WithURL("http://localhost:5173")
	b := newTestBridge(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- b.Stop()
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Log("Stop returned nil — process was killed after grace period")
		} else {
			var crashErr *ProcessCrashError
			if errors.As(err, &crashErr) {
				t.Log("Stop returned ProcessCrashError — expected for SIGTERM-ignoring process")
			} else {
				t.Errorf("unexpected error from Stop: %v", err)
			}
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Stop did not return within 15s — process may not have been killed")
	}
}
