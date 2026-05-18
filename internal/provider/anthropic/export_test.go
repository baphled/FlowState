package anthropic

import "time"

// SetStreamRequestTimeoutForTest overrides the provider's per-stream
// wall-clock cap inside Stream() so specs can drive the hung-body
// code path at millisecond timescales without sleeping 10 minutes.
// Setting zero restores the production default of
// defaultStreamRequestTimeout.
//
// Defence-in-depth backstop for the May 2026 mid-thinking-halt fix.
// See internal/provider/anthropic/anthropic.go:Provider.streamRequestTimeout
// for the rationale.
func (p *Provider) SetStreamRequestTimeoutForTest(d time.Duration) {
	p.streamRequestTimeout = d
}

// DefaultStreamRequestTimeoutForTest exposes the package-internal
// defaultStreamRequestTimeout constant so tests can pin its value
// without re-declaring it.
func DefaultStreamRequestTimeoutForTest() time.Duration {
	return defaultStreamRequestTimeout
}
