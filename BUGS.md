# Bugs

## Coverage baseline (2026-08-19)

HEAD=f968fc3f

- engine: 82.2%
- provider: ~86–92% (zai 86.0, shared 92.3)
- session: 84.4%
- app: 76.7%
- lint: 0 issues

## Open bugs

(none)

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
