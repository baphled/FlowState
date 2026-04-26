package harness

import "context"

// CheckWavesIncompleteForTest exposes (h *Harness).checkWavesIncomplete
// so the external waves_test package can drive the wave-fan-in
// validator-call contract without standing up a full streaming loop.
// The harness's runStreamEvaluation integration with this method is
// covered separately by harness_test.go's stream-level specs.
func CheckWavesIncompleteForTest(h *Harness, ctx context.Context, agentID string) string {
	return h.checkWavesIncomplete(ctx, agentID)
}
