package failover

import (
	"context"
	"errors"
	"fmt"
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
	cooldown, ok := classifyProviderHealthCooldown(err)
	if !ok {
		return false
	}
	health.MarkRateLimited(providerName, model, time.Now().Add(cooldown))
	return true
}

// CooldownForErrorType returns the recommended cooldown duration for a given provider error type.
//
// Expected:
//   - t is a provider error classification.
//
// Returns:
//   - A cooldown duration appropriate for the error type.
//
// Side effects:
//   - None.
//
// This table keeps provider-specific health classification aligned with the
// request-correctable versus durable distinction used by the failover hook.
func CooldownForErrorType(t provider.ErrorType) time.Duration {
	switch t {
	case provider.ErrorTypeRateLimit:
		return time.Hour
	case provider.ErrorTypeBilling, provider.ErrorTypeQuota, provider.ErrorTypeAuthFailure, provider.ErrorTypeModelNotFound:
		return 24 * time.Hour
	case provider.ErrorTypeOverload:
		return 60 * time.Second
	case provider.ErrorTypeNetworkError:
		return 30 * time.Second
	case provider.ErrorTypeServerError:
		return 2 * time.Minute
	default:
		return 5 * time.Minute
	}
}

// CooldownForAuthErrorCode returns a cooldown duration for a known
// auth-layer error code (OpenAI/Anthropic `error.code` field). A
// return of 0 means "no opinion" — the caller MUST then defer to
// CooldownForErrorType for the table default. Non-zero returns are
// authoritative for that code.
//
// S3.4: token_expired/expired_token now get a durable cooldown because
// the reactive refresh path runs before health marking and a failed
// refresh should poison the pair for long enough to re-open naturally.
// All other known auth codes get cooldowns appropriate to their severity.
//
// Expected:
//   - code is the provider-specific error code (may be "").
//
// Returns:
//   - A cooldown duration when the code is recognised.
//   - 0 when the code is unknown (caller defers to CooldownForErrorType).
//
// Side effects:
//   - None.
func CooldownForAuthErrorCode(code string) time.Duration {
	switch code {
	case "token_expired", "expired_token":
		return 24 * time.Hour
	case "invalid_api_key", "invalid_auth":
		return time.Hour
	case "account_deactivated", "billing_not_active":
		return 24 * time.Hour
	case "insufficient_quota":
		return 24 * time.Hour
	default:
		return 0 // no opinion — caller defers to CooldownForErrorType
	}
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
	bus.Subscribe(events.EventProviderError, detector.HandleError)
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
	if isAggregateFailoverError(data.Error) {
		return
	}

	if cooldown, ok := classifyProviderHealthCooldown(data.Error); ok {
		d.health.MarkRateLimited(data.ProviderName, data.ModelName, time.Now().Add(cooldown))
		d.bus.Publish(events.EventProviderRateLimited, events.NewProviderEvent(events.ProviderEventData{
			ProviderName: data.ProviderName,
		}))
		d.publishCooldownNotification(data.ProviderName, data.ModelName, cooldown)
	}
}

// publishCooldownNotification surfaces a provider cooldown as a
// user-facing NotificationEvent so the client can render a warning.
//
// Expected:
//   - provider and model identify the pair entering cooldown.
//   - cooldown is the applied duration.
//
// Returns: none.
// Side effects: publishes a `notification` event on the bus.
func (d *RateLimitDetector) publishCooldownNotification(provider, model string, cooldown time.Duration) {
	d.bus.Publish(events.EventNotification, events.NewNotificationEvent(events.NotificationEventData{
		ID:       fmt.Sprintf("cooldown:%s:%s", provider, model),
		Type:     events.NotificationTypeCooldown,
		Severity: events.NotificationSeverityWarning,
		Message:  fmt.Sprintf("Provider %s entered a %s cooldown", provider, cooldown.Truncate(time.Second)),
		Provider: provider,
		Model:    model,
	}))
}

// isRateLimitedError checks if the error indicates a rate-limit condition.
//
// Expected: err may be nil.
// Returns: true if error is a rate-limit signal.
// Side effects: none.
func (d *RateLimitDetector) isRateLimitedError(err error) bool {
	_, ok := classifyProviderHealthCooldown(err)
	return ok
}

// classifyProviderHealthCooldown ...
//
// Expected: parameters for classifyProviderHealthCooldown.
//
// Returns: result of classifyProviderHealthCooldown.
//
// Side effects: None.
func classifyProviderHealthCooldown(err error) (time.Duration, bool) {
	if err == nil || isAggregateFailoverError(err) {
		return 0, false
	}

	var provErr *provider.Error
	if errors.As(err, &provErr) {
		if isUserCorrectableError(provErr.ErrorType) {
			return 0, false
		}
		return cooldownForProviderError(provErr), true
	}

	return classifyProviderHealthCooldownText(err.Error())
}

// classifyProviderHealthCooldownText ...
//
// Expected: parameters for classifyProviderHealthCooldownText.
//
// Returns: result of classifyProviderHealthCooldownText.
//
// Side effects: None.
func classifyProviderHealthCooldownText(message string) (time.Duration, bool) {
	msg := strings.ToLower(message)

	if containsAny(msg,
		"rate_limit",
		"rate limit",
		"too many requests",
		"free usage exceeded",
	) {
		return time.Hour, true
	}

	if containsAny(msg,
		"401",
		"unauthorized",
		"authentication failed",
		"auth failed",
		"invalid api key",
		"invalid auth",
		"token expired",
		"token_expired",
		"expired_token",
		"403",
		"forbidden",
		"subscription expired",
		"account disabled",
		"limit exhausted",
		"quota exceeded",
		"quota exhausted",
		"insufficient balance",
		"billing",
	) {
		return 24 * time.Hour, true
	}

	if containsAny(msg,
		"invalid request",
		"malformed request",
		"context window exceeded",
	) {
		return 0, false
	}

	return 0, false
}

// containsAny ...
//
// Expected: parameters for containsAny.
//
// Returns: result of containsAny.
//
// Side effects: None.
func containsAny(message string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}

// isAggregateFailoverError ...
//
// Expected: parameters for isAggregateFailoverError.
//
// Returns: result of isAggregateFailoverError.
//
// Side effects: None.
func isAggregateFailoverError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "all providers failed:")
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
		next, err := fh.chain.NextHealthy(ProviderModel{}, fh.health)
		if err != nil {
			return errors.New("no healthy provider available")
		}

		req.Provider = next.Provider
		req.Model = next.Model
		return nil
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
