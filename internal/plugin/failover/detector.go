package failover

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
)

// CheckAndMarkRateLimited inspects err for rate-limit signals and, if found,
// marks the provider/model pair as rate-limited in health for one hour.
//
// Expected:
//   - health is non-nil.
//   - providerName and model identify the provider/model pair.
//   - err is the error returned by the provider attempt (may be nil).
//
// Returns:
//   - true if err was a rate-limit signal and health was updated.
//   - false otherwise.
//
// Side effects:
//   - May update health state for the given provider/model.
func CheckAndMarkRateLimited(health RateLimitAware, providerName, model string, err error) bool {
	if err == nil {
		return false
	}
	d := &RateLimitDetector{health: health}
	if d.isRateLimitedError(err) {
		health.MarkRateLimited(providerName, model, time.Now().Add(time.Hour))
		return true
	}
	return false
}

// RateLimitDetector monitors provider errors and detects rate-limit conditions.
//
// RateLimitDetector subscribes to "provider.error" events from the EventBus,
// extracts rate-limit signals from provider responses, and updates the rate-limit aware health
// accordingly. When a rate-limit is detected, it publishes a "provider.rate_limited"
// event to notify other components.
type RateLimitDetector struct {
	bus    *eventbus.EventBus
	health RateLimitAware
}

// NewRateLimitDetector creates a new RateLimitDetector instance.
//
// NewRateLimitDetector subscribes to "provider.error" events on the provided EventBus.
// The HealthManager is used to track rate-limited providers.
//
// Expected: bus is non-nil, health is non-nil.
// Returns: a RateLimitDetector ready to detect rate-limit conditions.
// Side effects: subscribes to "provider.error" event on the bus.
func NewRateLimitDetector(bus *eventbus.EventBus, health RateLimitAware) *RateLimitDetector {
	detector := &RateLimitDetector{
		bus:    bus,
		health: health,
	}
	bus.Subscribe("provider.error", detector.HandleError)
	return detector
}

// HandleError processes provider error events and detects rate-limit conditions.
//
// HandleError extracts provider error event data from the event, checks for rate-limit
// signals (rate-limit keywords in error message), and marks the provider as rate-limited
// in the HealthManager. If a rate-limit is detected, it publishes a "provider.rate_limited"
// event.
//
// Expected: event is a *events.ProviderErrorEvent.
// Returns: none.
// Side effects: may update health state and publish events.
func (d *RateLimitDetector) HandleError(event any) {
	providerErrorEvent, ok := event.(*events.ProviderErrorEvent)
	if !ok {
		return
	}

	data := providerErrorEvent.Data

	if d.isRateLimitedError(data.Error) {
		d.health.MarkRateLimited(
			data.ProviderName,
			data.ModelName,
			time.Now().Add(1*time.Hour),
		)
		d.bus.Publish(events.EventProviderRateLimited, events.NewProviderEvent(events.ProviderEventData{
			ProviderName: data.ProviderName,
		}))
	}
}

// isRateLimitedError checks if the error indicates a rate-limit condition.
//
// Expected: err may be nil.
// Returns: true if error message contains rate-limit keywords.
// Side effects: none.
func (d *RateLimitDetector) isRateLimitedError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(err.Error())
	rateLimitKeywords := []string{
		"rate_limit",
		"rate limit",
		"quota exceeded",
		"too many requests",
		"free usage exceeded",
		"429",
		"503",
	}

	for _, keyword := range rateLimitKeywords {
		if strings.Contains(errMsg, keyword) {
			return true
		}
	}

	return false
}

// Hook implements ChatParamsHook to automatically switch providers on rate-limit.
//
// Hook checks if the current provider is rate-limited before each chat request,
// and if so, attempts to find a healthy alternative from the FallbackChain. If a healthy
// alternative is found, it updates the ChatRequest with the new provider and model.
type Hook struct {
	chain  *FallbackChain
	health *HealthManager
}

// NewHook creates a new Hook instance.
//
// Expected: chain is non-nil, health is non-nil.
// Returns: a Hook ready to handle provider failover.
// Side effects: none.
func NewHook(chain *FallbackChain, health *HealthManager) *Hook {
	return &Hook{
		chain:  chain,
		health: health,
	}
}

// Apply implements the ChatParamsHook interface.
//
// Apply checks if the current provider/model is rate-limited. If rate-limited (or if
// req.Provider is empty), it calls chain.NextHealthy to find an alternative. If a
// healthy alternative is found, it updates req.Provider and req.Model. If no healthy
// alternative exists, it returns an error.
//
// Expected: ctx is a valid context, req is non-nil with Provider and Model fields.
// Returns: error if no healthy provider available, nil otherwise.
// Side effects: may modify req.Provider and req.Model.
func (fh *Hook) Apply(_ context.Context, req *provider.ChatRequest) error {
	if req == nil {
		return errors.New("request is nil")
	}

	currentProvider := req.Provider
	currentModel := req.Model

	if currentProvider == "" {
		currentProvider = "anthropic"
		req.Provider = currentProvider
	}

	if !fh.health.IsRateLimited(currentProvider, currentModel) {
		return nil
	}

	current := ProviderModel{
		Provider: currentProvider,
		Model:    currentModel,
	}

	next, err := fh.chain.NextHealthy(current, fh.health)
	if err != nil {
		return errors.New("no healthy provider available")
	}

	req.Provider = next.Provider
	req.Model = next.Model

	return nil
}
