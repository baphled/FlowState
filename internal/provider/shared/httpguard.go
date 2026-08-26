package shared

import (
	"net/http"
	"time"
)

// DefaultResponseHeaderTimeout bounds how long a provider streaming request
// waits for the upstream's response HEADERS (time-to-first-byte) before the
// attempt fails. A flapping provider that accepts the TCP connection but never
// sends a response otherwise hangs the caller indefinitely: openai/zai pass no
// per-attempt timeout at all, so a swarm member whose ctx has no deadline (the
// planning-loop default, member_timeout unset) blocks forever; anthropic's
// 10-minute wall-clock makes the same flap merely slow. This header timeout
// surfaces the dead connection within one failover-able window so the failover
// layer advances to a reachable provider.
//
// It does NOT cap total stream duration — once headers arrive the request runs
// to completion, so a legitimate long-running (extended-thinking) stream is
// unaffected. Mid-stream stalls after the first byte are covered separately by
// the engine idle watchdog (engineStreamIdleTimeout = 60s).
//
// Proven against both Stainless SDKs (anthropic-sdk-go, openai-go) over a real
// HTTP/2 connection — Go 1.26 honours ResponseHeaderTimeout on h2, unlike older
// releases that documented it as "no effect for HTTP/2". See
// internal/provider/*/stream_guard_test.go for the reproductions (black-hole
// no-headers servers + the openaicompat HTTP/2 mechanism proof).
const DefaultResponseHeaderTimeout = 60 * time.Second

// StreamGuardHTTPClient returns an *http.Client whose transport is a clone of
// http.DefaultTransport (preserving proxy resolution, keep-alive pooling and
// HTTP/2 negotiation) with ResponseHeaderTimeout applied. Both provider SDKs
// accept it via option.WithHTTPClient.
//
// Expected:
//   - responseHeaderTimeout is the desired time-to-first-byte ceiling; a zero
//     or negative value falls back to DefaultResponseHeaderTimeout.
//
// Returns:
//   - An *http.Client ready to hand to option.WithHTTPClient.
//
// Side effects:
//   - None (each call builds an independent client + transport).
func StreamGuardHTTPClient(responseHeaderTimeout time.Duration) *http.Client {
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = DefaultResponseHeaderTimeout
	}
	var tr *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = base.Clone()
	} else {
		// Defensive: DefaultTransport is always *http.Transport in std,
		// but a test or third party may have swapped it. Fall back to a
		// fresh transport rather than panicking on the type assertion.
		tr = &http.Transport{}
	}
	tr.ResponseHeaderTimeout = responseHeaderTimeout
	return &http.Client{Transport: tr}
}
