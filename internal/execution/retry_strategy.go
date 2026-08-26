package execution

import "github.com/baphled/flowstate/internal/harness"

// RetryStrategy decides whether the loop should retry after a failed evaluation.
type RetryStrategy interface {
	// ShouldRetry returns true if the loop should attempt another evaluation.
	ShouldRetry(attempt int, result *harness.EvaluationResult) bool
}

// DefaultRetryStrategy retries whenever the output score is below 1.0 and
// the attempt count is below the configured maximum.
type DefaultRetryStrategy struct {
	MaxRetries int
}

// ShouldRetry returns true when the attempt count has not exceeded MaxRetries
// and the last result did not achieve a perfect score.
//
// Expected:
//   - attempt is a 1-based count of the attempt just completed.
//   - result is the EvaluationResult from that attempt.
//
// Returns:
//   - true if another attempt should be made; false otherwise.
//
// Side effects:
//   - None.
func (s DefaultRetryStrategy) ShouldRetry(attempt int, result *harness.EvaluationResult) bool {
	if attempt >= s.MaxRetries {
		return false
	}
	return result.FinalScore < 1.0
}
