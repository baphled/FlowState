// Package questionrequest provides the in-process registry that holds
// suspended clarifying questions raised by the question tool while the
// agent waits for the operator's answer.
//
// This package handles the blocking-question lifecycle for FlowState:
//   - Registering a pending question with a unique request id
//   - Blocking Wait until the operator answers via the API or the
//     timeout fires
//   - Resolving pending questions from the HTTP answer endpoint
//   - Surfacing the pending question set per session
//
// The registry mirrors internal/permissionrequest's locked-pre-check
// + insert pattern and is intentionally consumer-agnostic.
package questionrequest
