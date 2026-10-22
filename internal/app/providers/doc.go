// Package providers builds the provider.Registry used by the FlowState
// composition root.
//
// The package is the canonical home for provider-construction logic per
// [[ADR - App Composition Root Boundary]]: the seven provider clients
// (anthropic, copilot, ollama, ollamacloud, openai, openzen, zai), their
// per-provider environment-variable resolution, the OpenCode-credential
// migration WARN, the Z.AI plan diagnostic, and the default-first
// preference list derived from configuration.
//
// Boundary invariants (per the ADR):
//   - This package imports the provider implementations and config; it
//     does not import internal/app/.
//   - The composition root (internal/app/) calls Build or
//     BuildWithFailures and then orchestrates the result; the providers
//     package itself does not know about the App struct, hook chain, or
//     engine wiring.
package providers
