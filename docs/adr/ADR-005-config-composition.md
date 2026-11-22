---
id: adr-config-driven-session-composition
title: ADR - Config-Driven Session Composition
type: adr
status: accepted
adr_group: Go CLI
created: 2026-11-18
modified: 2026-11-18T21:40
deciders:
  - Engineering Team
consulted:
  - Architecture Review Board
informed:
  - FlowState Contributors
tags:
  - type/adr
  - project/flowstate
  - topic/architecture
  - topic/adr
  - topic/configuration
  - topic/composition-root
  - topic/security
  - flowstate/cli
  - flowstate/app
  - flowstate/adr
  - timeline/november-2026
aliases:
  - Config-Driven Session Composition
  - flowstate.json Composition
  - M1-COMPOSITION
---

# ADR - Config-Driven Session Composition

## Status

**Accepted** — 2026-11-18. Landed as PR #292 (packet B) with the owning-layer correction as its prerequisite (PR #290,
  packet A). Live-verified against `z.ai/glm-5.3-flash` in the tool-loop dogfood. The composition-root principle this
  feature exercises is stated at prototype scale in [[ADR - App Composition Root Boundary]]; the provider routing
  field it interacts with is [[ADR - Z.AI Plan Routing as First-Class Field]].

## Context

### The flag-only era

Before config composition, every `session new` required explicit `--provider` and `--model` flags, and there was no
  way to set default budgets, storage paths, or tool allowlists. The CLI's `config_sha256` column existed in the
  session schema but was always passed as an empty string — dead plumbing from the M1-COMPOSITION design that was
  never wired. The owning-layer violation documented in [[ADR - Owning-Layer Boundary: CLI Wires Application Only]]
  was both cause and symptom: with the CLI wiring the store and session domain directly, there was no app-layer choke
  point where config resolution could live.

### The contract requirement

The Dropbox `CLI-AND-API-CONTRACT.md` §4 states:

> One JSON config file: providers (auth ref, base URL), default provider/model, tool allowlist, budget caps, storage
  path. config_sha256 recorded per session; identical config + identical task MUST produce identical wiring
  (M1-COMPOSITION). Secrets via environment references only — never in the config file.

Three properties follow: a single config file supplies defaults; the recorded hash proves wiring determinism; and the
  config file is credential-free by construction.

### The composition-hash requirement

The `config_sha256` column is the determinism pin. Two sessions created from the same config file with the same task
  and flags must produce the same hash; any wiring change (provider, model, allowlist, storage path) must change it.
  The hash is what makes "identical config + identical task → identical wiring" checkable rather than aspirational.

## Problem

1. **Flag fatigue and drift.** Without defaults, every invocation repeats provider/model flags; scripts accumulate
  copy-pasted flag sets that drift apart.
2. **No determinism proof.** Without a recorded hash, "same config, same wiring" is a claim, not a property.
3. **Credential leakage risk.** A config file that *can* hold keys *will* hold keys; the format must make secrets
  structurally impossible.
4. **Allowlist bypass by accident.** The bounded exec loop's tool gating was flag-driven only; projects could not
  commit a standard allowlist that all their sessions inherit.

## Decision

**`flowstate.json` (explicit `--config <path>`, else the ambient `./flowstate.json` when present in the working
  directory, else zero-config behaviour identical to today) supplies session defaults. Explicit flags strictly
  override config; config overrides built-in defaults. The resolved wiring is pinned by a SHA-256 composition hash
  recorded on the session.**

### Config schema

```json
{
  "default_provider": "z.ai",
  "default_model": "glm-5.3-flash",
  "default_budget": 20000,
  "storage_path": "./sessions.db",
  "tool_allowlist": ["exec.run", "fs.read", "fs.write"]
}
```

### Precedence

| Setting | Built-in default | Config override | Flag override |
|---|---|---|---|
| provider/model | none (flag required pre-#292) | `default_provider` / `default_model` | `--provider` / `--model` |
| budget | flag required | `default_budget` | `--budget` |
| storage path | `./flowstate.db` | `storage_path` | `--db` |
| tool allowlist | `exec.run` only | `tool_allowlist` | `--allow` |

Flags win over config; config wins over nothing (there are no other defaults). A config file that sets nothing a flag
  sets changes nothing that invocation.

### Composition hash

The hash is SHA-256 over a sorted-key, whitespace-free JSON object:

```json
{"config_source":"default","db_path":"./flowstate.db","harness":"flowstate-cli","model":"glm-5.3-flash","provider":"z.ai","tool_allowlist":["exec.run"]}
```

- `config_source` is the verbatim explicit `--config` path, the ambient default, or `"none"`.
- `db_path` is `filepath.Clean`-resolved.
- `tool_allowlist` is sorted before hashing.
- Go's `json.Marshal` of a map emits sorted keys with no whitespace, so canonicalisation is a property of the encoder,
  not an extra step.
- Any wiring change produces a different hash; identical wiring always produces an identical hash. Note that **path
  sensitivity is by design**: identical content at two different explicit paths hashes differently, because
  `config_source` records the path — the hash pins provenance, not just content.

### Secret rejection

The loader rejects any object key matching `(?i)key|token|secret|password` at any nesting depth, **before** field
  decoding. A config containing `{"openai_api_key": "..."}` exits 4 (`config_invalid`) without the value ever being
  unmarshalled. Auth is env-only: the z.ai provider reads `ZAI_API_KEY` via `os.Getenv` in its `NewFromEnv`. The
  config file is structurally incapable of holding credentials.

### Unknown-field rejection

The loader uses `json.Decoder.DisallowUnknownFields()`. A misspelled or extraneous field exits 4. This prevents silent
  drift — a config with `defaultProvider` (camelCase) fails loudly rather than being silently ignored, which would
  produce zero-config behaviour while the operator believes config is active.

### Where resolution lives

All resolution (ambient lookup, precedence merge, hash computation) lives in `cmd/flowstate/config.go` and flows into
  the app-layer `CreateSession` wrapper — the choke point restored by the owning-layer correction. App contributes the
  harness name (`app.DefaultHarness` = `flowstate-cli`); cmd contributes everything else. The split keeps the hash's
  inputs in the layer that owns the flags, while the session record write goes through the owning layer.

## Consequences

### Positive

1. **`session new --task ...` works without flags** when `flowstate.json` is present — the contract's ergonomics goal.
2. **Determinism is provable.** `config_sha256` turns "same config, same wiring" into a byte-comparable property;
  drift investigations query sessions by hash.
3. **Credentials are structurally excluded.** The pre-decode secret scan means even a malicious config cannot smuggle
  keys into the session record or the hash.
4. **The allowlist is project-committable.** A repo's `flowstate.json` pins its sessions' tool surface; the bounded
  exec loop inherits the same gating as `--allow`.
5. **Zero-config behaviour is untouched.** Absent config, the CLI behaves exactly as before — verified by the
  pre-existing e2e suite passing unchanged.

### Negative

1. **Ambient lookup is working-directory-sensitive.** Two invocations with identical flags from different directories
  can compose differently if only one has an ambient `flowstate.json`. Documented in the overview; explicit `--config`
  removes the ambiguity.
2. **Hash is provenance-sensitive.** Identical content at different paths hashes differently. This is intended (the
  hash pins where wiring came from) but surprises users who expect content-only hashing.
3. **All-or-nothing secret scan.** The regex rejects any key *containing* the pattern — a legitimate field named
  `keyspace_config` would be rejected. The false-positive cost is an early, loud error, which is the right failure
  direction.
4. **Unknown-field strictness taxes config evolution.** Adding a new config field requires shipping the schema change
  first; older binaries reject newer configs rather than ignoring new fields. Fail-loud beats fail-silent for wiring,
  but it couples config and binary versions.

### Neutral

1. JSON (not TOML/YAML) was already the CLI's wire format; config in JSON keeps one parsing path.
2. The composition hash includes `harness` — constant today (`flowstate-cli`), present so future harness variants
  cannot silently share hashes.
3. The ambient lookup checks only the process working directory — no parent walk, no XDG; see Alternatives D.

## Alternatives considered

### A. Environment variables for all composition

**Rejected.** The contract explicitly requires "one JSON config file", and environment variables cannot express the
  tool allowlist (a list value) without delimited-string hacks. Env vars also leak into process listings and shell
  history; a committed config file is reviewable, diffable, and version-controllable per project. Env remains the auth
  channel (API keys), which is the only place credentials belong.

### B. TOML or YAML config format

**Rejected.** The CLI's wire format is JSON; a second format adds a parser dependency and a second mental model.
  JSON's lack of comments is a feature here — stale comment drift cannot disagree with active values. The prototype's
  YAML provider file (`providers.yaml`) was fswt heritage, not the Go CLI contract; it is no longer read.

### C. Config stored in SQLite alongside sessions

**Rejected.** Circular dependency: the database path comes from the config, so config must be readable before the
  store opens. Config is also project-level state (committed to the repo) while sessions are runtime state
  (gitignored); conflating them breaks both lifecycles.

### D. Ambient config discovery beyond the working directory

**Rejected.** Walking parent directories or consulting XDG paths would make composition depend on invocation location
  in ways the composition hash cannot disambiguate — the whole point of `config_source` in the hash is that the
  wiring's origin is recorded verbatim. One lookup location, recorded in the hash, keeps determinism airtight.

### E. Partial config (merge with built-in defaults field-by-field)

**Rejected.** Field-by-field merging makes "what wiring did I get?" depend on which fields are present — the hash
  would encode the merge result but not the intent, and two different configs could produce identical wiring.
  All-or-nothing resolution keeps the mapping from config content to wiring injective.

## Cross-links

### Sibling ADRs (Go CLI group)

- [[ADR - Owning-Layer Boundary: CLI Wires Application Only]] — the prerequisite correction; config resolution flows
  through the restored app choke point.
- [[ADR - Session Run Lifecycle]] — budget defaults from config feed the fresh-budget resume rule.
- [[ADR - Schema Validation of JSON Output]] — same philosophy: the config format is strict (`DisallowUnknownFields`)
  just as the wire envelope is (`additionalProperties: false`).
- [[ADR - Crash-Recovery Detection and Recovery]] — recovery composes with config-driven budgets (fresh budget on the
  recovered run).

### Prototype ADRs

- [[ADR - App Composition Root Boundary]] — the composition-root principle; the Go CLI's config file is the
  per-invocation complement to the prototype's compile-time composition.
- [[ADR - Z.AI Plan Routing as First-Class Field]] — provider routing (plan selection) remains env/flag driven; config
  supplies the provider default, not the plan.

## Sources

- `cmd/flowstate/config.go:33` — secret-key regex `(?i)key|token|secret|password`.
- `cmd/flowstate/config.go:76` — `dec.DisallowUnknownFields()`.
- `cmd/flowstate/config.go:91-107` — `rejectSecretKeys` at any depth, pre-decode.
- `cmd/flowstate/config.go:181-212` — canonical composition map (sorted keys via `json.Marshal` of a map) and SHA-256.
- `cmd/flowstate/config.go:184-208` — `sort.Strings` on the allowlist; `filepath.Clean` on the db path.
- `internal/session/storeext.go:147` — `DefaultHarness` = `flowstate-cli`.
- Dropbox `CLI-AND-API-CONTRACT.md` §4 — the contract sentence this ADR implements.
- PR #290 (prerequisite), PR #292 (landing); live dogfood receipt against `z.ai/glm-5.3-flash`.

## Related Plans

- *Completed:* PR #292 plus its owning-layer prerequisite; live dogfood verification.
- *Possible follow-up:* config-schema `format` assertions (valid provider names, positive budgets) surfaced in the
  validation error text; deferred until a real misconfiguration report warrants it.
