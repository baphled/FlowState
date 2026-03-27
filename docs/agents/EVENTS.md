# Events

FlowState streams all agent activity as typed JSON events over Server-Sent Events (SSE) or WebSocket. Every event carries a `"type"` discriminator field that identifies its kind, making it straightforward to deserialise in any client.

## Connecting to the Event Stream

### SSE (Server-Sent Events)

```bash
# Create a session first
curl -s -X POST http://localhost:8080/api/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"agent_id": "planning-coordinator"}' | jq .

# Open the SSE stream (replace SESSION_ID)
curl -N http://localhost:8080/api/v1/sessions/SESSION_ID/stream?verbosity=standard
```

### WebSocket

```bash
# Using wscat (npm install -g wscat)
wscat -c ws://localhost:8080/api/v1/sessions/SESSION_ID/ws
```

Each SSE frame follows the standard format:

```
data: {"type":"status_transition","from":"idle","to":"researching","agentId":"planning-coordinator"}

data: {"type":"delegation","source_agent":"planning-coordinator","target_agent":"explorer",...}

data: [DONE]
```

## Verbosity Levels

The `?verbosity=` query parameter controls which events the server forwards to the client.

| Level      | Value      | Events included                                                    |
|------------|------------|--------------------------------------------------------------------|
| `minimal`  | `minimal`  | `status_transition`, `plan_artifact`, `review_verdict`            |
| `standard` | `standard` | Minimal + `tool_call`, `delegation`                               |
| `verbose`  | `verbose`  | Standard + `text_chunk`, `coordination_store`                     |

The default level is `standard` when the parameter is omitted.

## Event Types

### `text_chunk`

Raw LLM text output, streamed token by token. Only visible at `verbose` level.

**Go type**: `streaming.TextChunkEvent`

```json
{
  "type":    "text_chunk",
  "content": "The authentication middleware pattern uses...",
  "agentId": "explorer"
}
```

| Field     | Type   | Description                              |
|-----------|--------|------------------------------------------|
| `type`    | string | Always `"text_chunk"`                    |
| `content` | string | The raw text token from the LLM          |
| `agentId` | string | ID of the agent producing this output    |

---

### `tool_call`

A tool invocation with its arguments, result, and execution duration. Visible at `standard` level and above.

**Go type**: `streaming.ToolCallEvent`

```json
{
  "type":      "tool_call",
  "name":      "bash",
  "arguments": { "command": "find internal/ -name '*.go' | head -20" },
  "result":    "internal/api/server.go\ninternal/app/app.go\n...",
  "duration":  142000000,
  "agentId":   "explorer"
}
```

| Field       | Type              | Description                                      |
|-------------|-------------------|--------------------------------------------------|
| `type`      | string            | Always `"tool_call"`                             |
| `name`      | string            | Tool name (e.g. `"bash"`, `"file"`, `"web"`)    |
| `arguments` | `map[string]any`  | Tool arguments as key–value pairs                |
| `result`    | string            | Tool output (truncated for large results)        |
| `duration`  | number            | Execution duration in nanoseconds                |
| `agentId`   | string            | ID of the agent that called the tool             |

---

### `delegation`

A delegation event between agents, including model, provider, and progress indicators. Visible at `standard` level and above.

**Go type**: `streaming.DelegationEvent`

```json
{
  "type":          "delegation",
  "source_agent":  "planning-coordinator",
  "target_agent":  "explorer",
  "chain_id":      "chain-abc123",
  "status":        "started",
  "model_name":    "claude-sonnet-4-6",
  "provider_name": "anthropic",
  "description":   "Investigating codebase structure",
  "tool_calls":    0,
  "last_tool":     "",
  "started_at":    "2026-03-27T14:00:00Z",
  "completed_at":  null
}
```

| Field           | Type        | Description                                                         |
|-----------------|-------------|---------------------------------------------------------------------|
| `type`          | string      | Always `"delegation"`                                               |
| `source_agent`  | string      | ID of the delegating agent                                          |
| `target_agent`  | string      | ID of the agent receiving the delegation                            |
| `chain_id`      | string      | Coordination store namespace for this chain                         |
| `status`        | string      | `"started"` or `"completed"`                                        |
| `model_name`    | string      | LLM model the target agent is using                                 |
| `provider_name` | string      | Provider supplying the model                                        |
| `description`   | string      | Human-readable task description                                     |
| `tool_calls`    | number      | Number of tool calls made so far                                    |
| `last_tool`     | string      | Name of the most recently called tool                               |
| `started_at`    | string/null | RFC 3339 timestamp when the delegation started                      |
| `completed_at`  | string/null | RFC 3339 timestamp when the delegation finished (null while running)|

---

### `coordination_store`

A coordination store read or write operation. Only visible at `verbose` level.

**Go type**: `streaming.CoordinationStoreEvent`

```json
{
  "type":      "coordination_store",
  "operation": "set",
  "key":       "requirements",
  "chainId":   "chain-abc123"
}
```

| Field       | Type   | Description                                                  |
|-------------|--------|--------------------------------------------------------------|
| `type`      | string | Always `"coordination_store"`                                |
| `operation` | string | `"get"` or `"set"`                                           |
| `key`       | string | The key within the chain namespace (e.g. `"requirements"`)  |
| `chainId`   | string | The chain namespace this operation belongs to                |

---

### `status_transition`

A coordinator phase change. Visible at all verbosity levels (`minimal` and above).

**Go type**: `streaming.StatusTransitionEvent`

```json
{
  "type":    "status_transition",
  "from":    "researching",
  "to":      "analysing",
  "agentId": "planning-coordinator"
}
```

| Field     | Type   | Description                                              |
|-----------|--------|----------------------------------------------------------|
| `type`    | string | Always `"status_transition"`                             |
| `from`    | string | Previous phase (e.g. `"idle"`, `"researching"`)          |
| `to`      | string | New phase (e.g. `"analysing"`, `"writing"`, `"done"`)    |
| `agentId` | string | ID of the agent transitioning                            |

See [PLANNING_LOOP.md](./PLANNING_LOOP.md) for the full phase transition table.

---

### `plan_artifact`

The completed plan text produced by the Plan Writer. Visible at all verbosity levels (`minimal` and above).

**Go type**: `streaming.PlanArtifactEvent`

```json
{
  "type":    "plan_artifact",
  "content": "# Plan: Implement OAuth2 integration\n\n## TLDR\n...",
  "format":  "markdown",
  "agentId": "plan-writer"
}
```

| Field     | Type   | Description                                             |
|-----------|--------|---------------------------------------------------------|
| `type`    | string | Always `"plan_artifact"`                                |
| `content` | string | The full plan text                                      |
| `format`  | string | Content format, typically `"markdown"`                  |
| `agentId` | string | ID of the agent that produced the plan                  |

---

### `review_verdict`

The reviewer's verdict on the submitted plan. Visible at all verbosity levels (`minimal` and above).

**Go type**: `streaming.ReviewVerdictEvent`

```json
{
  "type":       "review_verdict",
  "verdict":    "APPROVE",
  "confidence": 0.92,
  "issues":     [],
  "agentId":    "plan-reviewer"
}
```

A rejection includes the blocking issues that the Plan Writer must address:

```json
{
  "type":       "review_verdict",
  "verdict":    "REJECT",
  "confidence": 0.78,
  "issues":     [
    "Missing acceptance criteria for task T3",
    "No rollback strategy specified for database migration"
  ],
  "agentId":    "plan-reviewer"
}
```

| Field        | Type       | Description                                                      |
|--------------|------------|------------------------------------------------------------------|
| `type`       | string     | Always `"review_verdict"`                                        |
| `verdict`    | string     | `"APPROVE"` or `"REJECT"`                                        |
| `confidence` | number     | Reviewer confidence in the verdict (0.0 – 1.0)                   |
| `issues`     | `string[]` | Blocking issues (empty on APPROVE, non-empty on REJECT)          |
| `agentId`    | string     | ID of the reviewing agent                                        |

## Event Deserialisation

All events share a `"type"` discriminator that identifies the concrete struct. Use `streaming.UnmarshalEvent` in Go to deserialise:

```go
event, err := streaming.UnmarshalEvent(data)
if err != nil {
    return err
}

switch e := event.(type) {
case streaming.ReviewVerdictEvent:
    fmt.Println("verdict:", e.Verdict)
case streaming.PlanArtifactEvent:
    fmt.Println("plan:", e.Content)
case streaming.StatusTransitionEvent:
    fmt.Printf("phase: %s → %s\n", e.From, e.To)
}
```

In JavaScript, read the `type` field before dispatching:

```javascript
const event = JSON.parse(data);
switch (event.type) {
  case 'review_verdict':
    console.log('verdict:', event.verdict, 'issues:', event.issues);
    break;
  case 'plan_artifact':
    console.log('plan:', event.content.slice(0, 80));
    break;
  case 'status_transition':
    console.log(`phase: ${event.from} → ${event.to}`);
    break;
}
```

## Related Documents

- [PLANNING_LOOP.md](./PLANNING_LOOP.md) — which events each phase emits
- [DELEGATION.md](./DELEGATION.md) — DelegationEvent fields in context
- [RESEARCH_AGENTS.md](./RESEARCH_AGENTS.md) — CoordinationStoreEvent and store key schema
