# Backend architecture overview

FlowState's current milestone is a native Go CLI/headless engine. The accepted
product boundaries and milestone scope live in Dropbox `/FlowState/Dev`; read the
entry point and current packet before work. This guide describes source ownership,
not completed capabilities or runtime qualification.

## Dependency direction

CLI adapters call application use cases. Explicit composition wires consumer-owned
ports to persistence, provider and tool adapters. Domain code owns invariants and
lifecycle; transports and concrete storage remain outside that boundary.

| Area | Source responsibility |
| --- | --- |
| `cmd/flowstate` | Parse input and render stable output/exits |
| `internal/app` | Compose collaborators and application use cases |
| `internal/runtime`, `internal/session` | Lifecycle, identity and ordering policy |
| `internal/provider`, `internal/tool` | Replaceable ports and domain-facing values |
| Provider/auth/storage adapters | Implement external effects through those ports |

See [the enforced code structure contract](../../rules/ARCHITECTURE.md) for exact
ownership and import rules. Existing concrete-store coupling is refactoring debt;
its presence is not permission to add another dependency or a new global.

## Structured provider tools

`internal/provider` owns `ToolDefinition`, complete `ToolCall` values and message
result correlation through `ToolCallID`. `ChatRequest.Validate` rejects malformed
identifiers, non-object JSON arguments/parameters, duplicate call IDs and unmatched
or repeated results. Identifiers use 1–128 ASCII letters, digits, underscores or
hyphens; tool names additionally permit dots for namespaces. Arguments are complete
JSON objects; schema evaluation is outside this
contract. Calls may remain unresolved at the end of history.

`StreamValidator.Accept` validates typed text, complete call, finish and error
events in order, rejecting duplicate calls and events after termination. Use a
fresh zero-value validator for each stream on one consumer goroutine. Failed
validation leaves its state unchanged. Adapters assemble wire fragments before
emitting a complete call. Zero-kind legacy content/error events remain supported.
Existing role/content JSON keys are preserved; new message fields are omitted
when empty. Callers own slices and raw JSON and must avoid concurrent mutation.

The Z.ai OpenAI-compatible coding route (`WithCoding`) validates requests before
HTTP and encodes function definitions, assistant call history and correlated tool
results. `Stream` assembles indexed, interleaved name/argument fragments and emits
complete calls in index order, followed by `StreamEventFinish`. Structured turns
require a finish reason and `[DONE]`; malformed JSON, missing/conflicting metadata,
duplicate IDs, invalid complete arguments and truncated streams terminate with
`*zai.ToolProtocolError`. Calls are withheld until every call validates and the
stream completes. Context cancellation unblocks event sends and closes the body.

The selectable Anthropic coding route rejects structured tools/history before
HTTP. Nonstream coding `Chat` encodes tool requests but rejects tool responses
with `ToolProtocolError`; use `Stream` for tool calls. Legacy text coding streams
remain supported, while unsolicited tool deltas fail closed. Other adapters and
the engine retain their existing behavior. Consumers still validate structured
events with `StreamValidator`; this adapter does not execute tools.

With the Go toolchain and module dependencies installed, run from repository root:

```sh
go run github.com/onsi/ginkgo/v2/ginkgo --procs=2 --randomize-all --randomize-suites --race --fail-on-pending --fail-on-empty --timeout=3m ./internal/provider ./internal/provider/zai
```

## Session tool catalog

`internal/app.NewSessionToolRegistry(workdir)` composes a fresh registry of `echo`,
`fs.read`, `fs.write` and `fs.edit`. It adds `exec.run` only when the existing
`FLOWSTATE_EXEC_ENABLED` opt-in is enabled. Registration grants no permission.
The explicit session-tool use case reuses this constructor and keeps its default
workdir at `SessionWorkdirRoot(dbPath)/sessionID`; an explicit workdir overrides it.

`SessionToolDefinitions(registry, allow)` returns independently owned
`provider.ToolDefinition` values in explicit allowlist order. An empty allowlist
advertises nothing, including exec. Unknown names, duplicate entries and custom
implementations without a qualified builtin schema fail without a partial list.
The function never registers an absent tool to satisfy an allow entry. Compose
the registry before use and avoid concurrent registry mutation.

The object schemas describe actual builtin decoder fields: `path` for reads;
`path` and optional `content` for writes; `path`, `old_text`, optional `new_text`
and `all` for edits; `cmd` and optional string-array `args` for exec. Omitted
content writes an empty file; omitted replacement text deletes the match.
Schemas advertise capabilities; the tool loop and implementations still enforce
permissions, confinement, byte caps and exec policy, including its invocation-time
environment gate. These APIs prepare metadata and do not execute an agent turn.

With the Go toolchain and module dependencies installed, run from repository root:

```sh
go run github.com/onsi/ginkgo/v2/ginkgo --procs=2 --randomize-all --randomize-suites --race --fail-on-pending --fail-on-empty --timeout=3m ./internal/app
```

## Changes and proof

A bounded refactor preserves the public contract, failure behaviour and tests.
Keep named types with their owning package, inject collaborators, document the
public surface and update the relevant guide with the change. Read [documentation
standards](../../rules/DOCUMENTATION.md) and [the test contract](../../rules/BDD_WORKFLOW.md).

Source checks and unit/integration tests do not prove live provider qualification,
T019 acceptance, product readiness, actual AI identity or independent review.
Resolve candidate-bound evidence only from the assigned Dropbox Metadata packet.
