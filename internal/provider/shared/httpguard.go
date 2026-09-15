package shared

import (
	"errors"
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

// DefaultTurnStreamTimeout bounds the TOTAL wall-clock duration of a
// single provider streaming turn (handshake + body), complementing
// DefaultResponseHeaderTimeout which only covers time-to-first-byte.
// Without this, a provider that trickles chunks forever (or an SDK
// retry loop inside one logical turn) can hold a turn open for hours —
// live reproducer: session 32aab76c (glm-5.3/zai, Sept 2026) ran a
// single assistant turn for 9,231,919 ms (~2h 34m) before terminating
// with tool_use_no_calls. The engine idle watchdog (60s gap) cannot
// catch a stream that keeps emitting chunks, and the TTFB guard
// cannot catch anything after the first byte — only a total-turn
// deadline caps both.
//
// Applied via context.WithTimeout inside openaicompat.RunStreamWithObserver
// (and the openai/zai providers that route through it). On expiry the
// stream closes cleanly with a *provider.Error of ErrorTypeNetworkError
// (retriable) carrying ErrTurnDeadlineExceeded — never a silent hang.
//
// Zero/negative values in the TurnDeadline override disable the cap.
const DefaultTurnStreamTimeout = 15 * time.Minute

// ErrTurnDeadlineExceeded is the sentinel wrapped by the terminal
// error chunk emitted when DefaultTurnStreamTimeout expires mid-turn.
// Consumers can detect it with errors.Is to distinguish a deadline
// kill from a transport failure.
var ErrTurnDeadlineExceeded = errors.New("per-turn stream deadline exceeded")

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
