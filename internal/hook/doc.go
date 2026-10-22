// Package hook provides middleware hooks for request processing.
//
// This package implements a middleware chain pattern for chat requests:
//   - Pre-processing and post-processing of requests
//   - Skill injection into prompts
//   - Learning capture from interactions
//   - Request/response transformation
//
// Hooks are composable and can be chained together to build complex
// request processing pipelines whilst keeping individual concerns separated.
package hook
