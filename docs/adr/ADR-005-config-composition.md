# ADR-005: Config-Driven Session Composition

## Status

Accepted (18 November 2026). Landed as PR #292 (packet B) with the owning-layer correction prerequisite (PR #290, packet A). Live-verified with z.ai/glm-5.3-flash in the tool-loop dogfood.

## Context

### The flag-only era

Before config composition, every `session new` required explicit `--provider` and `--model` flags, and there was no way to set default budgets, storage paths, or tool allowlists. The CLI's `config_sha256` column existed in the session schema but was always passed as an empty string — dead plumbing from the M1-COMPOSITION design that was never wired.

### The contract requirement

CLI-AND-API-CONTRACT §4 states: "One JSON config file: providers (auth ref, base URL), default provider/model, tool allowlist, budget caps, storage path. config_sha256 recorded per session; identical config + identical task MUST produce identical wiring (M1-COMPOSITION). Secrets via environment references only — never in the config file."

### The composition hash requirement

The `config_sha256` column is the composition pin: it proves that the session's wiring (provider, model, allowlist, storage path) is deterministic given the config file's content. Two sessions created from the same config file with the same task and flags must have the same hash. Two sessions with different configs must have different hashes.

## Decision

`flowstate.json` (explicit `--config <path>`, else the ambient `./flowstate.json` in the working directory when present, else zero-config identical to today) supplies session defaults. The schema is:

```json
{
  "default_provider": "z.ai",
  "default_model": "glm-5.3-flash",
  "default_budget": 20000,
  "storage_path": "./sessions.db",
  "tool_allowlist": ["exec.run", "fs.read", "fs.write"]
}
```

### Precedence and override

Explicit flags strictly override config values. Config values override the zero-config defaults. This means:
- `--provider z.ai --model glm-5.3-flash` overrides any config `default_provider`/`default_model`
- `--budget 50000` overrides any config `default_budget`
- `--db ./other.db` overrides any config `storage_path`
- `--allow fs.read` overrides any config `tool_allowlist`

### Composition hash

The hash is SHA-256 over a sorted-key, whitespace-free JSON object:
```json
{"config_source":"default","db_path":"./flowstate.db","harness":"flowstate-cli","model":"glm-5.3-flash","provider":"z.ai","tool_allowlist":["exec.run"]}
```

`config_source` is the explicit `--config` path, the ambient default, or `"none"`. The `db_path` is `filepath.Clean`-resolved. The `tool_allowlist` is sorted. The hash is deterministic: identical inputs always produce identical hashes; any wiring change (provider, model, allowlist, storage path) produces a different hash.

### Secret rejection

The config loader rejects any object key matching `(?i)key|token|secret|password` at any depth, before field decoding. This ensures that credentials never enter the composition hash or the session record. Auth is env-only: `ZAI_API_KEY` via `os.Getenv` in the zai provider's `NewFromEnv`.

Unknown fields are rejected via `DisallowUnknownFields()`.

## Alternatives considered

**Environment variables** for all composition were rejected: the contract explicitly requires "one JSON config file", and env vars don't support the tool allowlist (a list value). The config file is also self-documenting and version-controllable per project.

**TOML or YAML** were rejected in favour of JSON: the CLI already uses `encoding/json` for wire output, JSON has no comments (preventing stale-comment drift), and JSON is the lingua franca for machine-parseable config. The internal/config package's YAML support is fswt heritage, not the Go CLI contract.

**Config in SQLite** alongside sessions would create a circular dependency (the db path comes from the config); the config must be readable before the database is opened.

## Consequences

### Positive

- `session new --task ...` works without flags when flowstate.json is present
- `config_sha256` proves deterministic wiring and enables drift detection
- Secret-bearing keys are rejected before they enter the session record
- The tool allowlist from config drives the bounded exec loop with the same gating as --allow

### Negative

- The config file must be present in the working directory (or explicitly passed) for config-driven defaults to work
- The composition hash changes when any config field changes, even if the session's actual behaviour is unchanged
- Two config files with identical content at different paths produce different hashes because `config_source` records the verbatim path

### Risks

- A malformed config file (invalid JSON, unknown fields, negative budget, secret keys) exits 4 before the session is created — this is correct behaviour but may surprise users who expect partial-config support
- The ambient ./flowstate.json lookup means a different working directory can produce different session wiring for the same explicit flags — documented in the overview

## References

- CLI-AND-API-CONTRACT.md section 4
- PR #292 (config composition), PR #290 (owning-layer correction)
