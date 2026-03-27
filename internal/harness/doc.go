// Package harness provides generic interfaces for agent output validation,
// quality assessment, and iterative improvement loops.
//
// This package defines the abstraction layer for harness components that any
// agent can use. The planning harness (internal/plan/) is the first concrete
// implementation; future agents will implement these interfaces for their
// own output types.
//
// Harness layers (configurable per agent):
//   - Validator: checks output against structural and content rules
//   - Critic: LLM-based quality assessment with rubric scoring
//   - Voter: multi-variant generation and best-output selection
//   - Evaluator: orchestrates the validate-critique-vote loop
//
// Loop pattern (configurable per agent):
//   - Coordinator delegates to Writer agent
//   - Writer produces output through harness validation
//   - Reviewer evaluates output independently
//   - Reject-regenerate cycle with feedback until acceptance
//
// Current implementations:
//   - internal/plan/: ValidatorChain, LLMCritic, ConsistencyVoter satisfy
//     Validator, Critic, Voter respectively (via structural compatibility)
//   - internal/plan/harness.go: Harness satisfies Evaluator
//   - internal/streaming/harness_streamer.go: routes harness-enabled agents
package harness
