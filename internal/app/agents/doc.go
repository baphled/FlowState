// Package agents provides the markdown agent manifest prompts loaded by the
// FlowState agent platform at runtime.
//
// This package handles:
//   - Shipping the canonical agent prompt definitions as markdown files
//   - Providing structural contracts enforced by prompts_test.go to detect
//     drift in anchor directives or Turn Rules sections
package agents
