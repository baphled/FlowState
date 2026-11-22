package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ctxKey keys the request-scoped value the run route must still forward.
type ctxKey struct{}

// runProbe records what the run port observed on its context.
type runProbe struct {
	ctx       context.Context
	errAtCall error
	cause     error
	value     any
}

var _ = Describe("runContext", func() {
	const target = "/sessions/01a1f000-0000-4000-8000-000000000001/run"
	var probe *runProbe

	BeforeEach(func() { probe = &runProbe{} })

	// serve runs one run request on reqCtx; during the run the port calls
	// interrupt (when non-nil) and then waits for its own context to end.
	serve := func(reqCtx context.Context, interrupt func()) {
		p := (&fakePorts{}).ports()
		p.RunSession = func(ctx context.Context, _ string, _ RunOptions) ([]Event, error) {
			probe.ctx, probe.errAtCall, probe.value = ctx, ctx.Err(), ctx.Value(ctxKey{})
			if interrupt == nil {
				return nil, nil
			}
			interrupt()
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
				panic("run context was not cancelled")
			}
			probe.cause = context.Cause(ctx)
			return nil, ctx.Err()
		}
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader("")).WithContext(reqCtx)
		New(p).Handler().ServeHTTP(httptest.NewRecorder(), req)
	}

	It("hands the port a client_disconnected cause when the request is cancelled", func() {
		reqCtx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		serve(reqCtx, cancel)
		Expect(probe.cause).To(BeIdenticalTo(errClientGone))
	})

	It("hands the port a shutdown cause when the server's base context is cancelled", func() {
		reqCtx, cancel := context.WithCancelCause(context.Background())
		DeferCleanup(func() { cancel(nil) })
		serve(reqCtx, func() { cancel(errShuttingDown) })
		Expect(probe.cause).To(BeIdenticalTo(errShuttingDown))
	})

	It("names client_disconnected as the reason of a disconnect cause", func() {
		reqCtx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		serve(reqCtx, cancel)
		Expect(probe.cause.(interface{ CancelReason() string }).CancelReason()).To(Equal("client_disconnected"))
	})

	It("names shutdown as the reason of a shutdown cause", func() {
		reqCtx, cancel := context.WithCancelCause(context.Background())
		DeferCleanup(func() { cancel(nil) })
		serve(reqCtx, func() { cancel(errShuttingDown) })
		Expect(probe.cause.(interface{ CancelReason() string }).CancelReason()).To(Equal("shutdown"))
	})

	It("releases the run context when the handler returns", func() {
		serve(context.Background(), nil)
		Expect(probe.ctx.Err()).To(MatchError(context.Canceled))
	})

	It("still delivers request-scoped values to the port", func() {
		serve(context.WithValue(context.Background(), ctxKey{}, "scoped"), nil)
		Expect(probe.value).To(Equal("scoped"))
	})

	It("cancels nothing while a request completes normally", func() {
		serve(context.Background(), nil)
		Expect(probe.errAtCall).NotTo(HaveOccurred())
	})
})
