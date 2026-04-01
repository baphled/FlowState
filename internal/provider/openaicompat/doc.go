// Package openaicompat provides shared helpers for OpenAI-compatible provider implementations.
//
// This package centralises message conversion, tool conversion, and streaming logic for all providers
// using the OpenAI-compatible API (including openai, zai, and openzen). It ensures:
//   - Consistent construction of chat messages and tool call parameters
//   - Unified streaming response handling, including tool call support
//   - Elimination of duplicated logic across providers
//   - Strict adherence to FlowState's code style and documentation conventions
//
// Responsibilities:
//   - Convert FlowState provider.Message and provider.Tool types to OpenAI API types
//   - Parse and extract tool call arguments from streaming responses
//   - Accumulate and emit streaming content and tool call events
//   - Provide testable, modular helpers for all shared logic
//
// This package must not introduce new external dependencies and must not modify provider types.
// All exported and unexported functions must include full docblocks in British English.
//
// See openaicompat.go for implementation details.
package openaicompat
