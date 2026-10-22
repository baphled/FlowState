// Package harness provides plan evaluation orchestration for FlowState.
//
// This package implements the plan quality harness that validates AI-generated
// plans before they are accepted. It provides:
//   - Harness: the main evaluation orchestrator with retry logic.
//   - LLMCritic: an LLM-based plan quality evaluator.
//   - ConsistencyVoter: multi-variant consistency scoring.
//   - Aggregator: stream chunk aggregation.
package harness
