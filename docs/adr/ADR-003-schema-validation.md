# ADR-003: Schema Validation of --json Output

## Status

Accepted (18 November 2026). Landed as PR #295. Completes the last unchecked item in CLI-AND-API-CONTRACT §5's done checklist.

## Context

### The contract requirement

CLI-AND-API-CONTRACT §5's done checklist states: "--json output of every command validates against generated schemas." Before PR #295, no schema validation existed: the --json output shapes were defined only by the Go code that produced them (`sessionJSON` in sessioncmds.go, the `writeJSON` event envelope in sessionrun.go), with no independent verification that the emitted JSON matched the expected structure.

### The risk

Without schema validation, a refactoring could silently change a field name, drop a required field, or change a value type — and no test would fail. The output would still be valid JSON but semantically wrong. This is particularly risky for the event envelope (7 fields) and the session record (8 fields), which are the primary machine-parseable interfaces for downstream consumers.

## Decision

Hand-author JSON Schema documents in `schemas/cli.json` (one combined file with a key per command output shape) and validate actual command outputs against them in the e2e suite using `github.com/santhosh-tekuri/jsonschema/v6` as a test-only dependency.

### Schema inventory

| Schema key | Shape | Required fields |
|---|---|---|
| `session-new` | object | `id`, `status` |
| `session-cancel` | object | `id`, `status` |
| `session-list` | array of session records | 8 fields each |
| `session-events` | array of event envelopes | 7 fields each |
| `session-replay` | array of event envelopes | 7 fields each |
| `receipt-validate` | array of verdict records | `path`, `verdict` (enum: valid/invalid) |

### The event envelope schema

The event envelope is the most constrained: it requires all 7 projection fields (`session_id`, `seq`, `created_at_utc`, `actor`, `type`, `payload`, `payload_sha256`) with `payload` typed as `object`, `seq` as `integer`, and all other string fields as `string`. `additionalProperties` is false.

## Alternatives considered

### invopop/jsonschema reflection (attempted, abandoned)

Reflection-based generation (schemagen.go with mirror types) was abandoned: the generator imported `github.com/invopop/jsonschema` in the cmd package, violating the owning-layer contract; moving it to internal/app would require exporting wire types from cmd to app, inverting the dependency; and the mirror types were flagged as unused by golangci-lint after the generator was removed.

### Golden-file comparison

Committed fixtures compared byte-for-byte were rejected: updating fixtures on every intentional output change is high-maintenance and the comparison cannot distinguish intentional from accidental changes.

### Protobuf-based schema

Generating Go types and JSON Schema from protobuf definitions was rejected as over-engineering for a single-binary CLI with no cross-language consumers.

## Consequences

### Positive

- The e2e suite proves that every command's --json output matches the documented structure
- Schema drift is detected immediately (the validation spec fails on any field addition, removal, or type change)
- The schemas serve as executable documentation of the CLI's machine-parseable interfaces
- The test-only dependency (santhosh-tekuri/jsonschema) adds no production overhead

### Negative

- The schemas are hand-authored and must be updated manually when the output shape changes
- The santhosh-tekuri/jsonschema dependency adds ~12 lines to go.sum
- Schema validation runs at test time only (no runtime validation), so a runtime bug could still emit non-conforming output

### Risks

- Schema drift between the committed schemas and the actual output could go undetected if the e2e specs are not run in CI — mitigated by wiring check-schemas into make check and the ci-next lint job
- The schemas validate structure (field names, types, required) but not value semantics (e.g., a timestamp string that is not valid RFC3339 passes validation) — semantic validation is covered by the event-content verification in the store's replay path

## References

CLI-AND-API-CONTRACT.md section 5 (done checklist); PR #295 (schema validation e2e specs); santhosh-tekuri/jsonschema/v6 (test dependency).
