package tracer

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var _ Recorder = (*prometheusRecorder)(nil)

// prometheusRecorder implements Recorder using Prometheus counters and histograms.
type prometheusRecorder struct {
	retries          *prometheus.CounterVec
	validationScores *prometheus.HistogramVec
	criticResults    *prometheus.CounterVec
	providerLatency  *prometheus.HistogramVec
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
//   - Registers four Prometheus collectors with reg.
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
