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

## Changes and proof

A bounded refactor preserves the public contract, failure behaviour and tests.
Keep named types with their owning package, inject collaborators, document the
public surface and update the relevant guide with the change. Read [documentation
standards](../../rules/DOCUMENTATION.md) and [the test contract](../../rules/BDD_WORKFLOW.md).

Source checks and unit/integration tests do not prove live provider qualification,
T019 acceptance, product readiness, actual AI identity or independent review.
Resolve candidate-bound evidence only from the assigned Dropbox Metadata packet.

## M2 agent turn loop

`flowstate session agent <id>` runs a bounded multi-round agent turn:
the session's provider (zai glm coding route) receives the
allowlisted tool definitions (fs.read/fs.write/fs.edit plus exec.run
behind its env double gate) with hand-written JSON schemas, and its
complete tool calls execute through the same deny-by-default tool
loop that powers `session tool`. Each round appends the assistant
message with its tool calls and the correlated tool-role results
(truncated to 4 KiB and marked `truncated:true` when clipped; the
full result stays in the persisted `tool.result` event). The turn
stops when the model answers without tool calls, or earlier on the
round cap (12), the byte budget (default 50 000, counting content
plus tool results) or a terminal provider error. Deny-by-default
allowlist, workdir confinement and the exec allow-list all remain
enforced by the underlying loop; the agent layer only orchestrates.
