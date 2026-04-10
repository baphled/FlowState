package execution

import "github.com/baphled/flowstate/internal/harness"

// Option configures a [Loop] at creation time.
type Option func(*Loop)

// WithValidator sets the validator used to check agent output on each attempt.
//
// Expected:
//   - v is a non-nil harness.Validator.
//
// Returns:
//   - An Option that applies the validator to the loop.
//
// Side effects:
//   - None.
func WithValidator(v harness.Validator) Option {
	return func(l *Loop) {
		l.validator = v
	}
}

// WithCritic sets the critic and LLM provider used for quality review.
//
// Expected:
//   - c is a non-nil harness.Critic.
//   - p is a non-nil criticProvider (satisfies provider.Provider); if nil, critic review is skipped.
//
// Returns:
//   - An Option that applies the critic to the loop.
//
// Side effects:
//   - None.
func WithCritic(c harness.Critic, p criticProvider) Option {
	return func(l *Loop) {
		l.critic = c
		l.criticProvider = p
	}
}

// WithMaxRetries sets the maximum number of evaluation attempts before the loop gives up.
//
// Expected:
//   - n is a positive integer; values <= 0 are treated as 1.
//
// Returns:
//   - An Option that applies the retry limit to the loop.
//
// Side effects:
//   - None.
func WithMaxRetries(n int) Option {
	return func(l *Loop) {
		l.maxRetries = n
	}
}

// WithRetryStrategy overrides the default retry strategy.
//
// Expected:
//   - rs is a non-nil RetryStrategy.
//
// Returns:
//   - An Option that applies the retry strategy to the loop.
//
// Side effects:
//   - None.
func WithRetryStrategy(rs RetryStrategy) Option {
	return func(l *Loop) {
		l.retryStrategy = rs
	}
}

// WithOutcomeObserver registers an observer that is called when the loop terminates.
//
// Expected:
//   - obs is a non-nil OutcomeObserver.
//
// Returns:
//   - An Option that registers the observer on the loop.
//
// Side effects:
//   - None.
func WithOutcomeObserver(obs OutcomeObserver) Option {
	return func(l *Loop) {
		l.observer = obs
	}
}
