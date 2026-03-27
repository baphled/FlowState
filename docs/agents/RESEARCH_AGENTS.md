# Research Agents

The research phase of the [Planning Loop](./PLANNING_LOOP.md) uses three specialist agents to gather and synthesise information before the Plan Writer produces a plan. Each agent has a distinct role and a defined set of tools.

## The Three-Agent Pipeline

```
Explorer ──────┐
               ├──▶ Analyst ──▶ Plan Writer
Librarian ─────┘
```

Explorer and Librarian run **in parallel** during the research phase — the coordinator launches both simultaneously as background tasks. Once both complete, the coordinator delegates to the Analyst, which synthesises their outputs. Only after the Analyst writes its evidence dossier does the Plan Writer begin.

## Explorer

**Manifest**: `agents/explorer.json`  
**ID**: `explorer`  
**Name**: Codebase Explorer

The Explorer investigates the local codebase to find patterns, conventions, and structural details relevant to the planning request.

### Tools

| Tool                | Purpose                                     |
|---------------------|---------------------------------------------|
| `bash`              | Run shell commands to search and inspect    |
| `file`              | Read source files directly                  |
| `coordination_store`| Write findings to the shared store          |

### Skills

- `research` — systematic investigation methodology
- `code-reading` — understanding unfamiliar codebases
- `critical-thinking` — evaluating findings against the request

### What the Explorer produces

The Explorer writes its findings to `{chainID}/requirements` in the coordination store. The content is a structured summary of:

- Relevant packages and their responsibilities
- Existing patterns and conventions to follow
- Files likely to be affected
- Any constraints or gotchas found in the code

### When to use the Explorer

Use the Explorer (or delegate to it via the coordinator) when the planning request requires understanding of:

- Existing architecture and layer boundaries
- Interface contracts and dependency relationships
- Test conventions and coverage patterns
- Build and deployment configuration

---

## Librarian

**Manifest**: `agents/librarian.json`  
**ID**: `librarian`  
**Name**: Reference Librarian

The Librarian gathers external references — official documentation, library best practices, RFC specifications, and community guidance.

### Tools

| Tool                | Purpose                                              |
|---------------------|------------------------------------------------------|
| `web`               | Fetch URLs and search the web for references         |
| `bash`              | Run commands to inspect dependency versions          |
| `file`              | Read local documentation and configuration files     |
| `coordination_store`| Write external references to the shared store        |

### Skills

- `research` — systematic reference gathering
- `critical-thinking` — evaluating source quality and relevance

### What the Librarian produces

The Librarian writes its findings to `{chainID}/interview` in the coordination store. The content is a structured summary of:

- Relevant library APIs with version-accurate examples
- Official guidance and recommended patterns
- Known pitfalls or migration concerns
- Links to authoritative sources

### When to use the Librarian

Use the Librarian when the planning request involves:

- Third-party libraries with non-obvious APIs
- Protocol or specification compliance
- Security guidance from authoritative sources
- Deprecated APIs that need migration paths

---

## Analyst

**Manifest**: `agents/analyst.json`  
**ID**: `analyst`  
**Name**: Evidence Analyst

The Analyst synthesises the Explorer's codebase findings and the Librarian's external references into a coherent evidence dossier. It reads from the store, applies critical and systems-level thinking, and writes a consolidated analysis.

### Tools

| Tool                | Purpose                                                      |
|---------------------|--------------------------------------------------------------|
| `file`              | Read additional supporting files if needed                   |
| `coordination_store`| Read Explorer and Librarian outputs; write the dossier       |

The Analyst deliberately does **not** have `bash` or `web` tools — it works only with information already gathered by the other agents. This prevents scope creep and keeps the analysis phase focused.

### Skills

- `critical-thinking` — challenging assumptions in the findings
- `epistemic-rigor` — distinguishing known facts from inferences
- `systems-thinker` — understanding how components interact
- `research` — structured synthesis methodology

### What the Analyst produces

The Analyst reads `{chainID}/requirements` and `{chainID}/interview`, then writes its output to `{chainID}/evidence`. The evidence dossier includes:

- A synthesised view of the codebase and external context
- Identified risks and constraints
- Recommended approach based on the combined findings
- Open questions that the Plan Writer should address

---

## Coordination Store

The three research agents communicate exclusively through the coordination store — they do not call each other directly. The store is a key-value namespace shared across all agents in the same delegation chain.

### Interface

```go
type Store interface {
    Get(key string) ([]byte, error)
    Set(key string, value []byte) error
    List(prefix string) ([]string, error)
    Delete(key string) error
}
```

`ErrKeyNotFound` is returned when a key does not exist.

### Key naming convention

All keys are scoped to a `chainID` to prevent collisions when multiple planning chains run concurrently.

| Key                         | Written by  | Read by                     | Content                                 |
|-----------------------------|-------------|-----------------------------|-----------------------------------------|
| `{chainID}/requirements`    | Explorer    | Analyst                     | Codebase investigation findings         |
| `{chainID}/interview`       | Librarian   | Analyst                     | External reference summary              |
| `{chainID}/evidence`        | Analyst     | Plan Writer                 | Synthesised evidence dossier            |
| `{chainID}/plan`            | Plan Writer | Plan Reviewer, App          | Generated plan text                     |
| `{chainID}/review`          | Plan Reviewer| Coordinator, App           | VERDICT: APPROVE or REJECT with issues  |

### Listing keys for a chain

Use `Store.List(chainID)` to enumerate all keys written during a planning chain:

```go
keys, err := store.List("chain-abc123")
// returns: ["chain-abc123/requirements", "chain-abc123/interview", ...]
```

### Store implementation

The runtime uses `coordination.NewMemoryStore()` — an in-memory implementation backed by a `sync.RWMutex`-protected map. Data lives for the lifetime of the server process. For persistence across restarts, implement the `coordination.Store` interface with a durable backend.

### Reading store activity on the event stream

When the `verbose` verbosity level is set, each `Get` and `Set` operation emits a `CoordinationStoreEvent` on the stream:

```json
{
  "type":      "coordination_store",
  "operation": "set",
  "key":       "requirements",
  "chainId":   "chain-abc123"
}
```

See [EVENTS.md](./EVENTS.md#coordination_store) for the full schema.

---

## Adding a Research Agent

To add a fourth research agent to the pipeline:

1. Create a manifest in `agents/your-agent.json` following the Explorer pattern (see [CREATING_AGENTS.md](./CREATING_AGENTS.md)).
2. Add it to the coordinator's `delegation_table` in `agents/planning-coordinator.json`.
3. Choose a coordination store key for its output (e.g. `{chainID}/security-findings`).
4. Update the Analyst's prompt to read and synthesise the new key alongside `requirements` and `interview`.

The Analyst does not need code changes — it reads whatever keys are present in the store for its chain.

## Related Documents

- [PLANNING_LOOP.md](./PLANNING_LOOP.md) — how the coordinator sequences the research pipeline
- [DELEGATION.md](./DELEGATION.md) — the async delegation runtime that runs agents in parallel
- [EVENTS.md](./EVENTS.md) — CoordinationStoreEvent for observing store activity
- [CREATING_AGENTS.md](./CREATING_AGENTS.md) — manifest structure for new agents
