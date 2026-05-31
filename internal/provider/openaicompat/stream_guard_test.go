package openaicompat_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
	"github.com/baphled/flowstate/internal/provider/shared"
)

// TestStreamGuard_BoundsHTTP2NoHeaders is the linchpin test for the whole
// stream-guard fix. Production provider endpoints negotiate HTTP/2, and older
// Go releases documented http.Transport.ResponseHeaderTimeout as having "no
// effect for HTTP/2" — which would make the guard inert in production while
// still passing an HTTP/1.1 unit test. This serves a no-headers black-hole
// over a REAL h2 (TLS + ALPN) connection, confirms server-side that h2 was
// negotiated, and asserts shared.StreamGuardHTTPClient still bounds the
// time-to-first-byte window. If a future Go release regresses h2 support, this
// test fails loudly rather than the guard silently going dead.
func TestStreamGuard_BoundsHTTP2NoHeaders(t *testing.T) {
	proto := make(chan string, 1)
	blackhole := httptest.NewUnstartedServer(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			select {
			case proto <- r.Proto:
			default:
			}
			select {
			case <-r.Context().Done():
			case <-time.After(8 * time.Second):
			}
		},
	))
	blackhole.EnableHTTP2 = true
	blackhole.StartTLS()
	defer blackhole.Close()

	// Trust the test server's cert and force h2 on the guard's cloned
	// transport. We build the client via the production helper so the test
	// exercises the real ResponseHeaderTimeout wiring, only swapping in the
	// test TLS roots + a sub-second timeout.
	hc := shared.StreamGuardHTTPClient(700 * time.Millisecond)
	tr := hc.Transport.(*http.Transport)
	certpool := x509.NewCertPool()
	certpool.AddCert(blackhole.Certificate())
	tr.TLSClientConfig = &tls.Config{RootCAs: certpool} //nolint:gosec // test cert pool
	tr.ForceAttemptHTTP2 = true

	client := openaiAPI.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(blackhole.URL),
		option.WithMaxRetries(0),
		option.WithHTTPClient(hc),
	)
	params := openaicompat.BuildParams(provider.ChatRequest{
		Model:    "gpt-4o",
		Messages: []provider.Message{{Role: "user", Content: "hello"}},
	})

	done := make(chan struct{})
	go func() {
		ch := openaicompat.RunStream(context.Background(), client, params, "stream-guard-h2")
		for range ch { //nolint:revive // drain to terminal/close
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunStream did not terminate within 3s — ResponseHeaderTimeout did not bound the HTTP/2 no-headers window")
	}

	select {
	case p := <-proto:
		if p != "HTTP/2.0" {
			t.Fatalf("connection negotiated %q, want HTTP/2.0 — test did not exercise the h2 path", p)
		}
	default:
		t.Fatal("server never observed the request — cannot confirm HTTP/2 was exercised")
	}
}
