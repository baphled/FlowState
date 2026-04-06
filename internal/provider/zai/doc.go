// Package zai provides a Z.AI (Zhipu AI) provider implementation.
//
// This package provides:
//   - Chat completions via Z.AI's OpenAI-compatible API
//   - Streaming responses
//   - Embedding generation
//   - Model discovery
//
// Z.AI's API is OpenAI-compatible, so this provider reuses the OpenAI Go SDK
// with a custom base URL (https://api.z.ai/api/paas/v4).
package zai
