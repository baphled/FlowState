# ADR-005: Config-Driven Session Composition

## Status

Accepted (18 November 2026). Landed as PR #292 with the owning-layer correction prerequisite (PR #290). Live-verified with z.ai/glm-5.3-flash in the tool-loop dogfood.

## Context

Before config composition, every `session new` required explicit --provider and --model flags. The CLI's config_sha256 column existed in the session schema but was always passed as an empty string. CLI-AND-API-CONTRACT section 4 requires a JSON config file with default provider/model, tool allowlist, budget caps and storage path, plus config_sha256 recorded per session so identical config + identical task produces identical wiring.

## Decision

`flowstate.json` supplies session defaults. Explicit flags strictly override. `session new` persists config_sha256: the SHA-256 of the canonical effective composition (sorted-key JSON documented in the architecture overview). Unknown fields and secret-bearing keys exit 4. The schema is {default_provider, default_model, default_budget, storage_path, tool_allowlist}.

The canonical hash uses sorted-key, whitespace-free JSON with filepath.Clean-resolved db_path and sorted tool_allowlist. Two config files with the same semantic content but different key orderings produce the same hash. The hash is deterministic and drift-detectable.

Secret keys (matching key|token|secret|password case-insensitively at any depth) are rejected before field decoding, ensuring credentials never enter the composition hash. Auth is env-only (ZAI_API_KEY via os.Getenv).

## Consequences

Positive: session new works without flags when flowstate.json is present. config_sha256 proves deterministic wiring. Secret-bearing keys rejected. Config tool_allowlist drives the bounded exec loop.

Negative: config file must be present for defaults. Hash changes on any config field change. JSON key-order sensitivity.

Risks: malformed config exits 4 before session creation (correct but may surprise users expecting partial-config support). Ambient lookup means different working directories can produce different wiring.

## References

CLI-AND-API-CONTRACT section 4, PR #292.
