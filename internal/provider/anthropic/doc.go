// Package anthropic provides an Anthropic Claude API provider implementation.
//
// This package implements the provider.Provider interface for Anthropic:
//   - Chat completions with Claude models
//   - Streaming response support
//   - Tool/function calling integration
//
// Note: Anthropic does not support embeddings; attempting to use embedding
// functionality will return ErrNotSupported.
package anthropic
