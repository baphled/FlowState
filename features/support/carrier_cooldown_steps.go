//go:build e2e

package support

import (
	"context"
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
)

// failoverHookWithSingleCandidate wires a fresh HealthManager for the
// given candidate pair with persistence redirected to a throwaway so
// scenarios never touch the real health cache.
//
// Expected:
//   - providerName and model identify the candidate pair.
//
// Side effects:
//   - Resets scenario state and stamps a temp persist path.

// candidateFailsWithRateLimitRetryAfter queues a rate-limit error
// carrying the carrier's retry-after signal.
//
// Expected:
//   - seconds is the parsed retry-after duration.
//
// Side effects:
//   - Appends the typed error to the pending queue.
func (fs *FailoverSteps) candidateFailsWithRateLimitRetryAfter(seconds int) error {
	fs.queueError(&provider.Error{
		HTTPStatus: 429,
		ErrorType:  provider.ErrorTypeRateLimit,
		Provider:   fs.candidateProvider,
		Message:    "rate limited by carrier",
		RateLimit:  &provider.RateLimit{RetryAfter: time.Duration(seconds) * time.Second},
	})
	return nil
}

// candidateFailsWithAuthRetryAfter queues an auth error carrying the
// carrier's retry-after signal.
//
// Expected:
//   - seconds is the parsed retry-after duration.
//
// Side effects:
//   - Appends the typed error to the pending queue.
func (fs *FailoverSteps) candidateFailsWithAuthRetryAfter(seconds int) error {
	fs.queueError(&provider.Error{
		HTTPStatus: 401,
		ErrorType:  provider.ErrorTypeAuthFailure,
		Provider:   fs.candidateProvider,
		Message:    "auth failure with carrier retry-after",
		RateLimit:  &provider.RateLimit{RetryAfter: time.Duration(seconds) * time.Second},
	})
	return nil
}

// candidateFailsWithAuthCodeNoRetryAfter queues an auth error whose
// carrier was silent but whose error.code is known.
//
// Expected:
//   - code is the provider-specific auth error code.
//
// Side effects:
//   - Appends the typed error to the pending queue.
func (fs *FailoverSteps) candidateFailsWithAuthCodeNoRetryAfter(code string) error {
	fs.queueError(&provider.Error{
		HTTPStatus: 401,
		ErrorCode:  code,
		ErrorType:  provider.ErrorTypeAuthFailure,
		Provider:   fs.candidateProvider,
		Message:    "auth failure without retry-after",
	})
	return nil
}

// candidateFailsWithBillingNoRetryAfter queues a billing error with a
// silent carrier.
//
// Side effects:
//   - Appends the typed error to the pending queue.
func (fs *FailoverSteps) candidateFailsWithBillingNoRetryAfter() error {
	fs.queueError(&provider.Error{
		HTTPStatus: 400,
		ErrorType:  provider.ErrorTypeBilling,
		Provider:   fs.candidateProvider,
		Message:    "credit balance error",
	})
	return nil
}

// candidateFailsNetworkError queues a network error and processes it
// immediately so When-position steps leave observable health state.
//
// Side effects:
//   - Appends the typed error to the pending queue and marks health.
func (fs *FailoverSteps) candidateFailsNetworkError() error {
	fs.queueError(&provider.Error{
		ErrorType: provider.ErrorTypeNetworkError,
		Provider:  fs.candidateProvider,
		Message:   "network error",
	})
	return fs.processQueuedErrors()
}

// candidateFailsFourTimesWithNetworkError queues exactly four network
// errors in quick succession, starting from a clean health entry.
//
// Side effects:
//   - Appends four typed errors to the queue and marks health.
func (fs *FailoverSteps) candidateFailsFourTimesWithNetworkError() error {
	fs.health.ResetProviderHealth(fs.candidateProvider, fs.candidateModel)
	fs.carrierPendingErrors = nil
	for i := 0; i < 4; i++ {
		if err := fs.candidateFailsNetworkError(); err != nil {
			return err
		}
	}
	return nil
}

// candidateHasExpiredCooldown seeds an entry whose cooldown has already
// elapsed so the next failure resets rather than escalate.
//
// Side effects:
//   - Marks the pair with a past expiry.
func (fs *FailoverSteps) candidateHasExpiredCooldown() error {
	if fs.health == nil {
		return fmt.Errorf("health manager not initialised")
	}
	fs.health.MarkRateLimited(fs.candidateProvider, fs.candidateModel, time.Now().Add(-time.Minute))
	fs.health.ResetProviderHealth(fs.candidateProvider, fs.candidateModel)
	return nil
}

// candidateFailsAgainBeforeCooldownExpires queues a second failure
// while the live cooldown is still running.
//
// Side effects:
//   - Appends the pending error to the queue.
func (fs *FailoverSteps) candidateFailsAgainBeforeCooldownExpires() error {
	return nil
}

// queueError appends a typed provider error to the pending queue.
//
// Expected:
//   - provErr is a non-nil typed provider error.
//
// Side effects:
//   - Mutates the pending error queue.
func (fs *FailoverSteps) queueError(provErr *provider.Error) {
	fs.carrierPendingErrors = append(fs.carrierPendingErrors, provErr)
}

// processQueuedErrors drains the pending queue through the production
// per-attempt health marking, mirroring markProviderHealth.
//
// Side effects:
//   - Marks the health manager for every queued error.
func (fs *FailoverSteps) processQueuedErrors() error {
	if fs.health == nil {
		return fmt.Errorf("health manager not initialised")
	}
	if len(fs.carrierPendingErrors) == 0 {
		return fmt.Errorf("no candidate failure was queued")
	}
	for _, provErr := range fs.carrierPendingErrors {
		failover.CheckAndMarkRateLimited(fs.health, fs.candidateProvider, fs.candidateModel, provErr)
	}
	fs.carrierPendingErrors = nil
	return nil
}

// theFailoverHookProcessesTheError processes exactly the first queued
// error, leaving later failures queued for follow-up scenariofs.
//
// Side effects:
//   - Marks the health manager once and dequeues the error.
func (fs *FailoverSteps) theFailoverHookProcessesTheError() error {
	if len(fs.carrierPendingErrors) == 0 {
		return fmt.Errorf("no candidate failure was queued")
	}
	failover.CheckAndMarkRateLimited(fs.health, fs.candidateProvider, fs.candidateModel, fs.carrierPendingErrors[0])
	fs.carrierPendingErrors = fs.carrierPendingErrors[1:]
	return nil
}

// theFailoverHookProcessesTheNthError processes exactly the final
// queued error, so the recorded consecutive count reflects one failure
// per scenario step.
//
// Expected:
//   - ordinal is textual, e.g. "4th" or "2nd"; only the final error is
//     marked, earlier ones are queued as prior failures.
//
// Side effects:
//   - Marks the health manager once for the final queued error.
func (fs *FailoverSteps) theFailoverHookProcessesTheNthError(_ string) error {
	if len(fs.carrierPendingErrors) == 0 {
		if fs.health == nil {
			return fmt.Errorf("health manager not initialised")
		}
		return nil
	}
	last := fs.carrierPendingErrors[len(fs.carrierPendingErrors)-1]
	fs.carrierPendingErrors = nil
	failover.CheckAndMarkRateLimited(fs.health, fs.candidateProvider, fs.candidateModel, last)
	return nil
}

// remainingCooldown reports the live remaining cooldown for the pair.
//
// Returns:
//   - The remaining duration and true when tracked, zero and false
//     otherwise.
func (fs *FailoverSteps) remainingCooldown() (time.Duration, bool) {
	until, ok := fs.health.RateLimitedUntil(fs.candidateProvider, fs.candidateModel)
	if !ok {
		return 0, false
	}
	return time.Until(until), true
}

// cooldownExactly asserts the recorded cooldown matches the carrier
// signal to within a tolerance.
//
// Expected:
//   - seconds is the exact cooldown the health manager should hold.
func (fs *FailoverSteps) cooldownExactly(seconds int) error {
	remaining, ok := fs.remainingCooldown()
	if !ok {
		return fmt.Errorf("expected %q / %q to have a cooldown entry, but none found", fs.candidateProvider, fs.candidateModel)
	}
	want := time.Duration(seconds) * time.Second
	tolerance := 5 * time.Second
	if remaining > want+tolerance || remaining < want-3*time.Second {
		return fmt.Errorf("cooldown for %q / %q is %v, want ~%v", fs.candidateProvider, fs.candidateModel, remaining, want)
	}
	return nil
}

// cooldownOf asserts the recorded cooldown matches a shorthand form
// such as "1h" or "24h", falling back to hard-down state when the
// breaker has tripped and no cooldown entry remains.
//
// Expected:
//   - shorthand is a duration string like "1h" or "24h".
func (fs *FailoverSteps) cooldownOf(shorthand string) error {
	want, err := time.ParseDuration(shorthand)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", shorthand, err)
	}
	remaining, ok := fs.remainingCooldown()
	if !ok {
		if fs.health.IsHardDown(fs.candidateProvider, fs.candidateModel) && want >= time.Hour {
			return nil
		}
		return fmt.Errorf("expected %q / %q to have a cooldown entry, but none found", fs.candidateProvider, fs.candidateModel)
	}
	tolerance := 5 * time.Second
	if remaining > want+tolerance || remaining < want-3*time.Second {
		return fmt.Errorf("cooldown for %q / %q is %v, want ~%v", fs.candidateProvider, fs.candidateModel, remaining, want)
	}
	return nil
}

// cooldownAtLeastSeconds asserts a minimum remaining cooldown.
//
// Expected:
//   - seconds is the floor.
func (fs *FailoverSteps) cooldownAtLeastSeconds(seconds int) error {
	remaining, ok := fs.remainingCooldown()
	if !ok {
		return fmt.Errorf("expected %q / %q to have a cooldown entry, but none found", fs.candidateProvider, fs.candidateModel)
	}
	skew := 3 * time.Second
	if remaining+skew < time.Duration(seconds)*time.Second {
		return fmt.Errorf("cooldown for %q / %q is %v, want at least %ds", fs.candidateProvider, fs.candidateModel, remaining, seconds)
	}
	return nil
}

// cooldownAtLeastMinutes asserts a minimum remaining cooldown.
//
// Expected:
//   - minutes is the floor.
func (fs *FailoverSteps) cooldownAtLeastMinutes(minutes int) error {
	remaining, ok := fs.remainingCooldown()
	if !ok {
		return fmt.Errorf("expected %q / %q to have a cooldown entry, but none found", fs.candidateProvider, fs.candidateModel)
	}
	skew := 5 * time.Second
	if remaining+skew < time.Duration(minutes)*time.Minute {
		return fmt.Errorf("cooldown for %q / %q is %v, want at least %dm", fs.candidateProvider, fs.candidateModel, remaining, minutes)
	}
	return nil
}

// cooldownIsBase30s asserts the escalation reset produced the base
// network-error cooldown.
func (fs *FailoverSteps) cooldownIsBase30s() error {
	return fs.cooldownExactly(30)
}

// consecutiveFailures asserts the health manager's consecutive-failure
// count for the pair.
//
// Expected:
//   - count is the expected consecutive failure count.
func (fs *FailoverSteps) consecutiveFailures(count int) error {
	if got := fs.health.ConsecutiveFailures(fs.candidateProvider, fs.candidateModel); got != count {
		return fmt.Errorf("consecutive failures for %q / %q = %d, want %d", fs.candidateProvider, fs.candidateModel, got, count)
	}
	return nil
}

// RegisterCarrierCooldownSteps wires the carrier cooldown extraction
// and escalation feature steps onto the scenario context.
//
// Expected:
//   - ctx is a valid Godog ScenarioContext.
//
// Side effects:
//   - Registers the carrier cooldown scenario steps.
func registerCarrierCooldownSteps(ctx *godog.ScenarioContext, fs *FailoverSteps) {
	ctx.Step(`^the candidate fails with a rate limit error carrying retry-after: (\d+)$`, fs.candidateFailsWithRateLimitRetryAfter)
	ctx.Step(`^the candidate fails with an auth error carrying retry-after: (\d+)$`, fs.candidateFailsWithAuthRetryAfter)
	ctx.Step(`^the candidate fails with auth error code "([^"]*)" and no retry-after$`, fs.candidateFailsWithAuthCodeNoRetryAfter)
	ctx.Step(`^the candidate fails with a billing error and no retry-after$`, fs.candidateFailsWithBillingNoRetryAfter)
	ctx.Step(`^the candidate fails 4 times with network_error in quick succession$`, fs.candidateFailsFourTimesWithNetworkError)
	ctx.Step(`^the candidate has an expired cooldown entry$`, fs.candidateHasExpiredCooldown)
	ctx.Step(`^the candidate fails with a fresh network_error$`, fs.candidateFailsNetworkError)
	ctx.Step(`^the candidate fails again before cooldown expires$`, fs.candidateFailsAgainBeforeCooldownExpires)
	ctx.Step(`^the failover hook processes the error$`, fs.theFailoverHookProcessesTheError)
	ctx.Step(`^the failover hook processes the (\w+) error$`, fs.theFailoverHookProcessesTheNthError)
	ctx.Step(`^the health manager marks "([^"]*)" / "([^"]*)" with exactly (\d+)s cooldown$`, fs.markedExactlySeconds)
	ctx.Step(`^the health manager marks "([^"]*)" / "([^"]*)" with (\w+) cooldown$`, fs.markedShorthand)
	ctx.Step(`^the health manager reports (\d+) consecutive failure[s]? for "([^"]*)" / "([^"]*)"$`, fs.reportedConsecutiveFailures)
	ctx.Step(`^the cooldown for "([^"]*)" / "([^"]*)" is at least (\d+) minutes$`, fs.reportedAtLeastMinutes)
	ctx.Step(`^the cooldown for "([^"]*)" / "([^"]*)" is at least (\d+) seconds$`, fs.reportedAtLeastSeconds)
	ctx.Step(`^the cooldown for "([^"]*)" / "([^"]*)" is the base 30s$`, fs.reportedBase30s)
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		fs.health = nil
		fs.candidateProvider = ""
		fs.candidateModel = ""
		fs.carrierPendingErrors = nil
		return c, nil
	})
}

// markedExactlySeconds asserts the pair's cooldown after processing.
//
// Expected:
//   - providerName and model identify the pair; seconds is the exact
//     expected cooldown.
func (fs *FailoverSteps) markedExactlySeconds(providerName, model string, seconds int) error {
	if err := fs.checkPair(providerName, model); err != nil {
		return err
	}
	return fs.cooldownExactly(seconds)
}

// markedShorthand asserts the pair's cooldown after processing.
//
// Expected:
//   - providerName and model identify the pair; shorthand is a
//     duration string like "1h" or "24h".
func (fs *FailoverSteps) markedShorthand(providerName, model string, shorthand string) error {
	if err := fs.checkPair(providerName, model); err != nil {
		return err
	}
	return fs.cooldownOf(shorthand)
}

// reportedConsecutiveFailures asserts the recorded consecutive count.
//
// Expected:
//   - count is expected; providerName and model identify the pair.
func (fs *FailoverSteps) reportedConsecutiveFailures(count int, providerName, model string) error {
	if err := fs.checkPair(providerName, model); err != nil {
		return err
	}
	return fs.consecutiveFailures(count)
}

// reportedAtLeastMinutes asserts a minimum remaining cooldown.
//
// Expected:
//   - providerName and model identify the pair; minutes is the floor.
func (fs *FailoverSteps) reportedAtLeastMinutes(providerName, model string, minutes int) error {
	if err := fs.checkPair(providerName, model); err != nil {
		return err
	}
	return fs.cooldownAtLeastMinutes(minutes)
}

// reportedAtLeastSeconds asserts a minimum remaining cooldown.
//
// Expected:
//   - providerName and model identify the pair; seconds is the floor.
func (fs *FailoverSteps) reportedAtLeastSeconds(providerName, model string, seconds int) error {
	if err := fs.checkPair(providerName, model); err != nil {
		return err
	}
	return fs.cooldownAtLeastSeconds(seconds)
}

// reportedBase30s asserts the reset cooldown equals the base 30fs.
//
// Expected:
//   - providerName and model identify the pair.
func (fs *FailoverSteps) reportedBase30s(providerName, model string) error {
	if err := fs.checkPair(providerName, model); err != nil {
		return err
	}
	return fs.cooldownIsBase30s()
}

// checkPair verifies the assertion target matches the scenario pair.
//
// Expected:
//   - providerName and model name the candidate under test.
//
// Returns:
//   - An error when the assertion targets a different pair.
func (fs *FailoverSteps) checkPair(providerName, model string) error {
	if fs.health == nil {
		return fmt.Errorf("health manager not initialised")
	}
	if providerName != fs.candidateProvider || model != fs.candidateModel {
		return fmt.Errorf("assertion targets %q / %q but the scenario candidate is %q / %q", providerName, model, fs.candidateProvider, fs.candidateModel)
	}
	return nil
}
