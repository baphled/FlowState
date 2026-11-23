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

The result is text: a first line `child <id> <status> turns <n> tokens <n>` followed by the child's final message,
capped at 50 KB / 2000 lines with a truncation notice. A child that did not complete is marked `is_error` and carries
its reason. Parallel results are one block per task, each with its own cap.

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
- A child cannot delegate unless its `tools` lists `delegate`. Claude Code allows nesting by default, OpenCode gates
  it by permission and Pi by the child's tool list; FlowState starts closed. `MaxDelegationDepth` (2) stays as the
  hard backstop.

### Decisions

| # | Decision |
|---|---|
| D1 | Order and parallelism are the model's: several `delegate` calls in a turn run concurrently, capped at 4 running and 8 per call. There is no engine scheduler and no dependency graph. |
| D2 | The parent session is the coordinator and is a model with the `delegate` tool. |
| D3 | Profiles are declarative files as above; an unknown field, a tool name that is not registered, or a missing `name` or `description` fails the load. |
| D4 | The profile body is the child's system prompt. |
| D5 | The engine parses no verdict. An independent check is a reviewer profile the model calls. The engine adds a delegation receipt (child id, status, turns, tokens, reason) to the parent's log; gates are unchanged. |
| D6 | The cap is `max_turns` per profile. The existing reservation caps the child at its grant (the tool loop spends one unit per provider turn today) and is released to the parent on settlement, which happens on completion and cancellation. Recording the child's consumption against the parent, reporting tokens, and settling on `failed` (a provider failure sets the status and leaves the reservation unsettled today; with no production caller of `ChargeReservation`, a settled child releases its whole grant, spent or not) are part of the delegate packet. |
| D7 | A failed, cancelled or partial child returns to the parent as an `is_error` tool result with its reason; the parent model decides. |
| D8 | An interrupted child (not terminal: a crashed or paused run that never finished) resumes by `delegate {resume: <id>}`. A parent that crashed mid-delegate is not re-issued on resume: replay skips a dangling tool call and never re-issues it (the existing rule, `execloop.go`), and for `delegate` the model is additionally told the child's state and may resume it. A terminal child cannot be resumed; the model starts a new one. |
| D9 | The child's working directory is the parent's unless `cwd` is given. |
| D10 | Released as v1.10.0 after the delegate packets (the packet list is in the project records, not in the repository). |
| D11 | No run-level lease: each child is an ordinary session with the existing per-session protection. |
| D12 | There is no "skipped" child: nothing is scheduled, so nothing is skipped. |
| D13 | A child that reaches `max_turns` mid-tool-use ends `failed` with reason `max_turns` and returns its partial text marked partial. Today the loop falls out after the last turn's tool calls and completes the session with no `max_turns` marker (`turn.end` records only the last finish reason). This adds a cause to the `failed` row of ADR-002's status table. |

### Cancellation

An abort of the parent cancels every running child with the parent's reason, in the order the engine already uses
(final event, then status, then settlement under a detached context). Pi's SIGTERM-then-SIGKILL after five seconds
is the model; children are in-process here, so the existing cancellation settles them.

### Swarm manifests

`swarm run <manifest>` keeps its tables and receipts. A manifest becomes the brief the coordinator receives, with the
packets listed as tasks and their profiles, as Pi's `/implement` prompt template does; the model issues the `delegate`
calls and the receipts are filled from the delegation receipts. A deterministic mode that issues the calls in
dependency order (Pi's `chain`) is a possible later option.

### Engine changes this decision depends on

None of these exists today; each is part of a packet in the plan, and the ADR does not claim otherwise.

- A `SystemPrompt` field on `ExecLoopConfig`, delivered as a system message (see Agent profiles).
- Concurrent tool calls: `executeCalls` runs calls one after another, so several `delegate` calls in one turn running at
  once (D1) is new. The cap of 4 running and 8 per call applies per parent, so two overlapping calls share it.
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
- ADR-006 (crash recovery): a crashed parent is recovered, not re-run. The replay rule in `execloop.go` skips a dangling
  call and never re-issues it, and `delegate` adds the child's state.
- ADR-007 (transport adapters): the HTTP run accepts the same `--agents` scope as the CLI.
- ADR-008 (Anthropic provider): the subscription route refuses tools; the API-key route refuses them until the
  prerequisite packet lands.
