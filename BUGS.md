# Bugs

## Coverage baseline (2026-08-19)

HEAD=f968fc3f

- engine: 82.2%
- provider: ~86–92% (zai 86.0, shared 92.3)
- session: 84.4%
- app: 76.7%
- lint: 0 issues

## Open bugs

### Learning-hook subscriber misattributes tool results to the session ID

- File: `internal/app/app.go` — `handleToolExecuteResult` builds the learning
  context with `learning.AgentIDKey` set to `toolEvt.Data.SessionID`.
- Symptom: every persisted learning record carries the session ID in the
  AgentID field; per-agent learning aggregation groups by session, not agent.
- Suspected cause: `ToolExecuteResultEventData` carries `SessionID` and the
  event has no dedicated agent field in this subscriber path; the wiring
  assumes they coincide, which only holds for single-agent sessions.
- Severity: low — no data loss, records are persisted; downstream knowledge
  graph granularity is coarser than intended. Discovered while writing specs
  for the subscriber (commit 9753daf7); not fixed pending a data-model
  decision (add an AgentID field to the event vs. look the agent up).

## Resolved bugs

### execution.Loop.Evaluate panicked (SIGSEGV) on nil streamer

- File: `internal/execution/loop.go` — `Loop.runLoop` called `streamer.Stream` without a nil check.
- Symptom: `runtime error: invalid memory address or nil pointer dereference` (SIGSEGV) when
  `Evaluate`/`StreamEvaluate` were invoked with a nil `harness.Streamer` (reachable via
  mis-wired app DI), instead of returning an error.
- Suspected cause: missing guard; all other optional collaborators (validator, critic,
  observer) were nil-checked but the required streamer was not.
- Severity: medium — crash path only via mis-wiring, no data loss, but converts a
  config error into a process crash rather than an error return.
- Fix: nil guard at the top of `runLoop` returning
  `execution loop: streamer is nil for agent %q`; covered by internal Ginkgo spec
  (`Evaluate` with nil streamer returns error, not panic).
