# ADR-001: Owning-Layer Boundary — CLI Wires Application Only

## Status

Accepted (18 November 2026). Enforced by `check_code_contract.py` on every changed production Go file. Surfaced as a gate failure during the config-composition packet (PR #292) and corrected in PR #290.

## Context

FlowState's Go CLI follows a three-layer architecture with strict import direction: `cmd/flowstate` imports only `internal/app`, which composes domain ports to concrete adapters. The owning-layer contract (rules/ARCHITECTURE.md) states that `cmd/flowstate` is responsible for "input parsing, output and stable exits" with "no direct engine, provider or store wiring." The `internal/app` package is the composition root: it wires domain ports to concrete adapters explicitly, so the CLI delegates all domain work upward.

Before this ADR, `cmd/flowstate/sessioncmds.go` imported both `internal/session` and `internal/store` directly, bypassing the app layer. This happened organically: the session commands were written before the contract was formalised, and because `sessioncmds.go` was not modified between the contract's introduction and the config-composition packet, the whole-file gate never surfaced the violation.

The violation had three effects: `session.Create` was called with `""` as the `configSHA` parameter (composition hash never persisted, M1-COMPOSITION capability dead), provider and model resolution had no config fallback (every `session new` required explicit flags), and the store parameter type (`*store.Store`) leaked into cmd layer function signatures.

### Why the gate didn't catch it earlier

`check_code_contract.py` uses whole-file checking: if a file is in the PR's changed-file set, the entire file must satisfy the import contract. Pre-existing violations only surface when a packet modifies the offending file. No packet touched `sessioncmds.go` between the contract's introduction and PR #290.

### The discovery

The owning-layer correction was not planned. It surfaced as a gate failure while implementing the config-composition packet (PR #292): modifying `sessioncmds.go` for config resolution caused the whole-file gate to reject the file's pre-existing `internal/session` and `internal/store` imports. The correction was split into a prerequisite packet (PR #290) so the config-composition feature could build on a clean layering base.

## Decision

CLI session commands wire through `internal/app` exclusively via four wrappers in `internal/app/sessioncmd.go`: `CreateSession`, `ListSessions`, `InspectSession`, `CloseSession`. The store type is aliased in app (`type Store = store.Store` in turn_types.go), so the CLI's `*app.Store` parameter resolves to the same underlying type without importing the store package.

The `check_code_contract.py` gate enforces this on every changed production Go file. A violation in any file in the PR's changed-file set blocks the merge.

## Alternatives considered

**Allowing cmd to import session and store directly** would eliminate the wrapper functions and reduce indirection. However, it would also couple the CLI to the store's concrete API surface, make it harder to swap the storage backend (the app layer abstracts this), and violate the documented owning-layer contract without a compensating benefit.

**Moving cmd into internal/** would make the CLI a non-public binary and require restructuring the module. The effort outweighs the benefit for a single-binary CLI.

**Using an interface in cmd instead of a wrapper in app** could abstract the session operations without an app wrapper. However, this would still require cmd to import the session types for the interface's method signatures, which violates the spirit of the owning-layer contract.

## Consequences

**Positive:** The CLI surface is stable: adding internal capabilities doesn't change the CLI's import set. The app layer is the single point where domain ports meet concrete adapters. The whole-file gate catches violations on any touched file, preventing silent accretion.

**Negative:** One-function-per-operation wrappers add boilerplate. The app package's public surface grows with each CLI capability. Cross-layer debugging requires navigating through the app wrapper.

**Risks:** The wrapper functions could accumulate business logic that belongs in the domain layer — mitigated by keeping them as pure delegation plus composition-hash computation.

## References

rules/ARCHITECTURE.md (owning-layer table); PR #290 (owning-layer correction); PR #292 (config composition, the packet that surfaced the violation).
