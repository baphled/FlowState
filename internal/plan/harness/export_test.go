package harness

import "context"

// CheckWavesIncompleteForTest exposes (h *Harness).checkWavesIncomplete
// so the external waves_test package can drive the wave-fan-in
// validator-call contract without standing up a full streaming loop.
// The harness's runStreamEvaluation integration with this method is
// covered separately by harness_test.go's stream-level specs. The
// noToolCall flag is fixed to false here (the legacy validator-contract
// specs don't model tool-call presence); the directive-feedback path is
// covered by BuildWaveFeedbackForTest and the stream-level specs.
func CheckWavesIncompleteForTest(h *Harness, ctx context.Context, agentID string) string {
	return h.checkWavesIncomplete(ctx, agentID, false)
}

// BuildWaveFeedbackForTest exposes buildWaveFeedback so the external
// waves_test package can pin the directive-vs-passive feedback shape
// without standing up the full streaming loop. The noToolCall flag
// selects the synthesis-hang directive variant.
func BuildWaveFeedbackForTest(stage WaveStage, missing []string, err error, noToolCall bool) string {
	return buildWaveFeedback(stage, missing, err, noToolCall)
}
