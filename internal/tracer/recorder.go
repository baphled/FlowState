package tracer

// Recorder emits observability metrics for the harness and provider layers.
type Recorder interface {
	// RecordRetry records a harness retry event for the given agent.
	RecordRetry(agentID string)
	// RecordValidationScore records a plan validation score (0.0-1.0) for the given agent.
	RecordValidationScore(agentID string, score float64)
	// RecordCriticResult records whether the LLM critic passed or failed for the given agent.
	RecordCriticResult(agentID string, passed bool)
	// RecordProviderLatency records the latency in milliseconds for a provider method call.
	RecordProviderLatency(provider, method string, ms float64)
}

// NoopRecorder is a Recorder that discards all metrics. Useful for testing.
type NoopRecorder struct{}

// RecordRetry discards the retry event.
//
// Expected:
//   - agentID is a non-empty string identifying the agent.
//
// Side effects:
//   - None.
func (n *NoopRecorder) RecordRetry(_ string) {}

// RecordValidationScore discards the validation score.
//
// Expected:
//   - agentID is a non-empty string identifying the agent.
//   - score is a float64 in the range 0.0 to 1.0.
//
// Side effects:
//   - None.
func (n *NoopRecorder) RecordValidationScore(_ string, _ float64) {}

// RecordCriticResult discards the critic result.
//
// Expected:
//   - agentID is a non-empty string identifying the agent.
//   - passed indicates whether the critic check succeeded.
//
// Side effects:
//   - None.
func (n *NoopRecorder) RecordCriticResult(_ string, _ bool) {}

// RecordProviderLatency discards the provider latency.
//
// Expected:
//   - provider is a non-empty string identifying the provider.
//   - method is a non-empty string identifying the called method.
//   - ms is the latency in milliseconds as a non-negative float64.
//
// Side effects:
//   - None.
func (n *NoopRecorder) RecordProviderLatency(_, _ string, _ float64) {}
