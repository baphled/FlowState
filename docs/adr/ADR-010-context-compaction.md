---
id: adr-context-compaction
title: ADR - Context Management and Compaction
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
  - topic/session
  - topic/compaction
  - flowstate/cli
  - flowstate/adr
  - timeline/november-2026
aliases:
  - Compaction
  - Context Management
---

# ADR - Context Management and Compaction

## Status

**Accepted** — 2026-11-23. Decision only: no code changes with this ADR. ADR-009 listed compaction as a separate
programme; this is that programme's first record.

## Context

A session that runs long enough fills the model's context window and the provider refuses the next request. FlowState
has no answer today: the execution loop (`internal/app/execloop.go`) rebuilds the provider history by replaying the
session's events once at the start of each run (`replayInto`), appends to that in-memory history as turns complete, and
sends the whole of it with a request-time system prompt (`withSystemPrompt`: never persisted, never replayed). Nothing
bounds the history, and no component knows how large the window is: the provider port's `Models()` returns names only.

What FlowState does have is the measurement (#449). Each `assistant.message` payload carries the provider-reported
`usage` when there is one; `ContextTokens` sums input, output, cache read and cache write; `ExecLoopResult` and the
session diagnosis report `LastContextTokens`; and the comment on `ContextTokens` already names compaction as its
consumer. The log is append-only and replayable (ADR-006), so a compaction must be an event, not a rewrite.

What the proven harnesses do:

- **Pi** (its documentation, `compaction.md` and `sessions.md`, read in full). Automatic compaction triggers when
  `contextTokens > contextWindow - reserveTokens`, with `reserveTokens` defaulting to 16384. In a multi-turn run Pi
  checks after tools finish and their results are appended, before the next assistant response; it also checks before
  a new user prompt. The cut point is found by walking back through the messages accumulating token estimates until
  `keepRecentTokens` (default 20000) is reached; valid cut points are user, assistant, bash-execution and custom
  messages, never a tool result. The older span goes to an LLM call that returns a structured summary (Goal,
  Constraints & Preferences, Progress, Key Decisions, Next Steps, Critical Context, plus read and modified file
  lists), given the previous summary as iterative context. A `CompactionEntry` holding the summary, `firstKeptEntryId`,
  `tokensBefore` and the summarising call's `usage` is appended; the next request is the system prompt, the summary
  and the messages from `firstKeptEntryId` on. The original entries stay stored. A cut inside one over-long user-message
  span is handled by summarising the span's prefix separately and merging. A provider context-overflow error or an
  early `length` stop selects one compact-and-retry; if that compaction fails, nothing is retried. `/compact
  [instructions]` triggers it by hand, and compaction can be disabled while the manual command stays. Messages are
  serialised to text before summarising, thinking blocks are omitted, and tool results are truncated to 2000
  characters. Extension hooks can cancel or replace the summary.
- **Claude Code** (recalled; no web access while this was written, so none of it is relied on beyond the idea): it
  compacts automatically near the window limit, offers `/compact` with optional instructions, and delegates to
  subagents whose contexts are separate and whose final message returns (ADR-009 records the delegation side from its
  documentation).
- **OpenCode** (recalled, not verified here): it also summarises automatically when the window fills. Nothing in this
  ADR depends on its details.

The shape Pi's documentation describes, and that the recalled harnesses are expected to share, is one: measure the
provider-reported context, summarise the old span with the model, keep the recent tail verbatim, record the summary as
a session entry, rebuild from summary plus tail.

## Decision

**FlowState compacts as Pi's documentation describes, as an appended `context.compacted` event the replay honours.
The engine never rewrites or deletes events.** FlowState adds only what its own log, lease and delegation model need.

### When

- **Measure.** The trigger compares the context of the last provider-reported turn (`LastContextTokens`, the sum from
  #449) plus an estimate of what was appended since (tool results) against `window - reserve`. The estimate is bytes
  divided by four; it only has to be early enough, because the provider-reported figure corrects it each turn. The
  baseline has three cases. After a compaction the last `assistant.message` usage still describes the context that was
  replaced, so the baseline is the last event's `tokens_after` until a turn after it reports usage; the trigger cannot
  re-fire on stale figures. When no turn has reported usage (`LastContextReported` is false, as on the subscription
  route) or the session was resumed, the baseline is the estimate of the whole rebuilt history, because the logged
  figure measured the live history and the rebuild can be smaller or larger. A compaction whose `tokens_after` is
  still above `window - reserve` fails. Any failed automatic compaction (`context_too_large`, `no_cut`, an empty or
  `length`-finished summary, a failed summary call) writes nothing, is not retried within the run, and the run
  continues: it fails only when the provider itself then fails, so a small window cannot fail a request that still fits.
  The estimate can undercount; the cost of that is bounded, since a compaction that leaves `tokens_after` above the
  line fails, and the number of compactions is bounded by `max_turns` and the budget.
- **Where in the loop.** Before each provider request, which covers "after tool results are appended" and the start of
  a run, after replay (a resumed session may already be over the line).
- **Window.** The provider port's `Models()` carries no window, so a window comes from one of three sources, in this
  order. A session that runs under a profile (a delegated child, or the swarm coordinator of #450, which is a root
  session driven by its profile) takes the profile's `context_window` field (a positive integer of tokens; the profile
  loader already rejects unknown fields). Any session otherwise takes the config key `compaction.context_window` (the
  secret-key filter of ADR-005 does not match it), then the registry table of known `provider/model` pairs with their
  documented windows. A plain `session run` root has no profile, so it takes the config key, then the table. The config
  key is one integer for every model, so a session on a different model than the one it was set for needs it changed;
  the table is per model. A model with none of these has an unknown window. An unknown window disables automatic
  compaction (manual still works) and `session doctor` prints a warning. There is no default window: a wrong guess
  either compacts constantly or never. The table's values are filled in by the packet that adds it, each with a source
  link.
- **Reserve.** 16384 tokens, as in Pi's documentation, raised to the run's `MaxTokens` when that is larger: the
  reserve exists so the next response fits. It is a config value (`compaction.reserve`, in tokens), not a flag. The
  config loader rejects any key matching `key`, `token`, `secret` or `password` (ADR-005), so no key here may contain
  "token"; a `compaction` object in the config schema is part of packet (a).
- **Keep recent.** 20000 estimated tokens, as in Pi's documentation (`compaction.keep_recent`, in tokens).
- **Switch.** `compaction.enabled`, default true.
- **Manual.** `flowstate session compact <id> [--instructions text]`. It takes the session's exec lease like a run and
  is refused, with nothing written, while the lease is held or when the session is not `active` or `paused`. It runs
  crash recovery first, as `session run` does, so a crashed turn is answered before the cut is chosen.
  It has no loop and no per-invocation budget pool, so it draws nothing from one: D12's unit applies to the
  automatic trigger of packet (d). It leaves a paused session paused. The exec loop honours the compaction on its
  next run; the plain text-turn path never replays the log, so a compaction does not affect it.
- **Overflow.** A provider context-overflow error, or a `length` finish with no tool calls on a request that was
  already over `window - reserve`, compacts and retries the request once. A second failure is an ordinary provider
  failure. The provider adapters must classify overflow (`ErrContextOverflow`); today none does, and the loop's
  `provider.error` is terminal, so the overflow case writes a non-terminal `provider.error` (reason `context_overflow`)
  and no `failed` status.

### What

- **The summary call.** A provider call on the session's own provider and model with no tools, using the `Streamer`
  already in the loop. There is no `compaction_model` (D5). The span is serialised to text, as Pi's documentation
  describes, so the model does not continue it as a conversation: roles labelled, assistant tool calls as one line,
  each tool result truncated to 2000 characters, nothing else. A user or assistant text is clipped at 8192
  characters, and the whole request at 400 KiB (about 100k tokens): when the span does not fit, its oldest entries
  are left out ("[earlier part of the conversation omitted]"), so the summary covers the recent part only. The
  events before the cut are still dropped from the rebuilt history: what was omitted from the input is lost.
- **The prompt** asks for the Pi sections (Goal, Constraints & Preferences, Progress, Key Decisions, Next Steps,
  Critical Context) and the files read and modified. `--instructions` is appended as a focus line. The previous
  summary, if any, is passed as iterative context and the span summarised is from the previous `first_kept_seq`.
- **Cap.** The call is made with `MaxTokens` 8192. A summary that finishes `length`, or is empty, fails the
  compaction: nothing is appended, because a truncated summary is a silent loss.
- **Cut point.** The span is walked backwards from the end of the log, accumulating estimates until 20000 tokens are
  kept. The cut falls only on a `turn.start` (user task) or on the `assistant.message` of a turn, so a kept turn keeps
  its text, all its calls and their results: the replay then rebuilds it in the shape the live loop sent. A
  `tool.call` and its `tool.result` are one unit, and so are all the calls of one assistant turn: no cut lands between
  them, and none lands on a `tool.call` or `tool.result`. A cut that cannot satisfy this moves earlier, never later,
  so the kept tail never begins mid-pair. The log carries no turn id, but the order per turn is deltas,
  `assistant.message`, then call and result pairs, so the `assistant.message` is the turn's first replayed event. When
  no eligible cut exists, for example one turn or one huge pair, the compaction fails with reason `no_cut` and writes
  nothing. If one user task is longer than the keep budget, the cut lands at an assistant-turn boundary inside it, and
  the summary says the task was split. Pi's two-summary merge for this case is not adopted (D6).
- **What the rebuilt history holds.** This ADR depends on the replay rebuilding the shape the live loop sent: one
  assistant message per turn with its text and all its answered calls, then the results, user messages in place. Until
  the fix that makes `replayInto` do so ("fix(app): rebuild a resumed run's history in the shape the loop sent",
  #453) a resumed run dropped assistant text; packet (b) must not start before it has merged. Steering messages
  (`session.steered`) are not injected into the loop's history today and so are in neither the history nor the span;
  wiring steering into the loop is a separate packet, and compaction does nothing about it.
- **Always carried.** The system prompt (request-time, so no event holds it). The original task, verbatim, in the
  rebuilt first message whenever it falls before the cut (D4). The kept tail, replayed as the rebuild shapes it. The
  span summarised is exactly the events the rebuild replays: user messages, assistant text, calls and results. A call
  with no result is skipped by replay today and stays skipped; compaction runs between turns, when the loop has
  appended all results, so no live call is pending.

### How: the event

`context.compacted`, actor `engine`, appended through the session's event writer under the run's lease. Payload:

| Field | Meaning |
|---|---|
| `summary` | The summary text, at most 8192 tokens. |
| `first_kept_seq` | The `seq` of the first event the rebuilt history replays verbatim. Less than this event's own `seq`. |
| `tokens_before` | Estimated context replaced; the provider-reported figure when one exists. |
| `tokens_after` | Estimated context of the rebuilt history. |
| `reason` | `auto`, `manual` or `overflow`. |
| `model` | The model that wrote the summary. |
| `usage` | The summarising call's `provider.Usage`, absent when not reported. |

Rules:

1. **Replay** takes the last `context.compacted` event C. The history is one first message, then the events with
   `seq >= C.first_kept_seq` replayed as today. The first message opens with a fixed heading that says an automatic
   summary replaced the earlier conversation; if the original task is before the cut, it follows under "Original
   task", and the summary comes last, between the lines `--- summary ---` and `--- end of summary ---`, so model
   output cannot forge a heading above the genuine sections. A summary holding the closing line is refused when the
   event is written and when it is read. If the kept tail begins with a user message it is merged into
   that first message rather than leaving two user messages in a row, so no provider has to tolerate them. A
   `tool.result` whose call is before `first_kept_seq` is skipped, never orphaned; this matters because the crash
   answer of ADR-009 D8 is appended at the end of the log, after later events.
2. **Repeated compaction.** Only the last event counts. Its span began at the previous event's `first_kept_seq`, so the
   previous summary and the tail it kept are summarised together, as Pi's documentation describes.
3. **Transaction.** The summary call finishes before anything is written; the event is a single append. A crash before
   the append leaves the log as it was, and the next run recomputes the cut and calls again: the summarisation is
   idempotent to retry, and its only cost is the repeated call. A crash after the append is a completed compaction.
   There is no half state: no "compaction started" event.
4. **Append-only and integrity.** The log is unchanged except for the new row. `Store.Replay` checks `seq` contiguity
   and that each payload matches its own `sha256`; it does not chain events, so integrity here means those two checks,
   and the new event passes them like any other. The summary is part of the payload and so of its digest.
5. **Readers.** `session replay` (`ExportJSONL`) exports the full log; the compaction appears as a line, and the
   events before it remain, so an audit sees both what was summarised and the summary. `LoadSessionHistory` and its
   `replaySessionHistory` fail closed on an event type they do not know (`ErrInvalidHistory`), and have their own
   payload vocabulary and no caller outside tests: they are not changed by this design, and until a caller needs them
   a log holding `context.compacted` makes them return `ErrInvalidHistory`. `session doctor` reports the number of
   compactions, the last `tokens_after`, and the summaries' token totals separately from turn usage. The server's
   event listing and follow stream carry the event as they carry any type; packet (b) confirms this by test.
6. **Accounting.** A compaction is not a turn: `max_turns` is counted from `assistant.message` events on resume
   (ADR-009 D8; the root loop counts in memory), and a compaction writes none. The summarising call's `usage` rides on
   the event and is excluded from `usage` totals of turns (`summariseUsage` reads `assistant.message` only), so doctor
   shows it as its own line. The call also draws one unit on the budget pool, as a turn does (decided here, D12):
   `TurnBudget` is 1 in practice, and `finishDelegatedChild` charges `max(turns, grant - pool.Remaining())`, so the
   draw is charged to a child at settlement and a pool-limited session cannot spend unbounded summarising calls.
7. **Trust.** The summary is model output derived from tool results, which can carry attacker text. It is placed in the
   user-role first message under the fixed heading, and is no more trusted than a tool result. The summariser input
   is truncated per result, which also bounds how much attacker text a single result can plant. The summary persists
   in the log and reaches `session replay` and the HTTP events like any payload; this ADR adds no redaction, so any
   secret already visible to the model can reach a summary, and the same log policy that covers tool results covers it.

### Delegation

- A child is a separate session with its own log, so it has its own window, its own compaction and its own
  `context.compacted` events. A root session, including the swarm coordinator, compacts on its own account. Nothing
  in a child's compaction touches the parent.
- The parent's history holds the child's final text only, already capped at 50 KB / 2000 lines by ADR-009; the parent's
  summariser sees it truncated to the 2000-character serialisation limit, so a parent summary may know less of a child
  than the log does. Receipts and `delegation.*` events live in the log, not the context.
- A resumed child (ADR-009 D8) replays from its own last compaction. Resume's turns-used count is `assistant.message`
  events, which compaction does not change, so `max_turns` and delegation depth are unaffected. The grant is not: the
  summarising call draws one unit from the child's pool (D12), so a compacting child can exhaust its pool before it
  reaches `max_turns`, and the effective turns are fewer by the number of compactions.
- A `delegate` call and its result are one unit for the cut (above); the compaction never runs while a delegate call
  of that session is in flight, because it runs between turns.

### Decisions

| # | Decision |
|---|---|
| D1 | Compaction is an appended `context.compacted` event; replay rebuilds from the last one. No event is changed or removed. |
| D2 | Trigger: provider-reported `LastContextTokens` plus an estimate of what was appended since, above `window - reserve`, checked before each provider request. |
| D3 | The window: the profile's `context_window` for a session under a profile (delegated child, swarm coordinator), else `compaction.context_window`, else the registry table. Unknown means no automatic compaction and a doctor warning. There is no default window. |
| D4 | The first rebuilt message holds the heading, the original task verbatim when it falls before the cut, then the fenced summary; a user message at the head of the tail is merged into it, after the fence. |
| D5 | The session's own provider and model write the summary; there is no `compaction_model`. A different model is a later option. |
| D6 | The summary has Pi's structure; the previous summary is iterative context; one summary per compaction (no split-span merge); capped at 8192 tokens, and a truncated or empty summary fails the compaction. |
| D7 | Reserve 16384 (raised to `MaxTokens` when larger), keep-recent 20000, enabled by default. Config keys `compaction.reserve`, `compaction.keep_recent`, `compaction.enabled` and `compaction.context_window`, none containing "token" (ADR-005's secret filter). |
| D8 | Cut only at a `turn.start` or the `assistant.message` of a turn; a turn keeps its text, calls and results together. |
| D9 | `session compact <id> [--instructions text]` takes the exec lease; it is refused while the lease is held. |
| D10 | A context-overflow error, or a `length` finish with no tool calls over the line, compacts and retries once. Adapters gain an overflow classification; the retry writes a non-terminal `provider.error`. |
| D11 | One append is the whole transaction: nothing is written before the summary exists, and retry is a repeated call. |
| D12 | A compaction is not a turn: it does not count against `max_turns`; its usage is recorded on the event and reported separately. The summarising call draws one unit from the session's budget pool, like a turn, so a swarm's budget bounds its compaction cost; it is refused with the usual budget error when the pool cannot cover it. |
| D13 | The summary is untrusted model output in a user-role message; per-result truncation on its input; no redaction is added. |
| D14 | Delegation: each session compacts alone; the parent sees only a child's final text; replay of a resumed child honours its own compaction. |

## Consequences

- A long session continues instead of failing at the window, and the full history stays in the log for `session
  replay` and audit.
- A resumed session reads from its last compaction; the rebuilt history is shorter than the log, so the log is no
  longer a literal transcript of what the model saw. The event says exactly where the line fell.
- The summary can drop detail the model later needs, and a wrong summary is a wrong premise that the model cannot
  check against the log. Manual `--instructions` and the verbatim tail limit it; they do not remove it.
- Until #453, replay did not rebuild assistant text from `assistant.message` events: it replayed `turn.start` tasks,
  `tool.call` and `tool.result` only, and split one turn's calls into several assistant messages. That is fixed there,
  and this design assumes it. `LoadSessionHistory` has its own payload vocabulary (`id`, `ok`, fail-closed, byte
  limits) and no non-test caller; it is not shared with the rebuild and is left as it is (Rule 5).
- Overflow handling needs provider adapters to classify errors, which touches each adapter.
- Unknown-window models never auto-compact; the failure remains a provider error for them, with a doctor warning.

### Not now

Branch summarisation (FlowState has no session tree); extension hooks; prompt-caching strategy (a summarising request
that avoids writing a cache is an optimisation); cross-session memory; a UI for choosing the summarising model; a TUI
command; deterministic read/modified-file tracking (the summary prompt asks the model for them instead); pruning old
tool results without summarising.

### Packets

Each lands with the specs that name its negative oracle: the spec that must fail when the change is removed.

| Packet | Content | Size | Negative oracle |
|---|---|---|---|
| (a) | `context_window` on the profile and `compaction.*` in the config schema (a new object), the model table, the doctor warning. | about 300 lines | A model with no window still prints the warning when the table entry is removed; a profile with a non-positive window fails the load, and a plain root session reads `compaction.context_window`. |
| (b) | The `context.compacted` event type, the compaction-aware rebuild in `replayInto` (`LoadSessionHistory` is left as it is), `doctor` lines, `session replay` and server pass-through. No trigger. | about 400 lines | A fixture log with an event whose `first_kept_seq` is mid-pair rebuilds without orphaned `tool.result`; removing the rebuild makes the history include pre-cut messages. |
| (c) | The summariser, the cut chooser, the manual `session compact`, usage accounting. | about 600 lines, split if over | A cut on a tool result is rejected; a `length`-finished summary appends nothing; a failed summary call leaves the log byte-identical. |
| (d) | The automatic trigger, the overflow classification in the adapters, the single retry, the `provider.error` doc comment (non-terminal use), and these rules: a failed automatic compaction is not retried within the run and the run continues until the provider fails; a `length` finish keeps its truncated `assistant.message` (it is already appended and counted) and the retry compacts then continues from it (the overflowing request already reserved a unit and counted as a turn in memory but wrote no `assistant.message`, and the retry reserves a second unit; the logged turn count is the lower one, accepted); the estimate includes the request-time system prompt and tool definitions, so `tokens_after` is comparable with a reported figure; the summary draws its unit before the turn's own reservation, so it can spend a pool's last unit and the next turn then ends `budget_exhausted` (accepted: the budget is a bound, not a promise). | about 500 lines | Two overflows in a row end the run as a provider failure, not a loop; a window-unknown session never calls the summariser. |
| (e) | Delegation and crash specs: a child compacts without touching the parent's log; a resumed child replays from its compaction; a crash before the append leaves the log unchanged; a crash after it resumes from the new line. | about 400 lines | A parent log with a compacting child contains no `context.compacted` of the child; a pre-append crash fixture still replays in full. |

## Alternatives rejected

- **Truncating or dropping the oldest messages.** No summary, so the model loses the task and decisions silently; Pi's
  documentation does not stop there, and the recalled harnesses are not expected to.
- **Rewriting the log in place.** Breaks the append-only log, ADR-006 recovery and the digest check; Pi keeps the
  original entries too.
- **A separate summaries table.** A second source of truth beside the log with its own recovery story. One event is
  atomic with the log.
- **Trigger on an estimate alone.** The provider already reports the figure; estimating everything repeats what #449
  made unnecessary. The estimate is only for what was appended since.
- **A default window.** A wrong default compacts constantly or never; an unknown window is better reported than hidden.
- **A configurable summariser model now.** More surface, a second budget question and a quality risk before the basic
  path is proven; Pi's documentation reaches it through extensions, which FlowState does not have.
- **Compacting inside the delegate tool for the child.** The child is an ordinary session; its loop already owns this.

## Cross-links

- ADR-002 (session run lifecycle): a compaction runs inside a run and adds no status; the lease it takes is the run's.
- ADR-006 (crash recovery): replay is the recovery mechanism; one append keeps the recovery rules unchanged, and a
  `context.compacted` event is not run activity for the crash predicate.
- ADR-009 (delegation): children are separate sessions with separate windows; D8's crash answer is why replay skips a
  `tool.result` whose call is before the cut.
- ADR-008 (Anthropic provider) and the provider port: window and overflow classification are adapter-level gaps this
  ADR names and does not close.
- Pi's documentation: `compaction.md` and `sessions.md` in the Pi coding-agent package.
