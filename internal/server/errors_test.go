package server

import (
	"context"
	"errors"
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

var _ = Describe("a refused lease over HTTP", func() {
	const target = "/sessions/01a1f000-0000-4000-8000-000000000001/run"
	var rec *httptest.ResponseRecorder

	BeforeEach(func() {
		busy := errors.New("lease held")
		p := (&fakePorts{}).ports()
		p.RunSession = func(context.Context, string, RunOptions) ([]Event, error) { return nil, busy }
		p.ErrCode = func(error) string { return "session_busy" }
		rec = httptest.NewRecorder()
		New(p).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, target, strings.NewReader("")))
	})

	It("answers 409", func() { Expect(rec.Code).To(Equal(http.StatusConflict)) })
	It("keeps the code session_busy in the body", func() {
		Expect(rec.Body.String()).To(Equal(`{"code":"session_busy","message":"lease held"}`))
	})
})

var _ = Describe("a run cut off by the output cap over HTTP", func() {
	const target = "/sessions/01a1f000-0000-4000-8000-000000000001/run"
	var rec *httptest.ResponseRecorder

	BeforeEach(func() {
		p := (&fakePorts{}).ports()
		p.RunSession = func(context.Context, string, RunOptions) ([]Event, error) { return nil, errors.New("output_truncated") }
		p.ErrCode = func(error) string { return "output_truncated" }
		rec = httptest.NewRecorder()
		New(p).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, target, strings.NewReader("")))
	})

	It("answers 409", func() { Expect(rec.Code).To(Equal(http.StatusConflict)) })
	It("keeps the code output_truncated in the body", func() {
		Expect(rec.Body.String()).To(Equal(`{"code":"output_truncated","message":"output_truncated"}`))
	})
})

var _ = Describe("a run ended by the hook_refused code over HTTP", func() {
	const target = "/sessions/01a1f000-0000-4000-8000-000000000001/run"
	var rec *httptest.ResponseRecorder

	BeforeEach(func() {
		p := (&fakePorts{}).ports()
		p.RunSession = func(context.Context, string, RunOptions) ([]Event, error) { return nil, errors.New("hook_refused") }
		p.ErrCode = func(error) string { return "hook_refused" }
		rec = httptest.NewRecorder()
		New(p).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, target, strings.NewReader("")))
	})

	It("answers 422", func() { Expect(rec.Code).To(Equal(http.StatusUnprocessableEntity)) })
	It("keeps the code hook_refused in the body", func() {
		Expect(rec.Body.String()).To(Equal(`{"code":"hook_refused","message":"hook_refused"}`))
	})
})

var _ = Describe("a run ended by the hook_stop code over HTTP", func() {
	const target = "/sessions/01a1f000-0000-4000-8000-000000000001/run"
	var rec *httptest.ResponseRecorder

	BeforeEach(func() {
		p := (&fakePorts{}).ports()
		p.RunSession = func(context.Context, string, RunOptions) ([]Event, error) { return nil, errors.New("hook_stop") }
		p.ErrCode = func(error) string { return "hook_stop" }
		rec = httptest.NewRecorder()
		New(p).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, target, strings.NewReader("")))
	})

	It("answers 409", func() { Expect(rec.Code).To(Equal(http.StatusConflict)) })
	It("keeps the code hook_stop in the body", func() {
		Expect(rec.Body.String()).To(Equal(`{"code":"hook_stop","message":"hook_stop"}`))
	})
})
