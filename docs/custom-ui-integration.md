# Building a Custom UI for FlowState

A technical integration guide for mid-level engineers building a custom client (web SPA, desktop app, mobile app, or CLI/TUI) against the FlowState backend. It covers core backend communication only: the HTTP API, streaming, authentication, and hooks/plugins.

All file references are to the main worktree (`internal/...`, `web/...`).

---

## 1. Architecture overview

The engine is deliberately decoupled from presentation. The `Dispatcher` (internal/dispatch/dispatcher.go) is the single entry point into the engine, and streaming output is consumed through the `StreamConsumer` abstraction (internal/streaming/consumer.go), so the presentation layer knows nothing about engine internals. The HTTP API lives in internal/api/server.go. The Vue SPA in `web/` is a *reference client* only — it has no Go dependency, and your UI talks to exactly the same endpoints it does.

Streaming reaches a client over two channels:

1. **Ephemeral SSE** on `POST /api/chat` — a one-shot stream bound to the HTTP request that started the turn.
2. **Session long-poll** on `GET /api/v1/sessions/{id}/turns/{turn_id}` — the *sole* sessioned channel since session-scoped SSE was retired in May 2026.

```mermaid
flowchart LR
  UI[Custom UI<br/>SPA / desktop / mobile] -->|HTTP + cookie session + CSRF| API[HTTP API<br/>internal/api/server.go :8080]
  API -->|ephemeral SSE frames<br/>POST /api/chat| UI
  UI -->|long-poll<br/>GET .../turns/{id}?wait=true| API
  API --> DISP[Dispatcher<br/>internal/dispatch/dispatcher.go]
  DISP --> ENG[Engine<br/>via StreamConsumer]
  API -.->|JSON-RPC 2.0 over stdio| PLUGINS[External plugins]
```

---

## 2. Getting started: `flowstate serve`

```bash
flowstate serve --host localhost --port 8080
```

Flags and behaviour (internal/cli/serve.go:48-77):

| Flag | Default | Notes |
|---|---|---|
| `--host` | `localhost` | Bind address |
| `--port` | `8080` | Listen port |

Authentication is **enabled by default**, in mode `per-deployment-login`. There are **no TLS flags** — terminate TLS in a reverse proxy in front of the server if you need it.

Environment knobs (serve.go:269-298):

| Variable | Purpose |
|---|---|
| `FLOWSTATE_AUTH_ENABLED` | Enable/disable auth entirely |
| `FLOWSTATE_AUTH_MODE` | Auth mode; default `per-deployment-login` |
| `FLOWSTATE_AUTH_SECRET` | Session signing secret |
| `FLOWSTATE_AUTH_PRINCIPAL_ID` | Pre-provisioned principal id |
| `FLOWSTATE_AUTH_DISPLAY_NAME` | Display name for that principal |
| `FLOWSTATE_AUTH_ALLOWED_ORIGINS` | CSV allow-list enforced by the origin middleware |

---

## 3. Authentication & CSRF

The middleware chain, in order, is:

```
RequireOrigin → RequireSession → CSRF
```

- **Sessions** are cookie-based.
- **CSRF** uses gorilla/csrf; the token travels in the `X-CSRF-Token` header.
- `RequireOrigin` enforces the allowed-origins list — browsers must send a correct `Origin` header.

Auth endpoints:

| Method | Path | Purpose |
|---|---|---|
| POST | `/api/auth/login` | Establish session, receive cookies |
| POST | `/api/auth/logout` | Tear down session |
| GET | `/api/auth/whoami` | Current principal |
| GET | (csrf-preflight) | Fetch a CSRF token before your first state-changing POST |

GETs pass through CSRF; state-changing POSTs require the token. Unauthenticated requests receive a uniform `401` from middleware.

### Worked curl

```bash
# 1. Login — capture the session cookie (and CSRF cookie if issued)
curl -c cookies.txt -X POST http://localhost:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -H 'Origin: http://localhost:5173' \
  -d '{"principal":"operator"}'

# 2. Extract the CSRF token from the cookie jar (gorilla/csrf cookie name)
CSRF=$(awk '$6 ~ /csrf/ {print $7}' cookies.txt)

# 3. Authenticated, CSRF-protected POST
curl -b cookies.txt -X POST http://localhost:8080/api/v1/providers/quota/reset \
  -H 'Content-Type: application/json' \
  -H 'Origin: http://localhost:5173' \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"provider":"anthropic","account_hash":"a1b2c3","model":"claude-sonnet"}'
```

In a browser, `fetch` with `credentials: 'include'` (or `same-origin` if served from the API host) plus the `X-CSRF-Token` header covers everything.

---

## 4. Endpoint reference

### Chat & agents

| Method | Path | Auth | Notes |
|---|---|---|---|
| POST | `/api/chat` | session + CSRF | Body `{agent_id, message}` — **snake_case**. Responds with an ephemeral SSE stream (§5). |
| GET | `/api/agents` | — | Raw agent manifests; empty registry yields `[]` (never `null`) |
| GET | `/api/agents/{id}` | — | Manifest, or 404 **plain text** `agent not found` |
| GET | `/api/swarms` | — | `{id, description?, lead, members[]}` |

### Sessions

| Method | Path | Auth | Notes |
|---|---|---|---|
| POST | `/api/v1/sessions/{id}` | session | Create |
| GET | `/api/v1/sessions/{id}` | session | Fetch — **SessionResponse is camelCase** |
| DELETE | `/api/v1/sessions/{id}` | session + CSRF | Delete |

> **Casing gotcha:** `POST /api/chat` takes a snake_case body (`agent_id`, `message`), while SessionResponse and attachment rows are camelCase. Read each endpoint's schema below rather than assuming one convention.

### Turns (long-poll)

`GET /api/v1/sessions/{id}/turns/{turn_id}?wait=true&since=<timestamp>`

- `wait=true` — the server holds the request up to **25 seconds** for a state change, then returns the current turnResponse. The response carries `X-Accel-Buffering: no` so proxies do not buffer it.
- `since` — deduplication cursor: the server omits messages the client already has.

**turnResponse fields** (snake_case): `turn_id`, `session_id`, `status` (`running` | `completed` | `failed`), `started_at`, `completed_at`, `duration_ms`, `model`, `error`, `messages`. Live fields are `omitempty`: `phase`, `token_count`, `current_provider`, `current_model`, `context_usage`, `provider_quotas`, `compaction_events`, `gate_failures`, `critical_error`, `permission_requests`.

### Permissions

`POST /api/v1/sessions/{id}/permission-grant`

```json
{"request_id": "pr-123", "scope": "once"}
```

`scope` ∈ `once` | `session` | `forever` | `deny`. The resolution propagates to a waiting turn via the long-poll channel — poll the turn again after granting.

### Attachments

| Method | Path | Notes |
|---|---|---|
| POST | `/api/v1/sessions/{id}/attachments` | Multipart upload |
| GET | `/api/v1/sessions/{id}/attachments` | Envelope `{"attachments":[...]}` |

Row shape (camelCase): `{id, kind?, mediaType, sizeBytes, originalFilename?}`.

Limits: **10 files per request, 5 MB per file, 64 MB total request**. Errors use `{"error":"<code>","message":"<human-readable>"}`.

### Quota

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/providers/quota` | Rows are snake_case: `provider, account_hash, model?, observed_at, stale?, variant, rate_limit?, token_spend?, not_configured?, rate_limited_until?, status` |
| POST | `/api/v1/providers/quota/reset` | Body must be **exactly** `{"provider","account_hash","model"}` — `DisallowUnknownFields` rejects extras. Requires CSRF. Returns an empty `200` on success. |

---

## 5. Streaming

### 5a. Ephemeral SSE — `POST /api/chat`

Request (snake_case):

```json
{"agent_id": "general", "message": "Summarise today's commits"}
```

The response is `text/event-stream`. Each frame is `data: <json>\n\n`, terminated by the literal marker `data: [DONE]`. Frame catalogue (internal/api/sse_writers.go):

| Frame | Payload | Purpose |
|---|---|---|
| `chunk` | `{"content":"..."}` | Assistant text delta |
| `tool_call` | `{"type":"tool_call","name":"bash","status":"running","input":"..."}` | Tool invoked; `input` optional |
| `tool_result` | `{"type":"tool_result","content":"..."}` | Successful tool output |
| `tool_error` | `{"type":"tool_error","content":"..."}` | Tool failure — flip the running tool row to an error state in your UI |
| `skill_load` | `{"type":"skill_load","name":"clean-code"}` | A skill was loaded |
| `harness_retry` | `{"type":"harness_retry","content":"..."}` | Retry of the agent harness |
| `harness_attempt_start` | `{"type":"harness_attempt_start","content":"..."}` | Retry attempt begins |
| `harness_attempt_complete` | `{"type":"harness_attempt_complete","content":"..."}` | Retry attempt ends |
| `harness_critic_feedback` | `{"type":"harness_critic_feedback","content":"..."}` | Critic feedback on an attempt |
| `delegation` | see §6 | Work delegated to a child agent/swarm |
| `[DONE]` | literal | Stream complete |

Example stream:

```
data: {"content":"Let me check the "}

data: {"type":"tool_call","name":"bash","status":"running","input":"git log -1"}

data: {"type":"tool_result","content":"a1b2c3 fix: …"}

data: {"content":"latest commit fixes …"}

data: [DONE]
```

#### Consuming with fetch-stream (POST cannot use EventSource)

`EventSource` only issues GETs, so consume `POST /api/chat` with `fetch` and a reader:

```js
const res = await fetch('http://localhost:8080/api/chat', {
  method: 'POST',
  credentials: 'include',
  headers: {
    'Content-Type': 'application/json',
    'X-CSRF-Token': csrfToken,
  },
  body: JSON.stringify({ agent_id: 'general', message: 'Summarise today\'s commits' }),
});
const reader = res.body.getReader();
const dec = new TextDecoder();
let buf = '';
while (true) {
  const { done, value } = await reader.read();
  if (done) break;
  buf += dec.decode(value, { stream: true });
  let i;
  while ((i = buf.indexOf('\n\n')) >= 0) {
    const frame = buf.slice(0, i);
    buf = buf.slice(i + 2);
    const data = frame.replace(/^data: /m, '');
    if (data === '[DONE]') return;
    handleFrame(JSON.parse(data));
  }
}
```

`handleFrame` switches on `type` (absent for `chunk`) and appends `content`, updates the tool-call row, and so on per the table above.

### 5b. Session long-poll

Sessioned turns **do not stream over SSE** — session-scoped SSE was retired in May 2026. Use long-poll:

```
GET /api/v1/sessions/{id}/turns/{turn_id}?wait=true&since=2026-09-06T10:00:00Z
```

- `wait=true` holds the connection up to 25 s for a state change; then the server returns the full turnResponse. Re-issue immediately after each response while `status == "running"`.
- `since` is a cursor: pass the timestamp of the last message you hold and the server omits anything older.
- The response sets `X-Accel-Buffering: no`; configure your reverse proxy not to buffer.
- Permission grants (§ Permissions) propagate through this channel — after a user answers a `permission_requests` prompt, the next poll reflects the resolution.

```js
async function pollTurn(sessionId, turnId, since) {
  const url = `/api/v1/sessions/${sessionId}/turns/${turnId}` +
              `?wait=true${since ? `&since=${encodeURIComponent(since)}` : ''}`;
  const res = await fetch(url, { credentials: 'include' });
  const turn = await res.json();
  render(turn);
  if (turn.status === 'running') {
    return pollTurn(sessionId, turnId, turn.messages.at(-1)?.timestamp ?? since);
  }
  return turn;
}
```

---

## 6. @ mention routing

Mentions are parsed from the **plain message text** — no separate field — by `ExtractAtMentions` (internal/swarm/mentions.go:5-31). The parser skips email addresses. `ScanMentions` is always enabled for `/api/chat` (internal/api/server.go:1551, 1844).

So a message like:

```json
{"agent_id": "general", "message": "@reviewer take a look at this diff"}
```

resolves `@reviewer` against the agent registry (`GET /api/agents`) and swarm registry (`GET /api/swarms`) and routes the turn accordingly. Routing feedback arrives as ordinary `chunk` / `delegation` frames on the stream — a mention routing to a swarm emits a `delegation` frame:

```json
{
  "type": "delegation",
  "status": "started",
  "metadata": {
    "child_session_id": "sess-42",
    "parent_session_id": "sess-7",
    "source_agent": "general",
    "description": "review the diff",
    "model_name": "…",
    "provider_name": "…",
    "tool_calls": 3,
    "last_tool": "bash",
    "error": null,
    "load_skills": ["code-review"]
  }
}
```

`status` ∈ `started` | `completed` | `failed`; `model_name`, `provider_name`, `tool_calls`, `last_tool`, `error` and `load_skills[]` are optional. Render `started`/`completed`/`failed` as a delegating row in your UI.

Note: a single turn mentioning agents across multiple providers can accumulate quota usage across partitions — reflect `provider_quotas` from the turnResponse rather than assuming a single provider.

---

## 7. Hooks & plugins

### Manifests

Plugins live in subdirectories of the plugins dir — default `~/.local/share/flowstate` (internal/config/config.go:1398). Each plugin is declared at:

```
<plugins-dir>/<name>/manifest.json
```

```json
{
  "name": "audit-log",
  "version": "1.0.0",
  "description": "Logs every tool call",
  "command": "./audit-log",
  "args": ["--verbose"],
  "hooks": ["tool.execute.before", "tool.execute.after"],
  "timeout": 5000
}
```

Fields (internal/plugin/manifest.go:20-26): `name`, `version`, `description?`, `command`, `args?`, `hooks[]`, `timeout?`.

### Hook points (internal/plugin/hook_types.go:16-20)

| Hook | Fires when |
|---|---|
| `chat.params` | Chat request params assembled |
| `context.assembly` | Context being assembled for a turn |
| `event` | Engine events emitted |
| `tool.execute.before` | Before a tool runs |
| `tool.execute.after` | After a tool runs |

A hook declared by multiple plugins is fanned out to all of them; one plugin's failure does not block the others.

### Transport: JSON-RPC 2.0 over stdio (internal/plugin/external/jsonrpc.go:13-31)

FlowState spawns the plugin's `command` and speaks JSON-RPC 2.0 over its stdin/stdout:

```json
{"jsonrpc": "2.0", "id": 1, "method": "tool.execute.before", "params": {"tool": "bash", "input": "…"}}
```

```json
{"jsonrpc": "2.0", "id": 1, "result": {"ok": true}}
```

Errors: `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"…"}}`.

Plugins are transparent to your UI — you never talk to them directly; you simply observe their effects on the stream (e.g. a `context.assembly` hook altering what the model sees) and on turn results.

---

## 8. Errors & correlation IDs

All API errors are **sanitised** — they never leak Go internals, stack traces, or file paths — and carry a **correlation ID** you can quote when tracing a request through logs. Your UI should surface the correlation ID in any error toast/dialogue so support can locate the request.

Error shapes you will see:

- Attachment endpoints: `{"error":"<code>","message":"<human-readable>"}`.
- Quota reset: literal codes `invalid_request`, `not_found`, `not_implemented`, `method_not_allowed`, `internal_error`.
- Missing agent: 404 with plain-text body `agent not found`.

---

## 9. Caveats & troubleshooting

| Symptom / gotcha | Explanation |
|---|---|
| 401/403 on every POST | Missing `Origin` header, session cookie, or `X-CSRF-Token`. Middleware order is RequireOrigin → RequireSession → CSRF; fix the first failure first. |
| Long-poll returns instantly, no updates | You dropped `wait=true`, or your proxy is buffering — the endpoint sets `X-Accel-Buffering: no`, but check proxy config. |
| Docs mention session-scoped SSE / session SSE writers | **Stale.** Session-scoped SSE was retired May 2026; sessioned turns use long-poll only. |
| Docs mention `mention_routing_streamer.go` | **Stale** — that file does not exist; ignore any documentation referencing it. |
| `GET /api/sessions` also works | Legacy duplicate route; prefer `/api/v1/sessions`. |
| Casing confusion | Chat requests and quota rows are snake_case; SessionResponse and attachment rows are camelCase. Check §4 per endpoint. |
| Reset endpoint rejects your body | `POST /api/v1/providers/quota/reset` uses `DisallowUnknownFields` — send exactly `provider`, `account_hash`, `model`, nothing more. |
| Need TLS | There are no TLS flags on `serve`; terminate TLS at a reverse proxy. |
| Empty agents list renders as `[]` | By design — registries return `[]`, never `null`. |
