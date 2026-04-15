package cli_test

// Item 6 — CLI-level regression guard on serve shutdown.
//
// H3 introduced engine.Shutdown to drain session-splitter persist
// workers and L3 extraction goroutines before the serve process exits.
// Without this, those goroutines get killed at os.Exit and orphan
// `.tmp` files on disk with no log signal.
//
// The engine-level tests already cover Shutdown's drain semantics.
// This file is the CLI-level guard: a future refactor of serve.go
// that drops the engine.Shutdown call (for example, someone replacing
// the inline block with a plain `server.Shutdown` one-liner and
// forgetting the engine drain) must fail these tests.

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/baphled/flowstate/internal/cli"
)

// shutdownRecorder captures the order in which Shutdown was invoked
// across both the fake HTTP server and the fake engine. Shared mutex
// so the order slice is safe to assert against after both calls have
// completed sequentially (the production code is sequential; the mutex
// is defence against a future refactor that might parallelise).
type shutdownRecorder struct {
	mu    sync.Mutex
	order []string
}

func (r *shutdownRecorder) record(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = append(r.order, label)
}

func (r *shutdownRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

type fakeHTTPShutdowner struct {
	recorder *shutdownRecorder
	err      error
	calls    int
}

func (f *fakeHTTPShutdowner) Shutdown(_ context.Context) error {
	f.calls++
	f.recorder.record("http")
	return f.err
}

type fakeEngineShutdowner struct {
	recorder    *shutdownRecorder
	err         error
	calls       int
	ctxDeadline bool
}

func (f *fakeEngineShutdowner) Shutdown(ctx context.Context) error {
	f.calls++
	f.recorder.record("engine")
	if _, ok := ctx.Deadline(); ok {
		f.ctxDeadline = true
	}
	return f.err
}

// TestPerformServeShutdown_InvokesEngineShutdown is the core Item 6
// guard: if a future refactor drops the engine drain from runServe,
// this test must fail. An orphaned engine means the splitter persist
// workers die at os.Exit with unflushed writes.
func TestPerformServeShutdown_InvokesEngineShutdown(t *testing.T) {
	rec := &shutdownRecorder{}
	srv := &fakeHTTPShutdowner{recorder: rec}
	eng := &fakeEngineShutdowner{recorder: rec}

	var out, errOut bytes.Buffer
	if err := cli.PerformServeShutdownForTest(srv, eng, &out, &errOut); err != nil {
		t.Fatalf("performServeShutdown returned error: %v", err)
	}

	if eng.calls != 1 {
		t.Fatalf("engine.Shutdown calls = %d; want 1 (regression: engine drain skipped)", eng.calls)
	}
}

// TestPerformServeShutdown_EngineDrainCarriesDeadline pins the behaviour
// that the engine drain runs under a bounded deadline. Without this the
// serve process would block forever on a wedged extractor goroutine
// instead of emitting the documented warning.
func TestPerformServeShutdown_EngineDrainCarriesDeadline(t *testing.T) {
	rec := &shutdownRecorder{}
	srv := &fakeHTTPShutdowner{recorder: rec}
	eng := &fakeEngineShutdowner{recorder: rec}

	var out, errOut bytes.Buffer
	if err := cli.PerformServeShutdownForTest(srv, eng, &out, &errOut); err != nil {
		t.Fatalf("performServeShutdown returned error: %v", err)
	}

	if !eng.ctxDeadline {
		t.Fatal("engine.Shutdown was not given a context with a deadline; the drain will hang forever on a wedged goroutine")
	}
}

// TestPerformServeShutdown_OrdersHTTPBeforeEngine proves the sequence:
// HTTP server stops accepting, then engine drains. Reversing the order
// would let new handlers spawn splitter work after the drain
// supposedly finished.
func TestPerformServeShutdown_OrdersHTTPBeforeEngine(t *testing.T) {
	rec := &shutdownRecorder{}
	srv := &fakeHTTPShutdowner{recorder: rec}
	eng := &fakeEngineShutdowner{recorder: rec}

	var out, errOut bytes.Buffer
	if err := cli.PerformServeShutdownForTest(srv, eng, &out, &errOut); err != nil {
		t.Fatalf("performServeShutdown returned error: %v", err)
	}

	order := rec.snapshot()
	if len(order) != 2 {
		t.Fatalf("shutdown order = %v; want [http engine]", order)
	}
	if order[0] != "http" || order[1] != "engine" {
		t.Fatalf("shutdown order = %v; want [http engine]", order)
	}
}

// TestPerformServeShutdown_NilEngineIsTolerated covers the embedded-
// test path (or early-crash path) where the App has no engine. The
// helper must not panic; skipping the engine drain is acceptable
// because there is nothing to drain.
func TestPerformServeShutdown_NilEngineIsTolerated(t *testing.T) {
	rec := &shutdownRecorder{}
	srv := &fakeHTTPShutdowner{recorder: rec}

	var out, errOut bytes.Buffer
	err := cli.PerformServeShutdownForTest(srv, cli.EngineShutdownerForTest(nil), &out, &errOut)
	if err != nil {
		t.Fatalf("nil engine must not surface an error: %v", err)
	}
	if srv.calls != 1 {
		t.Fatalf("http.Shutdown calls = %d; want 1 even when engine is nil", srv.calls)
	}
}

// TestPerformServeShutdown_EngineDrainErrorDoesNotFailShutdown pins the
// documented behaviour that an engine-drain failure emits a warning
// and returns nil rather than surfacing an error. The HTTP server has
// already shut down at that point; returning an error would mask that
// and confuse the caller's exit-code handling.
func TestPerformServeShutdown_EngineDrainErrorDoesNotFailShutdown(t *testing.T) {
	rec := &shutdownRecorder{}
	srv := &fakeHTTPShutdowner{recorder: rec}
	eng := &fakeEngineShutdowner{recorder: rec, err: errors.New("drain timed out")}

	var out, errOut bytes.Buffer
	if err := cli.PerformServeShutdownForTest(srv, eng, &out, &errOut); err != nil {
		t.Fatalf("engine-drain error should not fail shutdown; got: %v", err)
	}
	if got := errOut.String(); got == "" {
		t.Fatalf("engine-drain error must surface a warning on stderr; stderr was empty")
	}
}
