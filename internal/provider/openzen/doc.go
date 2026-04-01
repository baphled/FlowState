// Package openzen provides an OpenZen curated model gateway provider.
//
// This package provides:
//   - Chat completions via OpenZen's OpenAI-compatible API
//   - Streaming responses
//   - Embedding generation
//   - Model discovery
//
// OpenZen's API is OpenAI-compatible, so this provider reuses the OpenAI Go SDK
// with a custom base URL (https://api.openzen.ai).
package openzen
