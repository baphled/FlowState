// Package shared provides common helpers for FlowState provider implementations.
//
// This package contains pure, provider-agnostic utilities that operate on the
// core provider types defined in internal/provider. All helpers in this package
// depend solely on standard library types and internal/provider types — no
// provider-specific SDK imports are permitted here.
//
// Responsibilities:
//   - Converting slices of provider.Message to intermediate role/content pairs
//     that each provider can then map to its own wire format
//   - Providing a common foundation to eliminate duplicated message-mapping
//     loops across the Anthropic, OpenAI-compatible, GitHub Copilot, and Ollama
//     provider implementations
//
// This package must remain free of provider SDK dependencies so that all four
// providers can import it without introducing import cycles.
package shared
