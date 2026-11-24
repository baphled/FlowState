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
| `internal/app` | Compose collaborators and application use cases, including the turn and tool loops |
| `internal/session` | Lifecycle, identity and ordering policy |
| `internal/server` | HTTP transport adapter — ratified ahead of implementation (ADR-007); owns the `net/http` surface cmd is banned from |
| `internal/agent` | Agent profiles: pure, filesystem-only loading and validation (the application layer supplies the tool catalog) |
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
session answers 409 `session_terminal` — closing/cancelling is not idempotent; if the cancel commits but
settling the session's budget reservation fails, the answer is still 500 but with the stable code
`reservation_unsettled` and a structured `session` field `{"id","status":"cancelled"}`, so a client can tell it
from "nothing happened"; the CLI prints the cancelled state before the error, whose code stays `internal` and
whose exit stays 1),
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
budget stops 409, a not-accepted run — failed gate or rejected packet — is 422 `receipt_invalid`, and the CLI exits 8) and
`POST /receipt/validate` `{"path"}` (an invalid receipt is a 200 `"invalid"`
verdict mirroring the CLI's per-file line) — reusing the CLI `swarm-validate`,
`swarm-run` and `receipt-validate` schema keys.
Errors render the shared `{code,message}` body with statuses from the
closed error-code table — an empty or unknown `ErrCode` result maps explicitly
to `internal`/500; the listener defaults to loopback `127.0.0.1:7600`
(`--addr`), auth is an optional `FLOWSTATE_SERVE_TOKEN` bearer, and startup refuses (usage, exit 2) an
address other than a literal loopback IP or `localhost` — including an empty host such as `:8080` — unless the
token is set; the error names both remedies. The
`http.Server` caps read headers at 5s and writes/idle at 30s. Shutdown cancels
in-flight reads (requests inherit the serve context) and drains within 5s. A read whose context is done and
whose port failed with a context error is not an internal failure: if the serve context is done the client
gets 503 `unavailable` ("server is shutting down"); otherwise the client has gone and nothing is written
(classified once by the read-port wrapper and rendered by the shared error writer; writes and the run route are
unchanged);
writes are detached from request cancellation, so neither shutdown nor a client
disconnect stops one half way; the run endpoint is the deliberate exception:
its turn follows the request context, so a disconnect or shutdown mid-run
cancels the session, as a signal does for the CLI, and `session.cancelled` records the reason
`client_disconnected` or `shutdown` (the CLI records `sigint`, `sigterm` or `sighup`). The CLI cancels a run on
SIGINT, SIGTERM or SIGHUP (SIGHUP
only when it was not ignored at start, as under `nohup`) and exits 130. SIGQUIT is deliberately not handled: Go's
default (dump goroutines, exit 2) is kept as the debugging behaviour operators expect. Every request body is capped at
1 MiB (a larger one is 400 `usage` naming the limit, and the connection is
closed) and every POST body is decoded strictly (unknown fields and trailing
data are 400 `usage`). The `after` cursor of the events endpoints must be one
integer: a query string that does not parse, a repeated cursor, an empty value
or a non-integer is 400 `usage`, and only an absent cursor means 0.
A swarm run that outlives the grace period still ends the process with an error.

S5 completes the surface. `GET /sessions/{id}/events?after=N` answers events
past the strictly-greater cursor (absent is 0; `after=-1` replays seq 0);
`.../events/stream?after=N` follows the log over SSE (200ms poll reading status
before events, terminal status frame, per-write deadline reset 15s ahead of the
global 30s `WriteTimeout`). `POST /sessions/{id}/run` runs one
synchronous turn behind the `session run` preamble and answers the projected
event log; `{"budget","max_tokens"}` mirrors the CLI flags, provider failures
(errors wrapping `app.ErrProvider`, named once in the message) 502 `provider_error`, budget stops and a
`max_turns` failure (the turn limit reached while still requesting tools, exit 5 on the CLI) 409, any
other failure keeps its own class (a storage error while recording the turn is 500 `internal`, and `session
run` exits 1), and the route's deadline extends 10 minutes; a client disconnect or shutdown mid-run cancels
the session, as
a signal does for the CLI. The run body also takes `max_turns` (1 to 1000, absent means the default of 25), the
same tool-loop turn limit as `session run --max-turns`; a negative or larger value is 400 `usage`, and a run that
reaches the limit while still asking for tools is 409 `max_turns`.
`POST /sessions` creates via the `session new` composition (a write, so it is
detached from cancellation): body fields override serve config, secret-looking
keys are 400 `usage` (client input, unlike the config's `config_invalid`).

## Structured provider tools

`internal/app` owns the M2 execution loop (`RunExecLoop`): a leased, fenced run replays
verified history and drives bounded provider turns; replay skips uncorrelated denial
results. `ExecLoopConfig.SystemPrompt`, when set, is sent as a role-`system` message first in every
provider request; it is neither an event nor part of replayed history.
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
HTTP, except for the Anthropic API-key route, which translates them (see below), sending the dots of
tool names such as `fs.read` as `_` and mapping reply names back.
Nonstream coding `Chat` on the OpenAI route encodes tool requests but rejects tool responses
with `ToolProtocolError`; use `Stream` for tool calls. Legacy text coding streams
remain supported, while unsolicited tool deltas fail closed. Other adapters and
the engine retain their existing behavior. Consumers still validate structured
events with `StreamValidator`; this adapter does not execute tools.

With the Go toolchain and module dependencies installed, run from repository root:

```sh
go run github.com/onsi/ginkgo/v2/ginkgo --procs=2 --randomize-all --randomize-suites --race --fail-on-pending --fail-on-empty --timeout=3m ./internal/provider ./internal/provider/zai
```

## Provider configuration

Provider settings come from the environment only. The CLI reads no provider configuration file: session
composition is `flowstate.json` (ADR-005), which never holds credentials, and secrets are never stored by the CLI.

| Provider | Variable | Meaning | Default |
|---|---|---|---|
| `z.ai` (alias `zai`) | `ZAI_API_KEY` | API key; required, resolving fails naming it when unset | none |
| `z.ai` | `ZAI_BASE_URL` | Endpoint base URL | the plan's endpoint |
| `z.ai` | `ZAI_PLAN` | `coding` or `general` | `coding` when unset |
| `z.ai` | `ZAI_CODING_ROUTE` | Coding-plan wire format, `openai` or `anthropic` | `openai`; `anthropic` when |
| | | | `ZAI_BASE_URL` names `/api/anthropic` |
| `anthropic` | `FLOWSTATE_ANTHROPIC_AUTH` | Route: `oauth` or `api_key`; another value is an error | `oauth` |
| `anthropic` | `FLOWSTATE_CLAUDE_BIN` | `claude` binary for the `oauth` route | `claude` on `PATH` |
| `anthropic` | `ANTHROPIC_API_KEY` | Key for the `api_key` route; required there | none |
| `anthropic` | `ANTHROPIC_BASE_URL` | API base URL for the `api_key` route | `https://api.anthropic.com` |

`ANTHROPIC_API_KEY` never selects the `api_key` route; only `FLOWSTATE_ANTHROPIC_AUTH=api_key` does, because users
export that key for other tools and still want the subscription.

### Migrating from `providers.yaml`

Earlier builds read `providers.yaml` (in `$FLOWSTATE_CONFIG_DIR` or the user configuration directory's `flowstate`
folder). It is no longer read, `config.yaml` and `FLOWSTATE_CONFIG_DIR` are ignored, and nothing warns about a
leftover file. Move each setting to the variable above, then delete the file:

- `providers.zai.api_key` becomes `ZAI_API_KEY`, `host` becomes `ZAI_BASE_URL`, `plan` becomes `ZAI_PLAN` and
  `route` becomes `ZAI_CODING_ROUTE`.
- `providers.anthropic.auth_type: api_key` becomes `FLOWSTATE_ANTHROPIC_AUTH=api_key` with `ANTHROPIC_API_KEY` (from
  `api_key`) and optionally `ANTHROPIC_BASE_URL` (from `host`). Without the new variable the `anthropic` provider
  uses the Claude subscription route, so a user who is not signed in with `claude` gets a sign-in error rather than
  a charge; one who is signed in uses the subscription instead of the API.
- A missing `ZAI_API_KEY` still fails loudly, but a stale `plan`, `host` or `route` is silently replaced by the
  defaults (coding plan, the plan's endpoint) until its variable is set.

## Anthropic provider

`internal/provider/anthropic` (registry name `anthropic`) is selected by
`FLOWSTATE_ANTHROPIC_AUTH`: `oauth` (the default) or `api_key`.
The OAuth route reaches a user's Claude subscription through their own Claude Code sign-in,
only by running the user's own unmodified `claude` binary in headless
print mode, prompt on stdin, and parsing its stream-json output. FlowState never reads,
stores or forwards a Claude credential and sends nothing to Anthropic itself; the user
signs in with `claude` and `/login`. `FLOWSTATE_CLAUDE_BIN` overrides the binary. The
provider refuses structured tool calls, always sends a lossy `User:` / `Assistant:` transcript
(so a prompt can never start with `/`) and ignores `MaxTokens`. FlowState does not pool or
resell a subscription but does not enforce single-user use: `flowstate serve` runs a session
for anyone who can reach it, so keep the loopback bind and set `FLOWSTATE_SERVE_TOKEN`; letting
others reach it would route their requests through the owner's plan, which the policy forbids.

The `api_key` route (`anthropic.APIKey`) is ordinary Anthropic API billing, independent of the subscription route. It
sends the key (`ANTHROPIC_API_KEY`) as `x-api-key` to `<base>/v1/messages` (`ANTHROPIC_BASE_URL`, else
`https://api.anthropic.com`), reusing the Messages wire client in `internal/provider/zai`. It never reads the Claude
Code sign-in or runs `claude`. It supports text chat and streaming, system messages, `MaxTokens` and structured tools
(`tool_use` / `tool_result` blocks); a truncated stream is an error, and nothing is retried. The subscription route
still refuses structured tools.
See ADR-008.

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

`session run <id> --agents user|project|both` loads agent profiles and registers the `delegate` tool in the run's
registry (`app.NewDelegateTool`); `delegate` must be allowlisted and the two must agree, or the run is a usage error
(exit 2). The execution loop passes the per-call event writer it already holds to the tool through the invocation
context, so the `delegation.started` and `delegation.completed` events are appended by that one writer between the
call and its result. `delegate {resume: <child id>}` continues an interrupted child (ADR-009 D8), and crash recovery
answers a parent's dangling `delegate` call from the `delegation.started` events. A failed, cancelled or partial child
is returned to the parent model as an `error:` tool result; the parent run does not
fail, and a cancelled parent still ends cancelled. The HTTP run has no `--agents` yet.

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
is given, with unchanged permission gates. `--allow` and `--allow-file` replace
the config allowlist; `--workdir` does not select an allowlist, it only sets
where the tools operate, so alone it keeps the config `tool_allowlist`. A loop
run whose effective allowlist is empty (for example `--workdir` with no config
allowlist, or an empty `--allow-file` manifest) fails with a usage error (exit
2, code `usage`) rather than running with no tools. `session new` pins the composition:
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
