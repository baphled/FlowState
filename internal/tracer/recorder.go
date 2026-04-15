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
	// RecordContextWindowTokens sets a gauge to the size in tokens of the
	// most recently assembled context window for the given agent. Call
	// sites: context.WindowBuilder.Build after assembly completes.
	RecordContextWindowTokens(agentID string, tokens int)
	// RecordCompressionTokensSaved adds tokensSaved to a running counter
	// of tokens eliminated by L2 auto-compaction for the given agent.
	//
	// Contract: implementations MUST silently ignore any tokensSaved
	// value where tokensSaved <= 0. This is load-bearing for the
	// Prometheus backend — counters cannot decrease without panicking
	// the process — but the same contract is mandatory for any custom
	// implementation so call sites can remain layered: the engine
	// passes through the raw delta (OriginalTokens - SummaryTokens) for
	// diagnostic visibility, and the recorder is the sole enforcer of
	// monotonicity. The NoopRecorder in this package and the real
	// prometheusRecorder are both compliant.
	//
	// Call sites: engine.publishContextCompactedEvent on successful
	// compaction, using OriginalTokens - SummaryTokens. The call site
	// also guards `delta > 0` as defence in depth so investigations
	// can spot anomalies at the emit point.
	RecordCompressionTokensSaved(agentID string, tokensSaved int)
	// RecordCompressionOverheadTokens adds overheadTokens to a running
	// counter of tokens the auto-compaction layer ADDED to the window
	// instead of saving — i.e. the absolute value of the delta when
	// the summary scaffold exceeded the compacted range. Paired with
	// RecordCompressionTokensSaved so a flat tokens_saved counter can
	// be disambiguated: "layer disabled" vs "every pass produced
	// overhead" becomes visible as a rising overhead_tokens_total.
	//
	// Contract: implementations MUST silently ignore overheadTokens
	// values where overheadTokens <= 0 for exactly the same reason as
	// RecordCompressionTokensSaved — Prometheus counters are monotonic
	// and negative additions panic the process. Call sites emit with
	// the absolute value of the net-negative delta; no negative values
	// should ever reach the recorder in practice, but the guard is
	// defence in depth.
	//
	// Call sites: engine.publishContextCompactedEvent when delta < 0,
	// with abs(delta) as the argument. delta == 0 is the break-even
	// case and fires neither counter.
	RecordCompressionOverheadTokens(agentID string, overheadTokens int)
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

// RecordContextWindowTokens discards the context window gauge update.
//
// Expected:
//   - agentID is a non-empty string identifying the agent.
//   - tokens is the assembled window size in tokens.
//
// Side effects:
//   - None.
func (n *NoopRecorder) RecordContextWindowTokens(_ string, _ int) {}

// RecordCompressionTokensSaved discards the compression savings delta.
//
// Expected:
//   - agentID is a non-empty string identifying the agent.
//   - tokensSaved is the delta OriginalTokens - SummaryTokens as
//     computed by the call site. The contract on the Recorder
//     interface stipulates non-positive values are ignored; the
//     Noop implementation trivially satisfies that by discarding
//     everything.
//
// Side effects:
//   - None.
func (n *NoopRecorder) RecordCompressionTokensSaved(_ string, _ int) {}

// RecordCompressionOverheadTokens discards the compression overhead delta.
//
// Expected:
//   - agentID is a non-empty string identifying the agent.
//   - overheadTokens is abs(OriginalTokens - SummaryTokens) when that
//     delta is strictly negative. The Recorder contract stipulates
//     non-positive values are ignored; the Noop satisfies that
//     trivially by discarding every call.
//
// Side effects:
//   - None.
func (n *NoopRecorder) RecordCompressionOverheadTokens(_ string, _ int) {}
