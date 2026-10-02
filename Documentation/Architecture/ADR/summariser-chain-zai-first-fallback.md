# ADR: SummariserChain Z.AI-first fallback ordering

**Status:** Accepted (2026-10-02)
**Context:** Compaction redesign Phase 1 (chainID `compaction-redesign`).

## Decision

The L2 compaction summariser now dispatches through `engine.SummariserChain`,
an ordered multi-provider fallback: the configured summariser provider first
(Z.AI by default when its credentials are present), Anthropic second, and a
local Ollama with a **configurable** model name as the deterministic last
resort. The Ollama hop is always present so a chain never silently degrades
to an empty summary, and deployments are never hard-wired to `llama3.2` —
the model that produced 179× "model llama3.2 does not exist" production
failures.

## Consequences

- A total chain failure raises a typed `ErrSummariserUnavailable`; the engine
  then degrades explicitly to the bounded truncation fallback with a WARN —
  never a silent `""` no-op that dispatches full raw history.
- The fallback order lives in `internal/config/summariser_config.go`
  (`AppConfig.SummariserChainEntries`) and is overridable via the
  `summariser` YAML block (`provider`, `model`, `ollama_model`,
  `anthropic_model`).
- An empty summariser response counts as a hop failure, so an empty summary
  walks the chain rather than being treated as success.
