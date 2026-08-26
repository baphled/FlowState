// Package provider defines the LLM provider interface and registry.
//
// This package provides:
//   - Common interface for LLM providers (chat, streaming, embeddings)
//   - Provider registry for managing multiple providers
//   - Failback chain for automatic provider fallback
//   - Message and request types for provider communication
//
// Concrete provider implementations (Anthropic, OpenAI, Ollama) are in
// sub-packages and implement the Provider interface defined here.
package provider
