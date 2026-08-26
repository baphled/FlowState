// Package turn provides the canonical Turn resource for FlowState's
// post-then-poll wire shape.
//
// This package handles:
//   - Producing one Turn per user-message-driven streaming pass with a stable UUID
//   - Tracking turn status transitions from running to completed or failed
//   - Snapshotting the model and provider pair the turn ran under
//   - Persisting engine-emitted messages during the turn via an in-memory Registry
//   - Propagating turn IDs through context via IDFromContext and WithTurnID
package turn
