//go:build e2e

package support

import (
	"fmt"
	"net/http/httptest"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/app"
)

// RegisterPrometheusRuntimeSteps wires the /metrics runtime-collector steps.
func RegisterPrometheusRuntimeSteps(ctx *godog.ScenarioContext) {
	s := &promRuntimeSteps{}
	ctx.Step(`^I scrape the Prometheus metrics endpoint$`, s.scrape)
	ctx.Step(`^the exposition contains "([^"]*)"$`, s.contains)
	ctx.Step(`^the exposition contains a flowstate_ metric family$`, s.containsFlowstateFamily)
}

type promRuntimeSteps struct {
	body string
}

func (s *promRuntimeSteps) scrape() error {
	handler := app.MetricsHandlerForTest()
	if handler == nil {
		return fmt.Errorf("metrics handler is nil")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		return fmt.Errorf("metrics scrape status = %d, want 200", rec.Code)
	}
	s.body = rec.Body.String()
	return nil
}

func (s *promRuntimeSteps) contains(metric string) error {
	if !strings.Contains(s.body, metric) {
		return fmt.Errorf("exposition missing %q", metric)
	}
	return nil
}

func (s *promRuntimeSteps) containsFlowstateFamily() error {
	if !strings.Contains(s.body, "flowstate_") {
		return fmt.Errorf("exposition missing any flowstate_ metric family")
	}
	return nil
}
