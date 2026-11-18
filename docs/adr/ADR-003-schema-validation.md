# ADR-003: Schema Validation of --json Output

## Status

Accepted (18 November 2026). Landed as PR #295. Completes the last unchecked item in CLI-AND-API-CONTRACT §5's done checklist.

## Context

The contract requires --json output of every command to validate against generated schemas. Without schema validation, a refactoring could silently change a field name, drop a required field, or change a value type — and no test would fail. The output would still be valid JSON but semantically wrong.

The highest-risk outputs are the event envelope (7 fields) and the session record (8 fields), which are the primary machine-parseable interfaces for downstream consumers.

Three approaches were considered. Hand-authored JSON Schema documents committed as files and validated in the e2e suite using a real JSON Schema validator was chosen. invopop/jsonschema reflection-based generation from Go wire structs was attempted first but abandoned because the generator imported `github.com/invopop/jsonschema` in the cmd package, violating the owning-layer contract. Golden-file comparison was rejected because it requires updating fixtures on every intentional output change and doesn't distinguish intentional from accidental changes.

## Decision

Hand-author JSON Schema documents in `schemas/cli.json` (one combined file with a key per command output shape) and validate actual command outputs against them in the e2e suite using `github.com/santhosh-tekuri/jsonschema/v6` as a test-only dependency.

Six schemas cover the CLI output shapes: `session-new` (id + status), `session-cancel` (id + status), `session-list` (array of 8-field session records), `session-events` (array of 7-field event envelopes), `session-replay` (same event array shape), `receipt-validate` (array of verdict records with path + verdict enum). All use `additionalProperties: false` and explicit `required` arrays.

The e2e specs include positive controls (each command's output passes) and a negative control (a tampered event envelope missing `payload_sha256` fails).

## Alternatives considered

invopop/jsonschema reflection-based generation was attempted first (schemagen.go with mirror types in types.go) but abandoned because the generator imported `github.com/invopop/jsonschema` in the cmd package, violating the owning-layer contract. The mirror types were also flagged as unused by golangci-lint after the generator was removed.

Protobuf-based schema generation was rejected as over-engineering for a single-binary CLI with no cross-language consumers. Golden-file comparison was rejected for high maintenance cost and poor intentional-vs-accidental change discrimination.

## Consequences

Positive: the e2e suite proves that every command's --json output matches the documented structure. Schema drift is detected immediately. The schemas serve as executable documentation. The test-only dependency adds no production overhead.

Negative: the schemas are hand-authored and must be updated manually when the output shape changes. Schema validation runs at test time only. The santhosh-tekuri/jsonschema dependency adds ~12 lines to go.sum.

Risks: schema drift could go undetected if the e2e specs are not run in CI — mitigated by wiring the validation into the required CI chain. The schemas validate structure but not value semantics — semantic validation is covered by the store's replay path.

## References

CLI-AND-API-CONTRACT.md section 5, PR #295, santhosh-tekuri/jsonschema/v6.
