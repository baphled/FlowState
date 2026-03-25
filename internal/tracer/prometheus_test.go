package tracer_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/baphled/flowstate/internal/tracer"
)

var _ tracer.Recorder = (*tracer.PrometheusRecorder)(nil)

var _ = Describe("PrometheusRecorder", func() {
	var (
		reg *prometheus.Registry
		rec *tracer.PrometheusRecorder
	)

	BeforeEach(func() {
		reg = prometheus.NewRegistry()
		rec = tracer.NewPrometheusRecorder(reg)
	})

	Describe("RecordRetry", func() {
		It("increments the retry counter for the given agent", func() {
			rec.RecordRetry("agent-1")
			rec.RecordRetry("agent-1")
			rec.RecordRetry("agent-2")

			val := counterValue(reg, "flowstate_harness_retries_total", prometheus.Labels{"agent_id": "agent-1"})
			Expect(val).To(Equal(2.0))

			val = counterValue(reg, "flowstate_harness_retries_total", prometheus.Labels{"agent_id": "agent-2"})
			Expect(val).To(Equal(1.0))
		})
	})

	Describe("RecordValidationScore", func() {
		It("records the score in the histogram", func() {
			rec.RecordValidationScore("agent-1", 0.85)
			rec.RecordValidationScore("agent-1", 0.92)

			count := histogramCount(reg, "flowstate_validation_score", prometheus.Labels{"agent_id": "agent-1"})
			Expect(count).To(Equal(uint64(2)))
		})

		It("records the sum of observed scores", func() {
			rec.RecordValidationScore("agent-1", 0.5)
			rec.RecordValidationScore("agent-1", 0.3)

			sum := histogramSum(reg, "flowstate_validation_score", prometheus.Labels{"agent_id": "agent-1"})
			Expect(sum).To(BeNumerically("~", 0.8, 0.001))
		})
	})

	Describe("RecordCriticResult", func() {
		It("increments the counter with passed=true label", func() {
			rec.RecordCriticResult("agent-1", true)
			rec.RecordCriticResult("agent-1", true)

			val := counterValue(reg, "flowstate_critic_results_total", prometheus.Labels{
				"agent_id": "agent-1",
				"passed":   "true",
			})
			Expect(val).To(Equal(2.0))
		})

		It("increments the counter with passed=false label", func() {
			rec.RecordCriticResult("agent-1", false)

			val := counterValue(reg, "flowstate_critic_results_total", prometheus.Labels{
				"agent_id": "agent-1",
				"passed":   "false",
			})
			Expect(val).To(Equal(1.0))
		})
	})

	Describe("RecordProviderLatency", func() {
		It("records latency in the histogram", func() {
			rec.RecordProviderLatency("anthropic", "stream", 150.0)
			rec.RecordProviderLatency("anthropic", "chat", 200.0)

			count := histogramCount(reg, "flowstate_provider_latency_ms", prometheus.Labels{
				"provider": "anthropic",
				"method":   "stream",
			})
			Expect(count).To(Equal(uint64(1)))

			sum := histogramSum(reg, "flowstate_provider_latency_ms", prometheus.Labels{
				"provider": "anthropic",
				"method":   "chat",
			})
			Expect(sum).To(Equal(200.0))
		})
	})
})

func gatherMetricFamily(reg *prometheus.Registry, name string) *dto.MetricFamily {
	families, err := reg.Gather()
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	for _, f := range families {
		if f.GetName() == name {
			return f
		}
	}
	Fail(fmt.Sprintf("metric family %q not found", name))
	return nil
}

func matchLabels(m *dto.Metric, labels prometheus.Labels) bool {
	if len(m.GetLabel()) != len(labels) {
		return false
	}
	for _, lp := range m.GetLabel() {
		expected, ok := labels[lp.GetName()]
		if !ok || lp.GetValue() != expected {
			return false
		}
	}
	return true
}

func counterValue(reg *prometheus.Registry, name string, labels prometheus.Labels) float64 {
	family := gatherMetricFamily(reg, name)
	for _, m := range family.GetMetric() {
		if matchLabels(m, labels) {
			return m.GetCounter().GetValue()
		}
	}
	Fail(fmt.Sprintf("counter metric %q with labels %v not found", name, labels))
	return 0
}

func histogramCount(reg *prometheus.Registry, name string, labels prometheus.Labels) uint64 {
	family := gatherMetricFamily(reg, name)
	for _, m := range family.GetMetric() {
		if matchLabels(m, labels) {
			return m.GetHistogram().GetSampleCount()
		}
	}
	Fail(fmt.Sprintf("histogram metric %q with labels %v not found", name, labels))
	return 0
}

func histogramSum(reg *prometheus.Registry, name string, labels prometheus.Labels) float64 {
	family := gatherMetricFamily(reg, name)
	for _, m := range family.GetMetric() {
		if matchLabels(m, labels) {
			return m.GetHistogram().GetSampleSum()
		}
	}
	Fail(fmt.Sprintf("histogram metric %q with labels %v not found", name, labels))
	return 0
}
