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
func (n *NoopRecorder) RecordRetry(_ string) {}

// RecordValidationScore discards the validation score.
func (n *NoopRecorder) RecordValidationScore(_ string, _ float64) {}

// RecordCriticResult discards the critic result.
func (n *NoopRecorder) RecordCriticResult(_ string, _ bool) {}

// RecordProviderLatency discards the provider latency.
func (n *NoopRecorder) RecordProviderLatency(_, _ string, _ float64) {}
