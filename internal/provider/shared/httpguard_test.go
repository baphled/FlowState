package shared_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/provider/shared"
)

// TestStreamGuardHTTPClient_SetsResponseHeaderTimeout pins that the helper
// applies the requested time-to-first-byte ceiling to the returned client's
// transport. This is the field the failover layer relies on to advance past a
// flapping provider that never sends response headers.
func TestStreamGuardHTTPClient_SetsResponseHeaderTimeout(t *testing.T) {
	c := shared.StreamGuardHTTPClient(7 * time.Second)
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", c.Transport)
	}
	if tr.ResponseHeaderTimeout != 7*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %s, want 7s", tr.ResponseHeaderTimeout)
	}
}

// TestStreamGuardHTTPClient_ZeroFallsBackToDefault pins the documented
// fallback: a non-positive timeout uses DefaultResponseHeaderTimeout rather
// than disabling the guard (which would re-open the unbounded-hang gap).
func TestStreamGuardHTTPClient_ZeroFallsBackToDefault(t *testing.T) {
	for _, d := range []time.Duration{0, -5 * time.Second} {
		c := shared.StreamGuardHTTPClient(d)
		tr := c.Transport.(*http.Transport)
		if tr.ResponseHeaderTimeout != shared.DefaultResponseHeaderTimeout {
			t.Fatalf("input %s: ResponseHeaderTimeout = %s, want default %s",
				d, tr.ResponseHeaderTimeout, shared.DefaultResponseHeaderTimeout)
		}
	}
}

// TestStreamGuardHTTPClient_ClonesDefaultTransport pins that the helper clones
// http.DefaultTransport rather than building a bare one, so proxy resolution,
// keep-alive pooling and HTTP/2 negotiation are preserved in production.
func TestStreamGuardHTTPClient_ClonesDefaultTransport(t *testing.T) {
	c := shared.StreamGuardHTTPClient(time.Second)
	tr := c.Transport.(*http.Transport)
	// http.DefaultTransport sets Proxy = ProxyFromEnvironment; a bare
	// &http.Transport{} leaves it nil. A non-nil Proxy proves the clone.
	if tr.Proxy == nil {
		t.Fatal("Proxy is nil — transport was not cloned from http.DefaultTransport")
	}
}
