package ollama

import "time"

// ResolveOllamaContextLengthForTest is a test-only export of the
// unexported resolveOllamaContextLength so external_test specs can drive
// it without widening the production API surface.
func ResolveOllamaContextLengthForTest(modelID string) int {
	return resolveOllamaContextLength(modelID)
}

// SetStreamGuardHeaderTimeoutForTest overrides the package-level
// streamGuardHeaderTimeout (the time-to-first-byte ceiling injected into the
// client via shared.StreamGuardHTTPClient) so specs can drive the no-response
// flap path at sub-second timescales. Returns a restore func.
func SetStreamGuardHeaderTimeoutForTest(d time.Duration) (restore func()) {
	prev := streamGuardHeaderTimeout
	streamGuardHeaderTimeout = d
	return func() { streamGuardHeaderTimeout = prev }
}
