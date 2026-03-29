package failover

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
)

// RateLimitDetector monitors provider errors and detects rate-limit conditions.
//
// RateLimitDetector subscribes to "provider.error" events from the EventBus,
// extracts rate-limit signals from provider responses, and updates the HealthManager
// accordingly. When a rate-limit is detected, it publishes a "provider.rate_limited"
// event to notify other components.
type RateLimitDetector struct {
	bus    *eventbus.EventBus
	health *HealthManager
}

// NewRateLimitDetector creates a new RateLimitDetector instance.
//
// NewRateLimitDetector subscribes to "provider.error" events on the provided EventBus.
// The HealthManager is used to track rate-limited providers.
//
// Expected: bus is non-nil, health is non-nil.
// Returns: a RateLimitDetector ready to detect rate-limit conditions.
// Side effects: subscribes to "provider.error" event on the bus.
func NewRateLimitDetector(bus *eventbus.EventBus, health *HealthManager) *RateLimitDetector {
	detector := &RateLimitDetector{
		bus:    bus,
		health: health,
	}
	bus.Subscribe("provider.error", detector.HandleError)
	return detector
}

// HandleError processes provider error events and detects rate-limit conditions.
//
// HandleError extracts provider event data from the event, checks for rate-limit
// signals (HTTP 429/503, retry-after header, rate-limit keywords in error message),
// and marks the provider as rate-limited in the HealthManager. If a rate-limit is
// detected, it publishes a "provider.rate_limited" event.
//
// Expected: event is a *events.ProviderEvent.
// Returns: none.
// Side effects: may update health state and publish events.
func (d *RateLimitDetector) HandleError(event any) {
	providerEvent, ok := event.(*events.ProviderEvent)
	if !ok {
		return
	}

	data := providerEvent.Data
	retryAfter := d.extractRetryAfter(data.Response)

	if d.isRateLimitedError(data.Error) || d.hasRateLimitStatus(data.Response) {
		err := d.health.MarkRateLimited(
			data.ProviderName,
			d.extractModel(data.Request),
			retryAfter,
		)
		if err == nil {
			d.bus.Publish("provider.rate_limited", events.NewProviderEvent(events.ProviderEventData{
				ProviderName: data.ProviderName,
			}))
		}
	}
}

// extractRetryAfter extracts retry-after duration from the response if available.
//
// Expected: response may be any type.
// Returns: time.Time of the retry-after if parseable, otherwise time.Now() + 1 hour.
// Side effects: none.
func (d *RateLimitDetector) extractRetryAfter(response any) time.Time {
	if response == nil {
		return time.Now().Add(1 * time.Hour)
	}

	respMap, ok := response.(map[string]any)
	if !ok {
		return time.Now().Add(1 * time.Hour)
	}

	headers, ok := respMap["headers"].(http.Header)
	if !ok {
		return time.Now().Add(1 * time.Hour)
	}

	retryAfter := headers.Get("Retry-After")
	if retryAfter == "" {
		return time.Now().Add(1 * time.Hour)
	}

	if seconds, err := strconv.Atoi(retryAfter); err == nil {
		return time.Now().Add(time.Duration(seconds) * time.Second)
	}

	if t, err := time.Parse(time.RFC1123, retryAfter); err == nil {
		return t
	}

	return time.Now().Add(1 * time.Hour)
}

// extractModel extracts the model name from the request if available.
//
// Expected: request may be any type.
// Returns: empty string if not extractable, otherwise model name.
// Side effects: none.
func (d *RateLimitDetector) extractModel(request any) string {
	if request == nil {
		return ""
	}

	req, ok := request.(*provider.ChatRequest)
	if !ok {
		return ""
	}

	return req.Model
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

// hasRateLimitStatus checks if the response indicates a rate-limit HTTP status.
//
// Expected: response may be any type.
// Returns: true if response contains HTTP 429 or 503 status code.
// Side effects: none.
func (d *RateLimitDetector) hasRateLimitStatus(response any) bool {
	if response == nil {
		return false
	}

	respMap, ok := response.(map[string]any)
	if !ok {
		return false
	}

	statusCode, ok := respMap["status_code"].(int)
	if !ok {
		return false
	}

	return statusCode == http.StatusTooManyRequests || statusCode == http.StatusServiceUnavailable
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
		currentModel = ""
		req.Provider = currentProvider
		req.Model = currentModel
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
