// Package ollamacloud provides an Ollama Cloud provider implementation.
//
// Ollama Cloud exposes an OpenAI-compatible API at https://ollama.com/api.
// This provider uses the openaicompat shared layer so it benefits from the
// same streaming, tool-call, and error-classification infrastructure as the
// other OpenAI-compatible providers (OpenAI, OpenZen, Z.AI).
//
// Configuration is read from config.yaml under providers.ollamacloud or the
// OLLAMA_CLOUD_API_KEY environment variable. An optional base_url override
// is supported for testing or custom endpoints.
package ollamacloud
