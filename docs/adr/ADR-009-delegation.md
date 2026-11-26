---
id: adr-delegation
title: ADR - Delegation to Child Agents
type: adr
status: accepted
adr_group: Go CLI
created: 2026-11-23
modified: 2026-11-23T00:00
deciders:
  - Engineering Team
consulted:
  - Architecture Review Board
informed:
  - FlowState Contributors
tags:
  - type/adr
  - project/flowstate
  - topic/architecture
  - topic/adr
  - topic/delegation
  - flowstate/cli
  - flowstate/adr
  - timeline/november-2026
aliases:
  - Delegation
  - delegation
---

# ADR - Delegation to Child Agents

## Status

**Accepted** — 2026-11-23. Replaces the engine-driven coordinator of the first child-execution plan.

## Context

`flowstate swarm run` records a manifest's packets, children and receipts, but runs no provider turn for a child. The
first plan for making children execute invented an engine coordinator that walked a dependency graph, a run lifecycle
table and a "skip dependents" policy. No mature coding agent does that. The harnesses that have shipped delegation
agree on one design:

- **Pi** (the `subagent` extension, read in full): a tool the parent model calls; a profile is a markdown file with
  `name`, `description`, `tools`, `model` and a system-prompt body; the child is a fresh process with only the task;
  the last assistant message returns, capped at 50 KB / 2000 lines; modes single, parallel (8 tasks, 4 at once) and
  chain; a failed child comes back as a tool error and the parent model decides; Ctrl+C sends SIGTERM, then SIGKILL
  after five seconds; there is no resume and no budget.
- **Claude Code** (documentation): the `Agent` tool; the same profile format plus `maxTurns`, `permissionMode`, hooks;
  a fresh context holding the delegation prompt; only the final message returns; several calls in one turn run
  concurrently (20 at once); a child may itself delegate to a bounded depth (default three layers;
  `CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH` lowers it, and omitting `Agent` from `tools` prevents it); `tools` is an
  allowlist and `disallowedTools` a denylist; a child that reaches `maxTurns` returns partial output marked partial; a
  child can be resumed by agent id.
- **OpenCode** (agents documentation): agents are markdown or JSON profiles with a mode (primary or subagent), a tool
  and permission set and a step cap (`steps`) after which tools are removed and the agent is asked for a text summary;
  subagents run in the background and report to the parent. (Earlier recollections of a `task` tool with `task_id`
  resume are not confirmed by the current documentation and are not relied on.)
- **Roo, Goose, Cline, Aider** (recalled): the same shape where they delegate at all (an isolated context, a summary
  returns, restricted tools, shallow nesting); Aider's architect/editor is a fixed pipeline, not delegation.

FlowState already has what none of them has together: child sessions persisted with a parent id and a depth cap,
budget reservations (a grant reserved before the spawn and released on settlement, which is completion or
cancellation), tool allowlists enforced in process, cancellation that settles children, crash recovery, receipts and
an HTTP API.

## Decision

**Delegation is a tool the parent model calls. The engine does not schedule children.** Everything below follows
from copying the agreed design and adding only what FlowState already has.

### The `delegate` tool

| Parameter | Meaning |
|---|---|
| `agent` and `task` | One child: a profile name and the brief. |
| `tasks` | Parallel: an array of `{agent, task, cwd?}`, at most 8, at most 4 running at once. Exclusive with `agent`/`task`. |
| `resume` | A child session id to continue (see D8). |
| `cwd` | Working directory of a single child; defaults to the parent's. The user creates it, not the engine. |

A completed single child returns JSON, `{child, status, turns, text}`: `text` is the child's final message, capped at
50 KB / 2000 lines with a truncation notice, and `turns` stands in for tokens until child token accounting lands (as in
the receipt). A child that did not complete is marked `is_error` and carries
its reason. Parallel results are one block per task, each with its own cap: a call with `tasks` returns one JSON object,
`{results: [{index, agent, child, status, turns, text?, reason?}, ...]}`, in input order, in which a failed, cancelled
or refused child is an entry with its status and reason (not an `is_error` result, so the model keeps its siblings'
results); only invalid arguments, validated before any child starts, and infrastructure failures are errors.
The cap of 4 running children applies per call: the calls of one turn are executed one after another.

### Agent profiles

A profile is a markdown file: YAML frontmatter, then the system prompt.

```markdown
---
name: reviewer
description: Reads a change and reports defects
tools: [fs.read]
model: inherit
max_turns: 25
---
You review. You do not edit. Begin your final message with VERDICT: pass or VERDICT: fail.
```

- `name` and `description` are required; `tools` is the allowlist (omitted means no tools, which is valid for a pure
  reasoning child), `model` is a model name or `inherit` (also the default) for the parent's, `max_turns` defaults to
  25. An unknown field fails the load (Claude Code ignores them silently; a profile gates
  tools, so FlowState is stricter).
- The body is the child's system prompt. `ExecLoopConfig` has no such field today and `provider.ChatRequest` carries the
  system prompt only as a message of role `system`: a new `SystemPrompt` field, delivered as that message, is a
  prerequisite change of the agent-profile packet.
- Locations: user `~/.config/flowstate/agents/*.md`; project `.flowstate/agents/*.md`. Project profiles load only on
  an explicit `--agents project|both`; `both` refuses a profile name that exists in both
  locations (`config_invalid`, naming both files) and has no override, so a repository cannot replace a trusted user
  profile. Pi gates them with an `agentScope` parameter and an interactive confirmation,
  and FlowState has no interactive prompt, so the opt-in is a flag. This repository keeps no agent-tool configuration
  (`AGENTS.md`) and ignores `.flowstate/`; a project that wants committed profiles unignores `.flowstate/agents/`
  (`.flowstate/*` then `!.flowstate/agents/`).
- **Read-only is enforced**, not requested: a profile without `fs.write`, `fs.edit` and `exec.run` cannot write,
  because the allowlist is checked in the engine. Pi's own reviewer prompt says to assume tool permissions are not
  perfectly enforceable.
  A profile that lists `exec.run` is not filesystem-confined: `exec.run` runs allowlisted binaries (`make`, `go`,
  `git`) that can execute arbitrary code, and its confinement is its existing policy (cwd and a per-binary
  allowlisted, secret-free environment: only `go` and `make` get `HOME` and the Go cache variables, with git
  config and netrc neutralised for them). Residual risks: the split only stops `HOME`/XDG/netrc/gitconfig-file
  credential discovery; it does not stop OS-keyring tokens, system-level credential helpers or ssh keys, and the exec
  allowlist (the subcommand gate) is the real control for `gh` and `git`. It is not a confidentiality boundary against
  allowlisted code runners (`git fetch --upload-pack`, `go test -exec`/`-toolexec`, `make`), and same-UID code can read
  `/proc/<engine>/environ` and the passwd home, as before.
- A child delegates only if its `tools` lists `delegate`, and only while a level is left below it. Claude Code allows
  nesting by default, OpenCode gates it by permission and Pi by the child's tool list; FlowState starts closed, so
  deny-by-default still holds at every level. Nesting is bounded at depth 2 (root, child, grandchild), so a
  coordinator can delegate to a team lead that delegates to specialists. `MaxDelegationDepth` (2) is the hard
  backstop.
- **`delegates` names the profiles a delegating profile may spawn.** A profile that lists `delegate` in `tools`
  declares `delegates: [profile names]`, the only profiles its `delegate` tool may spawn. It fails closed: none
  declared means nothing is allowed (nested enforcement lands with nested delegation, #445). `delegates` needs
  `delegate` in `tools` (the load fails otherwise); names follow the profile-name rule, at most 32, no duplicate, never
  the profile itself; a name outside the loaded scope fails the load, not the spawn. Cycles are allowed; the depth cap
  bounds them. A rule that a child may only spawn profiles whose tools are within its own was rejected: a nested
  review found a read-only reviewer with `[delegate, fs.read]` could spawn a writer, but a subset rule also forbids the
  main use, a read-only team lead delegating to engineers that write. Claude Code (`Agent(type1,type2)` in the tools
  list) and OpenCode (`permission.task` globs) make the permitted agents explicit configuration on the delegating
  agent; FlowState does the same.

### Decisions

| # | Decision |
|---|---|
| D1 | Order and parallelism are the model's: the tasks of one `delegate` call run concurrently, capped at 4 running and 8 per call (the calls of a turn run in sequence). There is no engine scheduler and no dependency graph. |
| D2 | The parent session is the coordinator and is a model with the `delegate` tool. |
| D3 | Profiles are declarative files as above; an unknown field, a tool name that is not registered, or a missing `name` or `description` fails the load. |
| D4 | The profile body is the child's system prompt. |
| D5 | The engine parses no verdict. An independent check is a reviewer profile the model calls. The engine adds a delegation receipt (child id, status, turns, reason; turns stand in for tokens in the receipt: it carries no token counts and a child's usage is not folded into its parent's, see Provider usage below) to the parent's log; gates are unchanged. |
| D6 | The cap is `max_turns` per profile. The existing reservation caps the child at its grant (the tool loop spends one unit per provider turn today) and is released to the parent on settlement, which happens on completion and cancellation. Recording the child's consumption against the parent, reporting a child's tokens, and settling on `failed` (a provider failure sets the status and leaves the reservation unsettled today; with no production caller of `ChargeReservation`, a settled child releases its whole grant, spent or not) are part of the delegate packet. |
| D7 | A failed, cancelled or partial child returns to the parent as an `is_error` tool result with its reason; the parent model decides. |
| D8 | An interrupted child (not terminal: a crashed or paused run that never finished) resumes by `delegate {resume: <id>}`. A parent that crashed mid-delegate is not re-issued on resume: replay skips a dangling tool call and never re-issues it (the rule in `rebuildHistory`, `execreplay.go`), and for `delegate` the model is additionally told the child's state and may resume it. A terminal child cannot be resumed; the model starts a new one. Resume is single-form (`resume` with an optional follow-up `task`, never with `agent`, `cwd` or `tasks`) and is refused, with nothing written, for an id that is not a child of this parent (unknown, another parent's, a root), a child that is not `active` or `paused`, a child whose exec lease is held (it is running; every run holds it as a judged handle owned by `delegate:<run id>` (ADR-011), authorised read-only, then claimed, then re-read under the claim, passed to the loop and released only after the child's settlement has returned; a row that cannot be judged counts as running), a recorded profile that is no longer loaded or whose sha256 changed, and a child that has used all its `max_turns` (the turns used are its `assistant.message` events, plus one for each turn a crash cut off, so a child may lose at most one turn per crash but the count is of the turns visible in the log: a crash before anything was written leaves a request uncharged). The child continues with `max_turns` minus the turns used as its grant: the reservation, one row per child, is charged for those turns and the remainder is drawn from the parent's pool first, so a pool that cannot cover it is a failed `budget_exhausted` result that changes nothing. A `task` is appended as a `turn.start` user message. Every run records `delegation.started` in the parent's log when the child exists (also for a resume, with `resumed: true`), and the receipt carries `resumed: true`. The crash answer is written by `RecoverCrashedSession` (ADR-006) on every run of a session, after the run lease (ADR-011) and before the first provider request, not by the loop's read-only replay. For every `delegate` call without a result, whenever it happened (a second crash of a resumed parent is answered too, though no new `turn.start` marks it), it appends a recorded `tool.result` error (actor `engine`, code `delegate_interrupted`) saying the call was interrupted by a crash and listing each child whose `delegation.started` follows that call (and precedes the next `delegate` call): its id and live status (`not found` when its session row is missing), and, for an unfinished one, `resume it with delegate {resume: "<id>"} or start a new one`; for one with a `delegation.completed` receipt, that it finished, its result was not delivered and cannot be read back, and that the model starts a new one only if it still needs the work. "no child had started; call again" is said only when no `delegation.started` follows the call. Only ids and statuses reach the text; a profile name or path never does. The leases of the listed unfinished children are released first (`Store.ClearLeaseOwnedBy`: only an owner starting `delegate:` or exactly `delegate`, the name an earlier build used; an empty prefix is refused): a delegate run killed with its process leaves its lease, a lease never expires, and no live run holds the parent. The result is appended before `session.interrupted`, so a retry after a crash between them is safe and answers nothing twice. Recovery does not judge liveness itself: every writer that runs it (`session run`, serve, `delegate resume`, `session compact`) holds a judged lease (ADR-011), so a live run in another process is refused before anything is written, and it does not touch the session's own lease row (a legacy row is refused by every acquirer until `session unlock --force`); it still clears the listed unfinished children's `delegate:` rows without judging them, known limit L1 (ADR-011), that can fence a live resumed child. Limit: the answer is appended at the end of the log, so a call left unanswered by an earlier build is answered after later events, and the history can read assistant{d1} "a", assistant{e1} "b", tool(e1), tool(d1) (two assistant messages: the late answer d1 follows a later turn's e1): valid by `ChatRequest.Validate` (pairing, not adjacency) but not adjacent, which a provider that wants the result next to its call may reject; the log is not reordered. |
| D9 | The child's working directory is the parent's unless `cwd` is given. |
| D10 | Released as v1.10.0 after the delegate packets (the packet list is in the project records, not in the repository). |
| D11 | No run-level lease: each child is an ordinary session with the existing per-session protection. |
| D12 | There is no "skipped" child: nothing is scheduled, so nothing is skipped. |
| D13 | A child that reaches `max_turns` mid-tool-use ends `failed` with reason `max_turns` and returns its partial text marked partial. The loop used to fall out after the last turn's tool calls and complete the session with no `max_turns` marker; it now executes that turn's calls, records their results, and fails the run with `ErrMaxTurns` (code `max_turns`, exit 5, HTTP 409) through the provider-failure path: a terminal `provider.error` event with reason `max_turns` comes before the `failed` status. A turn that answers without tool calls and finishes normally still completes, even on the last allowed turn; one cut off at the output cap fails with `output_truncated` (ADR-010 amendment 2026-11-25). The delegate tool reports the child as failed, partial; the parent continues (D7). This adds a cause to the `failed` row of ADR-002's status table. |
| D14 | Nested delegation (root, child, grandchild). **Who may delegate:** a child whose profile lists `delegate`, at depth 1; a profile listing `delegate` for a child at depth 2 is refused at spawn (and on resume) with `delegate is not available at depth 2: the nesting limit is 2` and no session is created; `CreateChildSession`'s depth check stays as the hard backstop. **Binding:** the child's registry gets its own `delegate` tool with the CHILD as parent: its session, streamer, pool, working directory (confinement is relative to the child's directory, which lies inside the root's), the root run's loaded profiles and, as `ParentEvents`, the child's per-call event writer, so a grandchild's `delegation.started` and `delegation.completed` land in the child's log and the child's in the root's. Its allowlist is its profile's tools exactly. **Which profiles:** the profile's own `delegates` list decides (the precedent is Claude Code's `Agent(type)` and OpenCode's `permission.task`, not a subset rule): the delegate tool bound to a child may spawn only those names, for a single `agent`, for every `tasks` entry (validated up front: one name outside the list refuses the whole call and no child is created) and for `resume` (the child's recorded profile name must be listed). It is fail-closed: a profile that lists `delegate` with no `delegates` may delegate to nothing, and the refusal says `profile X lists delegate but declares no delegates`; otherwise `profile X may not delegate to Y: it is not in X's delegates`. The tool's description lists only the allowed profiles. The root's own tool is unrestricted over the loaded scope (the user's explicit run), and a name that is not loaded is refused at load (`CheckDelegates`). Without this a read-only reviewer that lists `delegate` could reach a writer. **Budget:** a grandchild reserves from the child's pool, built from the child's grant, before it exists; the child's own turns draw from the same pool, so the grant bounds the subtree. Settlement returns the unspent part up the chain: a child is charged its own turns plus what its descendants spent, and the rest returns to the root's pool. **Cancellation:** the child runs under the root's context, so cancelling the root cancels the child's loop and its in-flight delegate call, which cancels the grandchild with the same reason, each level in the existing order (final event, status, settlement on a detached context). A grandchild's failure is data for the child (D7), a child's for the root. A panic through a delegating child closes the per-call event writer on the unwind before the child is settled, so each level ends `failed` (reason `panic`) with its reservation settled and its slot released. **Concurrency:** the per-call cap (4 running, 8 per call) multiplies across levels, so a root run also carries a cap of 8 descendant runs running at once (`DefaultDescendantLimit`), a semaphore created with the root's tool and passed down; a run past it waits for a slot, respecting its context, and never fails. A run holds one slot while it executes provider turns and tools and gives it up while its delegate call waits for its own children, then waits for one again: otherwise eight children each holding a slot and waiting for a grandchild would deadlock. **Resume:** `ResumeDelegatedChild` recovers the child as a crashed root (D8): a dangling delegate call of the child is answered with ITS grandchildren, their stale leases are cleared, and the child's own tool is bound again. Limits: a resumed child's pool is its `max_turns` minus the turns in its own log, so spend of grandchildren that finished before a crash is not deducted again after it; a resumed child runs in the parent's directory, not the narrower `cwd` it was started with. |

**Provider usage (D5, D6).** The loop now records the provider's token usage on each turn's `assistant.message`
(`usage`, omitted when the provider reports none), and `session doctor` totals it. That does not change D5 and D6:
budgets and the delegation receipt still count turns, not tokens. A turn without usage adds nothing, so the totals can
be partial; `turns_reported` against `turns_total` shows how partial.
The totals are a lower bound on spend: a turn that ends in a provider error, a cancellation or a pause records no
`assistant.message`, and a delegated child's usage is not included in its parent's totals (the delegation receipt
carries none). `last_context_tokens` comes from the last turn that reported usage, so it can be stale, and it
overstates the next request for a reasoning model, whose output tokens include reasoning that is not resent; it is an
estimate, not an exact count.

The working directory is checked once, before the child starts. A symlink swapped in after that check can let a child
read outside it, because the file tools resolve their root again on every call. That needs another actor able to create
symlinks (`exec.run` or an external process). Pinning the root inside the tools is tracked as a follow-up.

### Cancellation

An abort of the parent cancels every running child with the parent's reason, in the order the engine already uses
(final event, then status, then settlement under a detached context). Pi's SIGTERM-then-SIGKILL after five seconds
is the model; children are in-process here, so the existing cancellation settles them.

### Swarm manifests

`swarm run <manifest>` keeps its tables and receipts. A manifest becomes the brief the coordinator receives, with the
packets listed as tasks and their profiles, as Pi's `/implement` prompt template does; the model issues the `delegate`
calls and the receipts are filled from the delegation receipts. A deterministic mode that issues the calls in
dependency order (Pi's `chain`) is a possible later option.

Roles are data, not engine code. A manifest may name a top-level `coordinator:` (the profile that runs the swarm; its
prompt and tools come from that profile, and execute mode requires it), and each packet may carry `role:` (the profile
it is delegated to), beside `task:` (at most 8192 bytes) and the existing `prerequisites:`. A manifest without them
still validates and runs as before. `swarm.Brief` renders the coordinator's first message as a pure function: packets
in dependency order, gates by name and description only (never the command), and fixed instructions. The operator's
values sit in a fenced block marked as data, each Go-quoted, and validation bounds the manifest (at most 64 packets, a
64 KiB brief) and refuses control, format and line-separator characters and ids outside
`[A-Za-z0-9][A-Za-z0-9._-]{0,63}`. `app.ExecuteSwarm` implements this in the engine and `swarm run <manifest>
--execute` calls it (through `app.ExecuteSwarmFile`, which loads the manifest and builds the hash-checking admission
log and the gate runner in the workdir; the profile scope, provider and model resolve as for `session run` and
`session new`). A worked example is in `docs/examples/swarm`. A result prints as one JSON object
(`swarm-run-execute`), and the existing exit codes carry the status: accepted 0, `not_accepted` and `gate_failed` 8, a
failed coordinator by its own cause (provider 3, budget, `max_turns` or `output_truncated` 5, otherwise 1), cancelled
130. `ExecuteSwarmResult.Cause` carries that cause as an error. It checks every precondition before it creates a
session or run (the manifest, a loaded coordinator and role profiles, a role on every packet, `delegate` in the
coordinator's tools, a database outside the workdir) and gives the coordinator a delegate tool restricted, through the
allowlist above, to the manifest's roles; a coordinator profile that lists `delegates` narrows this to the
intersection, and a packet role the intersection leaves out is refused up front. The engine maps a delegated child to
its packet by the marker the brief tells the model to start each task with, `[packet <id>]` at the very start of the
task (`swarm.PacketRef`, which refuses wrapped or Unicode-spaced forms), read from the child's own first task after
the coordinator stops; the delegation receipts carry no packet argument, so `delegate*.go` is untouched. A marker that
names no manifest packet, or a role other than the packet's, or whose task text after the marker does not contain the
packet's task verbatim (whitespace runs collapsed), leaves the child unattributed (stored with an empty packet id)
rather than failing the run, and a packet no completed child stands for is recorded not run; the first completed child
is named as the one that admitted it. Only a coordinator that completed lets the engine run the gates and the
admission; a failed, cancelled or max-turns coordinator stores a failed run and runs no gate, and the model's claim
about the gates is never read. A swarm with no packet, no gate or a packet whose role is the coordinator is refused
before anything is created. The gates must run in the tree the children worked in: `swarm.NewGateRunnerIn(dir)` runs
them with `cmd.Dir = dir`, and `ExecuteSwarm` refuses a runner whose `Dir()` is not the workdir. A passed deadline is
a failed run with reason `deadline`; a budget-exhausted coordinator stays paused. The store keeps only pass or fail,
so `gate_failed` and a cancel during the gates both store fail with the packets admitted and no gate rows: a later
`swarm status` needs the richer result (a follow-up).

### Engine changes this decision depends on

None of these exists today; each is part of a packet in the plan, and the ADR does not claim otherwise.

- A `SystemPrompt` field on `ExecLoopConfig`, delivered as a system message (see Agent profiles).
- Concurrent tool calls: `executeCalls` runs calls one after another, so several `delegate` calls in one turn running at
  once (D1) is new. The cap of 4 running and 8 per call applies per call: the loop still executes a turn's calls one
  after another, so concurrency exists only inside one `delegate` call.
- A working directory for the child that defaults to the parent's (D9): the registry's workdir today is a per-session
  directory beside the database.
- Consumption recorded against the parent's reservation, a token or turn count in the result, and settlement on
  `failed` (D6): `ChargeReservation` has no production caller today (tests only), so the loop records no consumption
  and a settled child returns its whole grant to the parent, spent or not.
- Profiles are discovered beyond the working directory (the user directory), which ADR-005 rejects in its Alternative
  D because wiring that depends on where it was found is not disambiguated by the hash. Profiles sit outside that
  hash, so the delegation receipt records the profile's path and SHA-256 and the child's session records the profile
  name.
- Tool support on the Anthropic API-key route (a prerequisite packet; neither Anthropic route supports tools today).

## Consequences

- The engine gets simpler than the first plan: no run lifecycle table, no scheduler, no lease, no skip state.
- The behaviour is as good as the parent model's judgement, which is what every mature harness accepts.
- A tool-calling parent needs a provider that supports tools. Today only the z.ai route does: the Anthropic
  subscription route refuses tools and the Anthropic API-key route refuses structured tools. Tool support on the
  API-key route is a prerequisite packet.
- Project profiles are repository-controlled prompts; they are off unless asked for.
- Compaction, hooks, skills, MCP, memory, background children and a TUI are out of scope and remain separate
  programmes.

## Alternatives rejected

- **An engine-driven coordinator** walking a dependency graph (the first plan): no mature harness does this; it
  duplicates what the model does and adds a failure policy the model should own.
- **A child as a separate process** (Pi's example extension spawns one): FlowState's children are
  sessions in the same store, so a process buys isolation we already have and loses the shared budget and receipts.
- **No resume** (Pi): FlowState persists children already; Claude Code documents resume by agent id.
- **A money budget per child**: nobody caps money; turns are the universal cap, and the reservation already bounds
  tokens.

## Cross-links

- ADR-002 (session run lifecycle): the `failed` row gains `max_turns`; child sessions follow the same status machine.
- ADR-005 (config composition): profile discovery beyond the working directory is the class its Alternative D rejects;
  the delegation receipt records the profile's path and SHA-256 in place of the composition hash.
- ADR-006 (crash recovery): a crashed parent is recovered, not re-run. The replay rule in `rebuildHistory`
  (`execreplay.go`) skips a dangling call and never re-issues it, and `delegate` adds the child's state.
- ADR-007 (transport adapters): the HTTP run accepts the same `--agents` scope as the CLI.
- ADR-008 (Anthropic provider): the subscription route refuses tools; the API-key route refuses them until the
  prerequisite packet lands.
