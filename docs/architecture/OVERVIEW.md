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
| `internal/server` | HTTP transport adapter — ratified ahead of implementation (ADR-007); owns the `net/http` surface cmd is banned from |
| `internal/provider`, `internal/tool` | Replaceable ports and domain-facing values |
| Provider/auth/storage adapters | Implement external effects through those ports |

See [the enforced code structure contract](../../rules/ARCHITECTURE.md) for exact
ownership and import rules. Existing concrete-store coupling is refactoring debt;
its presence is not permission to add another dependency or a new global.

## Serve transport (S1–S4)

`flowstate serve` exposes the session surface over HTTP from `internal/server`:
`GET /health`, `GET /sessions[?status=…]`, `GET /sessions/{id}` (session
fields with embedded `events`), the write endpoints `POST /sessions/{id}/close`
and `POST /sessions/{id}/cancel` (200 with the terminal session row; a terminal
session answers 409 `session_terminal` — closing/cancelling is not idempotent),
and `GET /sessions/{id}/doctor` reusing the CLI `session-doctor` output shape,
wired through narrow `server.Ports`; no store
type crosses into transport and `cmd` only builds the closures and listener.
S3 adds the budget and steering resources: `GET /sessions/{id}/budget`
(session-budget shape), `POST /sessions/{id}/budget` `{"pool":N}` anchoring
the shared pool through the run budget seam (positive pool only; the
response is the updated overview), and `POST /sessions/{id}/message`
`{"from","body"}` (session-message shape) — steering failures, including
unknown sessions, answer 400 `usage` because the app layer wraps them into
`ErrUsage` before the shared code table.
S4 adds the path-based swarm and receipt endpoints — `POST /swarm/validate`
`{"path"}`, `POST /swarm/run` `{"parent","path","budget"}` (unknown parent 404,
budget stops 409, gate-not-accepted surfaces the app's unwrapped error → 500) and
`POST /receipt/validate` `{"path"}` (an invalid receipt is a 200 `"invalid"`
verdict mirroring the CLI's per-file line) — reusing the CLI `swarm-validate`,
`swarm-run` and `receipt-validate` schema keys.
Session creation stays out of scope (its config composition is a separate
packet). Errors render the shared `{code,message}` body with statuses from the
closed error-code table — an empty or unknown `ErrCode` result maps explicitly
to `internal`/500; the listener defaults to loopback `127.0.0.1:7600`
(`--addr`), auth is an optional `FLOWSTATE_SERVE_TOKEN` bearer, and the
`http.Server` caps read headers at 5s and writes/idle at 30s. Shutdown cancels
in-flight reads (requests inherit the serve context) and drains within 5s;
writes are detached from request cancellation, so neither shutdown nor a client
disconnect stops one half way, and a swarm run that outlives the grace period
still ends the process with an error. Run endpoints stay out of scope for now.

## Structured provider tools

`internal/app` owns the M2 execution loop (`RunExecLoop`): a leased, fenced run replays
verified history and drives bounded provider turns; replay skips uncorrelated denial
results.
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

## Verified conversation history

`LoadSessionHistory(ctx, store, sessionID, HistoryLimits)` calls the existing
store's verified `Replay` before strict conversation projection. It preserves
user messages, grouped assistant calls with original IDs/arguments, and correlated
tool outcomes in journal order. Only explicit `tool_denied` outcomes may omit an
execution start. Orphan, duplicate, mismatched, incomplete or ambiguous batches
return `ErrInvalidHistory` and no partial conversation. Bootstrap metadata and
manual tool rows outside pending batches create no synthetic messages; unfinished
assistant deltas reject continuation projection. Unknown event types fail closed.
Assistant messages and deltas require the engine actor or a nonempty `provider:`
actor.

All limits are positive hard bounds: inspected events, message count, serialized
message-array bytes and each result's content bytes. Overflow returns
`ErrHistoryLimit`; history is never silently pruned. Fitting result JSON stays
byte-exact. Oversized results use a valid JSON envelope with `truncated`,
`original_bytes` and a UTF8 `preview`; persisted payloads and hashes are unchanged.
Replay still materializes the full journal. This read-only API grants no lifecycle
admission, retry or execution permission and supplies no call-event producer.
Use the app Ginkgo command above for local verification.

## Per-call correlated event sink

`NewCallEventSink(writer, provider.ToolCall)` builds an app-owned `tool.EventSink`
adapter bound to one immutable provider call identity (ID, name, actor and a
compacted copy of the arguments). Handed to a real `tool.Loop`, it annotates every
emitted `tool.call`/`tool.result` event with the provider `tool_call_id` so the
bounded-history projection can correlate durable outcomes with assistant calls.
The caller's argument payload is never mutated; stored arguments are compared
byte-for-byte after canonical compaction.

Validation fails closed before persistence: an invalid call identity, a mismatched
tool name or actor, arguments that differ from the bound call, a result before its
call start, or a repeated start are all rejected with typed errors and no event
reaches the store. Once an append fails the adapter is sticky: every later append
returns `ErrCallEventSticky` joined with the original failure, so an
unacknowledged outcome can never be exposed as acknowledged. A pre-call append
failure therefore blocks the tool effect through the loop, and a post-result
append failure exposes no acknowledged result.

This adapter is a producer for the verified history consumer above; it grants no
execution-loop admission, automatic provider execution, retry or lifecycle claim.
Denied result-only feedback still comes from the loop's own deny path without a
synthesised call start.

## Session run lifecycle

`flowstate session run` drives one bounded run of an active session, or an explicit
resume of a paused one. A budget stop ends the run non-terminally: the single event
writer appends `session.paused` (status paused, exit 5), and resumption is never
implicit. A later run of a paused session routes through
`app.ResumePausedSession` before any turn work: the writer's atomic `Resume`
appends the correlated `session.resumed` event (actor `user`, `from_status`
paused) and restores status active, then the ordinary turn/loop composition
continues under a fresh budget taken from the rerun's own flags — the pre-pause
budget is never carried over. Non-paused runnable sessions rerun unchanged with no
`session.resumed` emission, and terminal sessions (completed, cancelled, failed)
reject run as `session_terminal` (exit 6). A resume append failure blocks turn
execution and surfaces the persisted failure; the session stays paused and
unacknowledged. Use the app Ginkgo command above for local verification.

## Session config composition

`flowstate.json` (CLI-AND-API-CONTRACT §4) supplies session defaults: an explicit
`--config <path>` must exist, otherwise the ambient `./flowstate.json` in the
process working directory applies when present, and with neither the CLI behaves
exactly as before (flag-required). The schema is `{default_provider,
default_model, default_budget, storage_path, tool_allowlist}`; unknown fields and
any secret-bearing key at any depth are rejected — secrets come from the
environment only, never the config file. Every config failure exits 4 with code
`config_invalid`.

Explicit flags strictly override config values; a config `tool_allowlist`
activates the same bounded execution loop as `--allow` when no tool-surface flag
is given, with unchanged permission gates. `session new` pins the composition:
it persists `config_sha256`, the SHA-256 of the canonical effective composition —
a sorted-key, whitespace-free JSON object `{db_path, config_source, harness,
model, provider, tool_allowlist}` (allowlist sorted, `config_source` one of the
explicit path, `default`, or `none`). Identical effective composition yields an
identical hash; any wiring change changes it, so identical config plus identical
task produces identical wiring.

## Architecture Decision Records

The Go CLI's architecture decisions are recorded in `docs/adr/` using the
vault ADR template (frontmatter, Context/Problem/Decision/Consequences/
Alternatives/Cross-links/Sources). Start at `docs/adr/README.md` for the
index; `docs/adr/ADR-COMPARISON.md` maps the prototype vault ADR corpus
(78 files) to Go CLI implementation status.

## Changes and proof

A bounded refactor preserves the public contract, failure behaviour and tests.
Keep named types with their owning package, inject collaborators, document the
public surface and update the relevant guide with the change. Read [documentation
standards](../../rules/DOCUMENTATION.md) and [the test contract](../../rules/BDD_WORKFLOW.md).

Source checks and unit/integration tests do not prove live provider qualification,
T019 acceptance, product readiness, actual AI identity or independent review.
Resolve candidate-bound evidence only from the assigned Dropbox Metadata packet.
