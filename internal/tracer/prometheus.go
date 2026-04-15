package tracer

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var _ Recorder = (*prometheusRecorder)(nil)

// prometheusRecorder implements Recorder using Prometheus counters and histograms.
type prometheusRecorder struct {
	retries              *prometheus.CounterVec
	validationScores     *prometheus.HistogramVec
	criticResults        *prometheus.CounterVec
	providerLatency      *prometheus.HistogramVec
	contextWindowTokens  *prometheus.GaugeVec
	compressionTokensSav *prometheus.CounterVec
}

// NewPrometheusRecorder returns a Recorder backed by Prometheus metrics registered with reg.
//
// Expected:
//   - reg is a valid, non-nil prometheus.Registerer.
//
// Returns:
//   - A Recorder that records metrics to the provided Prometheus registry.
//
// Side effects:
//   - Registers six Prometheus collectors with reg.
func NewPrometheusRecorder(reg prometheus.Registerer) Recorder {
	return &prometheusRecorder{
		retries: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "flowstate_harness_retries_total",
			Help: "Total number of harness retry attempts.",
		}, []string{"agent_id"}),
		validationScores: promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
			Name:    "flowstate_validation_score",
			Help:    "Distribution of harness plan validation scores.",
			Buckets: prometheus.LinearBuckets(0, 0.1, 11),
		}, []string{"agent_id"}),
		criticResults: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "flowstate_critic_results_total",
			Help: "Total number of harness critic review results.",
		}, []string{"agent_id", "passed"}),
		providerLatency: promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
			Name:    "flowstate_provider_latency_ms",
			Help:    "Provider call latency in milliseconds.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 12),
		}, []string{"provider", "method"}),
		contextWindowTokens: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "flowstate_context_window_tokens",
			Help: "Size in tokens of the most recently assembled context window per agent.",
		}, []string{"agent_id"}),
		compressionTokensSav: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "flowstate_compression_tokens_saved_total",
			// M3 — the Help text is the honest contract: gross tokens
			// removed only when auto-compaction produced a net saving.
			// Compactions whose JSON-wrapped summary is as large or
			// larger than the replaced range are neither counted nor
			// subtracted — a Prometheus counter cannot decrease, and
			// subtracting silently would lie to dashboards. Operators
			// should read this counter as "cumulative win", not
			// "cumulative work done".
			Help: "Cumulative tokens eliminated by L2 auto-compaction per agent. " +
				"Incremented by OriginalTokens - SummaryTokens only when the " +
				"delta is a net saving (> 0). Compactions with non-positive " +
				"savings are NOT subtracted or counted, so a flat counter can " +
				"mean either 'compaction disabled' or 'every pass produced " +
				"overhead' — correlate with flowstate_context_window_tokens " +
				"and event logs to distinguish.",
		}, []string{"agent_id"}),
	}
}

// RecordRetry records a harness retry event for the given agent.
//
// Expected:
//   - agentID is a non-empty string identifying the agent.
//
// Side effects:
//   - Increments the retry counter label for agentID.
func (p *prometheusRecorder) RecordRetry(agentID string) {
	p.retries.WithLabelValues(agentID).Inc()
}

// RecordValidationScore records a plan validation score for the given agent.
//
// Expected:
//   - agentID is a non-empty string identifying the agent.
//   - score is a value between 0.0 and 1.0.
//
// Side effects:
//   - Observes score on the validation scores histogram for agentID.
func (p *prometheusRecorder) RecordValidationScore(agentID string, score float64) {
	p.validationScores.WithLabelValues(agentID).Observe(score)
}

// RecordCriticResult records whether the LLM critic passed or failed for the given agent.
//
// Expected:
//   - agentID is a non-empty string identifying the agent.
//
// Side effects:
//   - Increments the critic results counter for agentID with the passed label.
func (p *prometheusRecorder) RecordCriticResult(agentID string, passed bool) {
	p.criticResults.WithLabelValues(agentID, strconv.FormatBool(passed)).Inc()
}

// RecordProviderLatency records the latency in milliseconds for a provider method call.
//
// Expected:
//   - prov and method are non-empty strings identifying the provider and method.
//   - ms is a non-negative latency value in milliseconds.
//
// Side effects:
//   - Observes ms on the provider latency histogram for the prov and method labels.
func (p *prometheusRecorder) RecordProviderLatency(prov, method string, ms float64) {
	p.providerLatency.WithLabelValues(prov, method).Observe(ms)
}

// RecordContextWindowTokens sets the context-window gauge for agentID to tokens.
//
// Expected:
//   - agentID is a non-empty string identifying the agent.
//   - tokens is the assembled window size in tokens (≥0).
//
// Side effects:
//   - Sets the flowstate_context_window_tokens gauge for agentID.
func (p *prometheusRecorder) RecordContextWindowTokens(agentID string, tokens int) {
	p.contextWindowTokens.WithLabelValues(agentID).Set(float64(tokens))
}

// RecordCompressionTokensSaved adds tokensSaved to the compression-savings counter.
//
// Expected:
//   - agentID is a non-empty string identifying the agent.
//   - tokensSaved is the delta of tokens eliminated by compaction.
//     Non-positive values are ignored to keep the counter monotonic.
//
// Side effects:
//   - Increments the flowstate_compression_tokens_saved_total counter
//     for agentID by tokensSaved when positive; otherwise no-op.
func (p *prometheusRecorder) RecordCompressionTokensSaved(agentID string, tokensSaved int) {
	if tokensSaved <= 0 {
		return
	}
	p.compressionTokensSav.WithLabelValues(agentID).Add(float64(tokensSaved))
}
