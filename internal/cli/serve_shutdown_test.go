package cli_test

import (
	"bytes"
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/cli"
)

// shutdownRecorder captures the order in which Shutdown was invoked
// across both the fake HTTP server and the fake engine. Shared mutex so
// the order slice is safe to assert against after both calls have
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

// Item 6 — CLI-level regression guard on serve shutdown.
//
// H3 introduced engine.Shutdown to drain session-splitter persist workers
// and L3 extraction goroutines before the serve process exits. Without
// this, those goroutines get killed at os.Exit and orphan `.tmp` files
// on disk with no log signal.
//
// The engine-level tests already cover Shutdown's drain semantics. This
// file is the CLI-level guard: a future refactor of serve.go that drops
// the engine.Shutdown call (for example, someone replacing the inline
// block with a plain `server.Shutdown` one-liner and forgetting the
// engine drain) must fail these specs.
var _ = Describe("performServeShutdown", func() {
	It("invokes engine.Shutdown after the HTTP server (regression: drain skipped)", func() {
		rec := &shutdownRecorder{}
		srv := &fakeHTTPShutdowner{recorder: rec}
		eng := &fakeEngineShutdowner{recorder: rec}

		var out, errOut bytes.Buffer
		Expect(cli.PerformServeShutdownForTest(srv, eng, &out, &errOut)).To(Succeed())
		Expect(eng.calls).To(Equal(1))
	})

	It("supplies a context with a deadline to engine.Shutdown so the drain cannot hang forever", func() {
		rec := &shutdownRecorder{}
		srv := &fakeHTTPShutdowner{recorder: rec}
		eng := &fakeEngineShutdowner{recorder: rec}

		var out, errOut bytes.Buffer
		Expect(cli.PerformServeShutdownForTest(srv, eng, &out, &errOut)).To(Succeed())
		Expect(eng.ctxDeadline).To(BeTrue(),
			"engine.Shutdown was not given a context with a deadline")
	})

	It("orders HTTP shutdown before engine drain", func() {
		rec := &shutdownRecorder{}
		srv := &fakeHTTPShutdowner{recorder: rec}
		eng := &fakeEngineShutdowner{recorder: rec}

		var out, errOut bytes.Buffer
		Expect(cli.PerformServeShutdownForTest(srv, eng, &out, &errOut)).To(Succeed())

		Expect(rec.snapshot()).To(Equal([]string{"http", "engine"}))
	})

	It("tolerates a nil engine (embedded-test or early-crash path)", func() {
		rec := &shutdownRecorder{}
		srv := &fakeHTTPShutdowner{recorder: rec}

		var out, errOut bytes.Buffer
		err := cli.PerformServeShutdownForTest(srv, cli.EngineShutdownerForTest(nil), &out, &errOut)
		Expect(err).NotTo(HaveOccurred(),
			"nil engine must not surface an error")
		Expect(srv.calls).To(Equal(1),
			"http.Shutdown must run even when engine is nil")
	})

	It("emits a warning to stderr (and returns nil) when the engine drain errors", func() {
		rec := &shutdownRecorder{}
		srv := &fakeHTTPShutdowner{recorder: rec}
		eng := &fakeEngineShutdowner{recorder: rec, err: errors.New("drain timed out")}

		var out, errOut bytes.Buffer
		Expect(cli.PerformServeShutdownForTest(srv, eng, &out, &errOut)).To(Succeed())
		Expect(errOut.String()).NotTo(BeEmpty(),
			"engine-drain error must surface a warning on stderr")
	})
})
