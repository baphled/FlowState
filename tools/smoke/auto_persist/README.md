# auto_persist smoke

One-shot Go program that exercises the `coordination.PersistingStore`
wrapper end-to-end: writes a plan + APPROVE review to the user's real
file-backed coord-store, then waits for the post-Set callback to flush
the plan to `~/.local/share/flowstate/plans/<chain-id>.md`.

Used to manually verify the wiring after the auto-persist commit
(`25b4675`). The unit specs in
`internal/coordination/persisting_store_test.go` cover the trigger
logic; this probe exists so anyone debugging "why didn't my approved
plan land on disk?" has a single command to reproduce the happy path.

## Run

```bash
go run ./tools/smoke/auto_persist/
```

Exits 0 with `PASS: auto-persist landed at <path>` on success.
Exits 1 with diagnostic dumps (was the review actually persisted? was
the plan body present?) on failure.

## What it covers

- The wrapper's Set hot path fires the callback on `<chain>/review`
  payloads containing `APPROVE`.
- `App.PersistApprovedPlan` reads `<chain>/plan` from the same store
  and writes a `plan.File` to disk via `plan.Store.Create`.
- The async goroutine completes within the 1s polling budget (the
  unit test uses 500ms; this gives more headroom for a real
  filesystem).

## What it does NOT cover

- The agent-driven path through the `plan_write` tool — that's the
  primary route. Smoke-test it via:
  ```bash
  ./build/flowstate run --agent=plan-writer --prompt="..." --session=...
  ls ~/.local/share/flowstate/plans/
  ```
- Cosmetic quality of the persisted file. Today
  `PersistApprovedPlan` (app.go:1460) dumps the raw markdown payload
  into the `TLDR` field rather than re-parsing it via
  `plan.ParseFile` + `TasksFromPlanText`. The file is functional and
  appears in `flowstate plan list`, but the body has the original
  frontmatter nested inside the persisted file's TLDR section. The
  agent-driven `plan_write` path doesn't have this quirk because it
  parses the input properly. Consider unifying the two paths in a
  follow-up.
