# Delegation

The delegation system allows a coordinator agent to hand off sub-tasks to specialist agents asynchronously. Each delegation is tracked by the `BackgroundTaskManager`, which runs the target agent in a goroutine and reports its status through the event stream.

## How Delegation Works

When an agent calls the `delegate` tool, the runtime:

1. Validates the `Handoff` struct (source agent, target agent, and chain ID are required).
2. Looks up the target agent's isolated engine instance from the pre-built engine map.
3. Launches the target's engine as a background task via `BackgroundTaskManager.Launch`.
4. Returns a task ID to the caller immediately, without waiting for completion.
5. Emits a `DelegationEvent` on the stream when the task starts and when it finishes.

Target agents receive their own isolated engine instance — they do not share state with the coordinator. This prevents context corruption during concurrent delegations.

## The Handoff Struct

A `Handoff` carries all metadata for a single delegation. The coordinator populates this before calling `delegate`.

| Field         | JSON key        | Type                | Required | Description                                                      |
|---------------|-----------------|---------------------|----------|------------------------------------------------------------------|
| `SourceAgent` | `source_agent`  | `string`            | yes      | ID of the agent initiating the handoff                          |
| `TargetAgent` | `target_agent`  | `string`            | yes      | ID of the agent that should receive the handoff                 |
| `TaskType`    | `task_type`     | `string`            | no       | Delegation category for routing and handling (e.g. `"research"`) |
| `ChainID`     | `chain_id`      | `string`            | yes      | Coordination store namespace for this delegation chain          |
| `Message`     | `message`       | `string`            | no       | Instruction or request passed to the target agent               |
| `Feedback`    | `feedback`      | `string`            | no       | Response or review data returned to the caller                  |
| `Metadata`    | `metadata`      | `map[string]string` | no       | Arbitrary key–value attributes for additional context            |

### Validation

`Handoff.Validate()` returns an error when any required field is empty:

- `source_agent` must not be empty
- `target_agent` must not be empty
- `chain_id` must not be empty

### Example Handoff

```json
{
  "source_agent": "planning-coordinator",
  "target_agent": "explorer",
  "task_type":    "research",
  "chain_id":     "chain-abc123",
  "message":      "Investigate the authentication middleware patterns in internal/api/",
  "feedback":     "",
  "metadata": {
    "priority": "high",
    "scope":    "internal/api"
  }
}
```

## BackgroundTaskManager

The `BackgroundTaskManager` tracks parallel delegation tasks. It maintains a map of `BackgroundTask` values, each protected by a read-write mutex.

### BackgroundTask fields

| Field         | Type          | Description                                           |
|---------------|---------------|-------------------------------------------------------|
| `ID`          | `string`      | Unique task identifier                                |
| `AgentID`     | `string`      | ID of the agent running this task                     |
| `Description` | `string`      | Human-readable task description                       |
| `Status`      | `atomicValue` | Current status: `pending`, `running`, `completed`, `failed`, or `cancelled` |
| `StartedAt`   | `time.Time`   | UTC time when the task was launched                   |
| `CompletedAt` | `*time.Time`  | UTC time when the task finished (nil while running)   |
| `Result`      | `string`      | Output from the target agent on success               |
| `Error`       | `error`       | Non-nil when the task failed                          |

### Task lifecycle

```
pending → running → completed
                 → failed
                 → cancelled
```

Tasks transition to `cancelled` when the context is cancelled, `failed` when the function returns a non-nil error, and `completed` on success.

### Manager methods

| Method             | Description                                                         |
|--------------------|---------------------------------------------------------------------|
| `Launch(ctx, id, agentID, desc, fn)` | Starts a background task and returns the `*BackgroundTask` immediately |
| `Get(id)`          | Returns the task by ID and whether it was found                     |
| `Cancel(id)`       | Requests cancellation of a pending or running task                  |
| `List()`           | Returns all tracked tasks                                           |
| `EvictCompleted()` | Removes all terminal-state tasks (completed, failed, cancelled)     |
| `ActiveCount()`    | Returns the number of pending or running tasks                      |

Call `EvictCompleted()` periodically to prevent unbounded memory growth in long-running coordinators.

## Wiring: createDelegateEngine

Each delegation target gets its own engine instance created by `App.createDelegateEngine`. This function builds an isolated `engine.Engine` with:

- The target agent's manifest (model preferences, tools, skills)
- The shared `coordination.Store` instance so agents can exchange data
- A hook chain wired to the target's manifest, not the coordinator's

The target engine does **not** receive a `DelegateTool`. This prevents recursive delegation loops.

The `wireDelegateToolIfEnabled` function (called during `App.New`) builds the engine map and attaches a `DelegateTool` to the coordinator's engine only when `manifest.Delegation.CanDelegate` is `true`.

## Parallel Delegation

The coordinator can launch multiple agents concurrently using `BackgroundTaskManager.Launch` in a loop. Each target runs in its own goroutine. The coordinator then waits for all active tasks to complete before moving to the next phase.

For example, Explorer and Librarian run in parallel during the research phase of the [Planning Loop](./PLANNING_LOOP.md):

```
C → BackgroundTaskManager.Launch("explorer-task", "explorer", ...)
C → BackgroundTaskManager.Launch("librarian-task", "librarian", ...)
C → wait until BackgroundTaskManager.ActiveCount() == 0
C → proceed to analyst delegation
```

## Event Visibility

Delegation activity is surfaced through two event types on the [event stream](./EVENTS.md):

- `DelegationEvent` — emitted when a delegation starts and when it completes, including model, provider, tool call count, and timing
- `StatusTransitionEvent` — emitted when the coordinator's phase changes (e.g. `researching → analysing`)

See [EVENTS.md](./EVENTS.md) for the full schemas and verbosity levels.

## Related Documents

- [PLANNING_LOOP.md](./PLANNING_LOOP.md) — how the coordinator sequences delegations
- [EVENTS.md](./EVENTS.md) — DelegationEvent and StatusTransitionEvent schemas
- [CREATING_AGENTS.md](./CREATING_AGENTS.md) — how to configure agents for delegation
- [RESEARCH_AGENTS.md](./RESEARCH_AGENTS.md) — the three research agents and their coordination store usage
