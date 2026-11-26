# FlowState

FlowState is a headless agent harness written in Go. It runs agent sessions from the command line or over a local
HTTP API, records every step as an ordered, hash-chained event in SQLite, and can replay, inspect and recover any
session from that log. Providers and tools sit behind small ports, so a session does not depend on which model or
tool set it uses.

This repository is the greenfield rewrite. It contains the implementation, its tests, schemas and the rules that
govern how it is changed. There is no TUI yet: the command line and the HTTP API are the interfaces.

## What works today

- **Sessions.** Create, run, pause on a budget, resume, cancel, close and list sessions. A session's whole history
  is an append-only event log that can be streamed, replayed and verified.
- **Providers.** `z.ai` (OpenAI-compatible and Anthropic-compatible routes, with tool calls) and `anthropic` (through
  your own signed-in `claude` binary, or the Anthropic API with a key). Both are resolved from the environment only.
- **Tools.** A bounded tool loop with an explicit allowlist: `echo`, `fs.read`, `fs.write`, `fs.edit` and, only when
  `FLOWSTATE_EXEC_ENABLED=1` and the binary is allowlisted, `exec.run`. Registering a tool grants no permission; the
  allowlist, workdir confinement and byte caps do.
- **Budgets.** A per-run budget, plus shared budget pools that reserve a share for each child session. A budget stop
  pauses the session instead of failing it.
- **Delegation.** Child sessions with a parent, a depth limit, and steering messages from a parent to its children
  (a message is accepted only from an ancestor).
- **Swarm manifests.** Validate a manifest, and run one. The receipts-only run creates a child session per packet in
  dependency order, reserves budget, admits packets, runs the gates and records the outcome. With `--execute`, a
  coordinator agent delegates the packets to role profiles and the engine runs the gates itself (see
  [Running a swarm](#running-a-swarm)). A cancelled run cancels its unfinished children.
- **Recovery and diagnosis.** A crashed run is detected from its log, and the next `session run` (or the server's
  run endpoint) recovers it automatically. `session doctor` reports
  whether a session crashed, its pending messages and budget, the last finish reason, whether an answer was
  truncated and the provider token usage the loop recorded (`usage`: input, output and cache tokens, `turns_reported`
  against `turns_total`, and the context size of the last reporting turn; the totals are `null` when no turn
  reported usage, as for the subscription route and for older logs; the totals are a lower bound on spend, a child's
  usage is not in its parent's, and the context figure can be stale), the `context_window` (tokens and source,
  `null` and `unknown` when none is known), `warnings` and `compactions` (the valid and the invalid count of
  `context.compacted` events); `session replay` re-verifies the log.
- **HTTP API.** `flowstate serve` exposes the same operations, with server-sent events for a live log.

**Not built yet:** the receipts-only `swarm run` does not execute provider turns for its children (it creates,
budgets and gates them; `swarm run --execute` is the path that runs them). Skills, hooks, MCP, provider failover
and a frontend are also outstanding. Nothing here is claimed to be production ready; see
[the architecture overview](docs/architecture/OVERVIEW.md) for the exact current behaviour.

## Quick start

You need the Go version recorded in `go.mod`. `make build` writes the binary to `build/flowstate`; the examples
below call it as `flowstate`, so put `build` on your `PATH` or substitute the path.

```sh
make build
export ZAI_API_KEY=...           # or see "Credentials" below; add ZAI_PLAN=general for a pay-per-token key
flowstate session new --provider z.ai --model glm-4.5-flash --task "Say hello in one sentence"
# prints {"id":"<id>","parent_id":"","status":"active"}; the provider is only contacted by `session run`
flowstate session run <id>     # prints the session's events as JSON lines
flowstate session events <id>  # the log again; add --follow to tail it
flowstate session replay <id>  # re-verifies the log's hash chain
flowstate session doctor <id>  # crash state, messages, budget, finish reason, truncation, usage
```

By default everything is stored in `./flowstate.db`; pass `--db <path>` to any subcommand to use another file.

Run with tools by allowing them explicitly, and say where they operate (a relative `--workdir`, such as `.`, is
resolved against the current directory; `~` is not expanded):

```sh
flowstate session run <id> --allow fs.read --allow fs.write --workdir ./work
```

To use your Claude subscription instead of an API key, set no key at all: the `anthropic` provider runs your own
signed-in `claude` binary and never reads or forwards a Claude credential.

```sh
flowstate session new --provider anthropic --model haiku --task "Say hello"
```

## Compaction

`session compact <id> [--instructions <text>]` summarises the old part of a long session with the session's own
provider and model (no tools, at most 8192 tokens) and appends one `context.compacted` event; the log is never
rewritten, and `session replay` still shows everything. The newest turns (about 20000 estimated tokens) stay verbatim.
It takes the session's exec lease under its own unique owner, `compact:<uuid>`, by the judged acquire, so a session
whose lease is held by a live run cannot be compacted (nothing is written). A crashed `run:` or `compact:` row of this
machine is taken over by the next `session run`, `session compact` or `tool run`. A legacy row, one written by an
older binary (`cli`, `delegate:` or `compact:`), records no machine and cannot be judged: every one of them refuses it,
naming the holder (exit 9, code `session_busy`), and it needs `flowstate session unlock <id> --force`; a leftover
`compact:` row is no longer cleared silently. A compaction whose row was taken from it fails fenced and writes nothing.
It is refused unless the session is `active` or `paused`; it recovers a crashed turn under its lease first; a paused
session stays paused. It never releases a lease it did not take. It has no loop, so no budget pool applies:
every flag that does not apply (`--budget`, `--max-tokens`, `--max-turns`, `--allow`, `--workdir`, `--provider`,
`--model` and the like) is refused with exit 2 naming it. `--instructions` is one line of at most 1000
bytes with no control characters (a tab counts), appended as a focus for the summary. It prints one JSON object:
`session`, `event_seq`, `first_kept_seq`, `tokens_before`, `tokens_after`, `reason`, `model` and `usage`. The
summariser's input is capped at 400 KiB: of a very long span only the newest part is summarised, though the events
before the cut still leave the rebuilt history.

Exit codes: 0 done; 2 usage, including `no_cut` (nothing new to compact, or the history is too short, so nothing is
written); 3 the provider failed, or the summary was empty, cut off at the token cap or invalid (code
`provider_error`); 4 the database or crash recovery failed (`config_invalid`); 6 a finished session; 7 a corrupt log;
130 cancelled by a signal (no compaction event was written); 9 the lease is held or cannot be judged (code
`session_busy`: wait, or see `session unlock`); 1 an unknown session, or a log that changed during the summary (code
`internal`: run again). A failed compaction writes no compaction event (crash
recovery may add its interruption marker first).

What exists today: a tool-loop session (`session run` with `--allow` or the other tool-loop flags) honours a
compaction on its next run, compacts itself automatically near the window (see Configuration), and `session doctor`
counts the compactions. The plain text-turn path never rebuilds history from the log, so a compaction does not
change what it sends; for the retry after a provider overflow see [Overflow retry](#compaction).

## Unlocking a lease

`session unlock <id> [--force]` deals with the exec lease row that a run leaves while it works on a session (ADR-011).
Use it when a run refuses a session because a row that cannot be judged is in the way: a row written by another
machine, or one from before the lease columns existed (a legacy row, with no machine and no lock). Without `--force`
it only reports: it prints one JSON object (`session`, `item`, `outcome`, `holder` with `owner`, `host`, `pid`, `token`
and `acquired_at`, `machine_matches`, `legacy_row`, `event_appended` and `message`), deletes nothing and exits 1 when a
row exists and its holder cannot be judged (outcome `reported`); with no row it exits 0 (outcome `no_lease`). A
live holder on this machine is refused even without `--force`: the report is still printed, but the exit is 9, code
`session_busy`. A legacy or otherwise unjudgeable row without `--force` stays exit 1, `reported`. The report
writes nothing to the lease row or the log, but it may create the lock directory beside the database.

With `--force` it deletes the row it read, only if the row is still the one it showed (a replaced row survives and the
exit is 1), appends one `session.unlocked` event to the log (none for a finished session), writes one line on stderr
naming the owner, host and pid, and prints the report (outcome `deleted`). It runs no recovery: the next `session run`
acquires the lease, recovers a crashed turn from the log and runs. A lease whose lock is held by a live process on this
machine is refused even with `--force` (the holder is printed, exit 9, code `session_busy`): stop that process instead.

**Danger.** A legacy row holds no lock, so `--force` cannot tell whether its run is still alive. If it is, deleting the
row leaves two writers on one log. Check that nothing is running the session before you force; the stderr line repeats
the warning for a legacy row. The owner and host come from the row, so both are escaped for a terminal in the JSON and
on stderr. Flags that do not apply (`--budget`, `--model` and the like) are refused with exit 2 naming the first.

## Commands

| Command | Purpose |
| --- | --- |
| `session new` | Create a session: `--provider`, `--model`, `--task <text\|@file>`, `--parent`, `--reserve` |
| `session run <id>` | One turn or tool loop: `--budget`, `--max-tokens`, `--max-turns`, `--allow`, `--allow-file`, `--workdir`, `--agents` |
| `session compact <id>` | Compact a tool-loop session's context by hand: `--instructions <text>`; see [Compaction](#compaction) |
| `session unlock <id>` | Report, or with `--force` delete, a lease row that cannot be judged; see [Unlocking a lease](#unlocking-a-lease) |
| `session list [--status <s>]` | List sessions |
| `session events <id> [--follow]` | Print or follow the event log |
| `session replay <id>` | Replay and verify the log |
| `session doctor <id>` | Read-only diagnosis of a session |
| `session budget <id>` | Show a session's budget and reservations |
| `session message <id>` | Send a steering message from a parent to a child |
| `session cancel <id>` / `session close <id>` | End a session |
| `session tool <id>` | Run one allowed tool directly (holds the session's run lease, so a live run refuses it) |
| `swarm validate <manifest>` | Validate a swarm manifest (YAML) |
| `swarm run <manifest>` | Run one: requires `--parent <id>` and `--budget <n>` |
| `swarm run <manifest> --execute` | Run one with a coordinator agent that delegates the packets; see [Running a swarm](#running-a-swarm) |
| `receipt validate <file...>` | Validate acceptance receipts against their schema |
| `store info` | Database facts; the file is not modified (WAL sidecars may appear): schema, journal mode, foreign keys |
| `agents list` | List agent profiles as a JSON array: `--agents user\|project\|both` (default `user`), `--workdir` |
| `agents validate <file...>` | Validate profile files: one `{path, ok, error?}` per file; exit 4 if any is invalid |
| `serve` | Start the HTTP API |

`replay <id>` writes its raw JSONL transcript to files and pipes, because each event's `payload_sha256` hashes the
payload bytes, and escapes control and format characters (as `\uXXXX`) only when writing to a terminal. All other
command-line JSON output is always terminal-safe. `session events` and `session run` embed each payload as JSON that
`encoding/json` re-compacts, so their output bytes already differ from the stored payload bytes; verify with `replay`,
not by re-hashing that output.

Common flags: `--db <path>` and `--config <path>`. Output is JSON; errors are a `{code, message}` object on
standard error. The machine-readable shapes are in [schemas/cli.json](schemas/cli.json).

### Agent profiles

An agent profile is a markdown file: YAML frontmatter, then the system prompt for a child agent
(ADR-009, the delegation design record). Profiles are loaded, validated and listed by `flowstate agents`, and the
`delegate` tool runs them as child sessions (see "Delegating to a child agent" below).

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

- `name` (`^[a-z][a-z0-9-]{0,63}$`) and `description` are required. `tools` is the allowlist; omitted means no tools.
  Names must be registered (`echo`, `fs.read`, `fs.write`, `fs.edit`, `exec.run`) or the reserved `delegate`, and may
  not repeat. `model` is a model name or `inherit` (the default). `max_turns` is 1 to 100 and defaults to 25.
  `context_window` is optional: a plain integer of 1 to 100000000 tokens, the model's context window for a session
  running under this profile (shown by `agents list`; 0 there means unset).
- `delegates` is a list of profile names (each `^[a-z][a-z0-9-]{0,63}$`, at most 32, no duplicate, never the profile
  itself): the only profiles this profile's `delegate` tool may spawn. It needs `delegate` in `tools`, otherwise the
  load fails with "delegates needs delegate in tools". `delegate` in `tools` without `delegates` loads, with
  `delegates: []` (visible in `agents list`): it fails closed, nothing is allowed. A delegating child may spawn
  only the names in its own `delegates` (see nested delegation below); the root run is not restricted by it.
  A name outside the loaded scope fails `agents list`, `session run` and the other loaders at load
  (`config_invalid`), naming both profiles; `agents validate` checks files one by one, so it does not cross-check.
  Cycles (a delegates b, b delegates a) are allowed: the depth cap bounds the recursion.
- An unknown field fails the load. So does an empty body: a profile with no instructions is a mistake.
- The frontmatter is strict. Refused: an explicit YAML tag; a non-string text value or a `max_turns` that is not a
  plain decimal integer; a second YAML document or content after `...`; a control, format or separator character
  (DEL, C1, ZWSP, RLO, U+2028 and the like) in `description` or `model` (a tab is fine); a block scalar (`>` or `|`),
  because `description` and `model` are one line; NUL, ESC, DEL or C1 characters in the body (a zero-width joiner is
  allowed there); a description over 500 characters; and, under `--agents both`, a name in both the user and project
  directories.
- A null or empty value counts as omitted (a null `tools` entry is refused). `model` is trimmed and at most 200
  characters.
- `agents validate` takes only file names and `--json`: any other argument starting with `-` is a usage error (exit 2).
  Write `./-name.md` for such a file.
- A profile file is at most 64 KiB, must start with `---` at byte 0, and must be a regular file: a symlink, a
  directory or a fifo is refused.
- User profiles live in `flowstate/agents/*.md` below the operating system's user config directory
  (`$XDG_CONFIG_HOME` or `~/.config` on Linux); this is the OS convention, not FlowState config discovery. Project
  profiles live in `<workdir>/.flowstate/agents/*.md` and load only with `--agents project` or `--agents both`,
  because they are prompts, with tools, controlled by the repository. With `both`, a name found in both directories
  is refused (`config_invalid`, exit 4, naming both files): a repository profile never replaces a user profile.
  Only `*.md` files directly in a directory count (no hidden files, no recursion); two files with one name in a
  directory fail the load.
- This repository's `.gitignore` ignores `.flowstate/`. A project that wants committed profiles unignores them
  (`.flowstate/*` then `!.flowstate/agents/`).

A coordinator that may delegate only to named profiles:

```markdown
---
name: team-lead
description: Splits a task and hands the parts to engineers and a reviewer
tools: [delegate, fs.read]
delegates: [engineer, reviewer]
---
You plan. You never edit. Delegate each part to an engineer, then to the reviewer.
```

As a child, this profile may start `engineer` and `reviewer` and nothing else; see nested delegation below.

### Turn limit

A tool loop runs at most 25 turns by default. `session run <id> --max-turns <n>` (1 to 1000; anything else is a
usage error, exit 2) changes that for the run. `--max-turns` is a tool-loop flag like `--allow`: without a tool
loop (`--allow`, `--allow-file`, `--agents` or a config `tool_allowlist`) it is a usage error. A loop still asking
for tools when it reaches the limit fails with `max_turns` (exit 5) and the session is `failed`, so raise the limit
before the run. The HTTP run body takes the same limit as `max_turns`.

### Output cap and truncated answers

A tool loop does not accept a turn the output cap cut off. When a provider turn finishes `length` with no complete
tool call, the engine retries it once with the cap doubled (never above the model's table maximum; an unknown model's
ceiling is 32768), after a non-terminal `provider.error` with `reason` `output_truncated` and `terminal` false. The
retry draws a second pool unit and counts as a second turn. If the retry is cut off too, or the cap is already at the
ceiling, the run fails with `output_truncated` (exit 5, HTTP 409, a terminal `provider.error`, session `failed`); a
delegated child or swarm coordinator that fails this way is failed, never completed. A `length` finish that carries
complete tool calls runs them as before. A child whose pool has no spare unit for the retry stops as a budget failure
instead. The single-turn path (no tool loop) has no loop to retry in: a `length` answer there is still exit 0, with
one warning line when the provider reports a finish reason (every resolved z.ai route does).

### Delegating to a child agent

`session run <id> --agents user|project|both` loads the profiles for that scope and gives the model one extra tool,
`delegate`. Without `--agents` there is no `delegate` tool. The tool stays deny-by-default: it must also be on the
allowlist (`--allow delegate`, `--allow-file` or the config `tool_allowlist`), and the two must agree. `--agents`
without `delegate` allowlisted, `delegate` allowlisted without `--agents`, and a scope that is not `user`, `project`
or `both` are usage errors (exit 2). A name in both directories under `both` is `config_invalid` (exit 4), before
any turn runs.

A worked example with a reader and a reviewer (the reviewer cannot write):

```sh
mkdir -p ~/.config/flowstate/agents
cat > ~/.config/flowstate/agents/reader.md <<'EOF'
---
name: reader
description: Reads files and summarises them
tools: [fs.read]
---
Summarise what you read.
EOF
cat > ~/.config/flowstate/agents/reviewer.md <<'EOF'
---
name: reviewer
description: Reviews a change; never edits
tools: [fs.read]
max_turns: 10
---
You review. Begin with VERDICT: pass or VERDICT: fail.
EOF
flowstate agents list --agents user
id=$(flowstate session new --provider z.ai --model glm-5.3 \
  --task "Have the reader summarise main.go, then the reviewer judge it" | jq -r .id)
flowstate session run "$id" --budget 200 --agents user --allow delegate --workdir ./work
```

- The tool takes `agent` (a loaded profile name) and `task`, and optionally `cwd`, a directory inside the parent's
  workdir; anything outside it is refused. Its description lists the loaded profiles, so the model can choose. With
  `agent` and `task` one call runs one child.
- `resume` continues an interrupted child: `{"resume": "<child id>", "task": "<follow-up, optional>"}`, alone (not
  with `agent`, `cwd` or `tasks`). The child must belong to this parent, be active or paused (a crashed or paused run;
  a child that reaches its turn limit is failed, which is final) and not be running; a finished child is refused
  ("start a new one"), as are another session's id, a profile that is gone or whose file changed since the child
  started, and a child with no turns left. It continues from its own log with the turns it has left (`max_turns` minus
  the turns already used), which the budget charges, and a `task` becomes its next user message. The receipt says
  `"resumed": true`.
- If the parent itself crashes during a `delegate` call, the call is not run again. When the parent is resumed
  (`session run <id>`), the log gets an error result for each such call, before the run's first request, naming every
  child it started with its id and status: an unfinished one can be resumed (`resume it with delegate {resume:
  "<id>"} or start a new one`), a finished one lost its result, which is not delivered, and the model starts a new one
  only if it still needs the work. The text says no child had started only when none had. Paths and profile names are
  never repeated to the model. The leases that unfinished children's killed runners left are cleared, so they can be
  resumed (a live resumed child can be fenced: known limit L1, ADR-011). Each child's start is recorded as a
  `delegation.started` event in the parent's log. Under `session run`, serve and `delegate resume` a judged lease
  (ADR-011) keeps other runs out. A call answered late (a log an earlier build left with the call unanswered) gets its
  result after later events, which a provider that wants the result next to the call may reject.
- `tasks` runs several children in one call: an array of `{agent, task, cwd?}`, at most 8, exclusive with `agent`,
  `task` and `cwd`. At most 4 run at once; the rest start as slots free (calls in one turn run one after another, so the
  cap of 4 applies per call). Every entry is validated before any child starts: a ninth task, an unknown agent, a blank
  or oversized task or an empty array refuses the whole call, naming the entry's index, and creates no child. For
  example, a reader and a reviewer in parallel:

  ```json
  {"tasks": [{"agent": "reader", "task": "Summarise main.go"},
             {"agent": "reviewer", "task": "Review main.go", "cwd": "pkg"}]}
  ```

  The result is one JSON object, `{"results": [{index, agent, child, status, turns, text?, reason?}, ...]}`, in
  input order whatever the completion order. A failed, cancelled or refused child (a budget, a `cwd` outside the
  parent's) is an entry with its `status` and `reason`, and its partial `text`, not an error: its siblings keep
  running and the model sees every result. Each entry's text has its own 50 KB / 2000 line cap. Cancelling the parent
  cancels the running children and the entries not yet started, which create no session. A `delegation.completed`
  receipt per child lands in the parent's log as it finishes, carrying its `index`.
- A completed child returns `{child, status, turns, text}`, the text capped at 50 KB and 2000 lines with a notice.
  A failed, cancelled or partial child comes back as an error result (`error: ...` with the child id, status, turns,
  reason and any partial text): the parent model decides what to do and the parent run does not fail. Cancelling the
  parent cancels the child with the same reason.
- The child gets exactly its profile's tools, prompt and `max_turns`; it can delegate only if its profile lists
  `delegate` (see the nested delegation item below). With `--budget`, its grant is drawn from the parent's pool and the
  unspent part returns. A `delegation.completed` receipt lands in the parent's log between the call and its result.
- Nested delegation: a coordinator can delegate to a team lead that delegates to specialists. A child whose profile
  lists `delegate` gets its own `delegate` tool, bound to it, and may start grandchildren; a grandchild may not
  delegate. The depth is at most 2 (root, child, grandchild): a profile that lists `delegate` for a grandchild is
  refused when it would start (`delegate is not available at depth 2: the nesting limit is 2`) and no session is
  created. A child without `delegate` in its profile cannot delegate. A delegating child may start only the profiles its
  own `delegates` names, for a single `agent`, every `tasks` entry (one other name refuses the whole call, no child
  starts) and `resume`; its tool description lists only those. A profile that lists `delegate` with no `delegates`
  may start nothing. The root run keeps every loaded profile. Each level works inside its parent's directory.
  The child's grant bounds its whole subtree: a grandchild's grant is carved out of the child's, and the unspent part
  returns up the chain. Cancelling the root cancels every level with the root's reason. A grandchild's receipts are
  in the child's log and the child's in the root's. At most 8 descendant runs of one root run execute at once; a
  call past that waits for a slot. A run that waits for its own children does not hold a slot.

  ```sh
  cat > ~/.config/flowstate/agents/team-lead.md <<'EOF'
  ---
  name: team-lead
  description: Splits a task and delegates the parts to specialists
  tools: [delegate, fs.read]
  delegates: [reader, reviewer]
  max_turns: 20
  ---
  Delegate each part to a specialist, then summarise their results.
  EOF
  ```

  With the `reader` and `reviewer` profiles above as specialists, the coordinator (the root run, with
  `--agents user --allow delegate`) delegates `{"agent": "team-lead", "task": "Review main.go"}`, and the team lead
  delegates to `reader` and `reviewer`, the only profiles its `delegates` allows.
  The root's result holds only the team lead's final text.
- Project profiles come from `<workdir>/.flowstate/agents` when `--workdir` is given, otherwise from
  `.flowstate/agents` in the process's current directory, never from the default workdir beside the database. With
  `--agents` and no `--workdir`, that default directory is created (mode 0700) before the run.
- Use project profiles only for repositories you trust: a model with `fs.write` on the same workdir could write a
  profile there in an earlier run, and a child's tools are not limited by the parent's allowlist.
  A project profile's `delegates` is also repository-controlled:
  under `--agents both` it can name user profiles, which then load as its delegates.
- A task is at most 64 KiB, and the tool description lists at most 32 profiles, then `(and N more)`.
- The parent needs a provider that accepts tools: the z.ai coding route or the Anthropic API-key route. With one that
  refuses tools the run fails as it does today; nothing checks this in advance.
- `--agents` is a command-line option only. The HTTP run does not accept it yet; that is a later change.

### Running a swarm

`swarm run <manifest> --execute` runs a swarm for real. The manifest names a `coordinator:` profile and gives each
packet a `role:` profile; the coordinator is told the packets and delegates each one to its role with the `delegate`
tool, and every role is a profile file, so no role is built into the engine. A worked example, a reader and a
reviewer with a gate that checks the file the reviewer must write, is in
[docs/examples/swarm](docs/examples/swarm/review-swarm.yaml):

```sh
cp -r docs/examples/swarm ./swarm-work && cd swarm-work
cp ../README.md .            # something for the reader to read
flowstate agents list --agents project --workdir .
flowstate swarm run review-swarm.yaml --execute --agents project --workdir . --db ../flowstate.db \
  --provider z.ai --model glm-5.3 --budget 20000 --max-turns 12
```

Keep `--db` outside `--workdir`: the reviewer can write there, and a database inside it could be overwritten. The run
is refused (exit 2) if the database or its `-wal` or `-shm` file is inside the workdir, the default `flowstate.db`
included.

- Flags: `--agents user|project|both` (default `user`) picks where the profiles load from, `--workdir` (default the
  current directory) is where the coordinator's tools, the children and the gates all work, `--provider` and `--model`
  come from the flags or the config defaults as for `session new`, `--budget` is the coordinator's turn pool (0 means
  none), `--max-turns` bounds the coordinator's loop (1 to 1000) and `--max-tokens` caps each response. No `--parent`
  is needed: the engine creates the coordinator session. Without `--execute` these flags are usage errors (exit 2): the
  receipts-only `swarm run` refuses them rather than ignoring them, and is otherwise unchanged. `--workdir` must be an
  existing directory (a symlink to one is fine).
- Needs a provider that accepts tool calls (the z.ai coding route or the Anthropic API-key route). The manifest needs a
  coordinator, at least one packet with a role and at least one gate, and no packet may use the coordinator as its role.
  The coordinator may delegate only to the manifest's roles; if its profile lists `delegates`, only to the roles that
  list also names (a packet role it leaves out is a usage error), and an empty list adds no limit.
- The coordinator is given the swarm's brief: the packets in dependency order with their roles and tasks, and the gate
  names and descriptions (never their commands). It must start every delegated task with `[packet <id>]` followed by
  the packet's task text, which is how the engine attributes a child to a packet.
- **The engine decides the outcome, not the model.** After the coordinator completes, the engine admits a packet only if
  a child attributed to it completed, then runs the gates itself in the workdir. What the coordinator claims about the
  gates is ignored. A failed or cancelled coordinator runs no gate.
- Output is one JSON object (`swarm-run-execute` in [schemas/cli.json](schemas/cli.json)): `run_id`, `status`
  (`accepted`, `not_accepted`, `gate_failed`, `failed` or `cancelled`), `reason` (why a run failed or was cancelled,
  such as `max_turns`, `output_truncated`, `budget_exhausted` or `sigint`), `coordinator_session`, `children` (`id`,
  `profile`, `packet`, `status`, `turns`), `gates` (`name`, `passed`, `interrupted`) and `packets` (the admission
  decision, with a `reason` for a refusal and the admitting child as `evidence`).
- Exit codes: 0 accepted; 8 `not_accepted` or `gate_failed` (as for the receipts-only run); a failed coordinator
  keeps its own class (3 provider error, 5 budget, `max_turns`, `output_truncated` or `hook_stop` (a coordinator
  stopped by a `turn.after` hook), 1 anything else, including a passed deadline); 130 cancelled by SIGINT, SIGTERM
  or SIGHUP, with the children settled so no session stays
  active; 2 usage errors, which include a manifest with no coordinator or a role with no profile, and 4
  `config_invalid`, such as a profile in both directories under `--agents both`. A refused run creates no session or
  run.
- Gates run as plain argv (no shell) in the workdir. A gate that executes a file the swarm's write-capable roles can
  write (`./check.sh`) runs what they wrote, and a stale `REVIEW.md` from an earlier run satisfies `test -s REVIEW.md`:
  use a fresh workdir per run.
- To see what happened, run `session events <coordinator_session>` for the coordinator's log (its `delegation.started`
  and `delegation.completed` receipts name each child) and `session events <child id>` for a child's own turns.

### Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Completed (a single-turn `length` answer is still 0; one warning line when a finish reason is reported) |
| 1 | Unknown session, or any other failure without its own class (code `internal`); a lease lost mid-run (fenced) is `internal`, exit 1 |
| 2 | Usage error, or `hook_refused` (a `user.prompt` hook refused the run before any provider request) |
| 3 | Provider error |
| 4 | `config_invalid`: bad configuration, an unopenable database, or a failed crash recovery or steering drain |
| 5 | Budget stop (the session is paused), `max_turns` (the turn limit reached while still requesting tools; failed), `output_truncated` (a tool-loop turn cut off at the output cap twice; failed), or `hook_stop` (a `turn.after` hook stopped the run; root runs are paused) |
| 6 | The session is already terminal |
| 7 | Replay found a corrupt log |
| 8 | A swarm run, receipts-only or `--execute`, was not accepted (failed gate or rejected packet), or a receipt is invalid |
| 9 | `session_busy`: the session's lease is held by a live run, or by a holder that cannot be judged; nothing was written |
| 130 | Cancelled by SIGINT, SIGTERM or SIGHUP (SIGHUP is left alone when ignored, as under `nohup`) |

## Configuration

**Provider settings come from the environment only.** The command line reads no provider file and never stores a
secret.

| Provider | Variable | Meaning |
| --- | --- | --- |
| `z.ai` | `ZAI_API_KEY` | API key (required) |
| `z.ai` | `ZAI_PLAN` | `coding` (the default) or `general`, which pay-per-token keys need |
| `z.ai` | `ZAI_BASE_URL`, `ZAI_CODING_ROUTE` | Endpoint and coding-plan wire format (`openai` or `anthropic`) |
| `anthropic` | `FLOWSTATE_ANTHROPIC_AUTH` | `oauth` (default: your `claude` binary) or `api_key` |
| `anthropic` | `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL` | Key and endpoint for the `api_key` route only |
| `anthropic` | `FLOWSTATE_CLAUDE_BIN` | Which `claude` binary to run |

`ANTHROPIC_API_KEY` alone never selects the API route: many tools export that key, and you may still want the
subscription. Only `FLOWSTATE_ANTHROPIC_AUTH=api_key` does.

**`exec.run` environment.** The child gets an allowlisted pass-through of the engine's own environment, chosen per
binary. Every binary gets `PATH`, `LANG`, `LC_ALL`, `LC_CTYPE`, `TERM`, `TZ`, `TMPDIR`, `TMP`, `TEMP` and the Windows
`SYSTEMROOT`, `PATHEXT`, `COMSPEC` and `WINDIR`. Only `go` and `make` also get `HOME`, `USER`, `LOGNAME`, `GOPATH`,
`GOCACHE`, `GOMODCACHE`, `GOTOOLCHAIN`, `GOROOT`, `GOENV`, `GOPROXY`, `GOSUMDB`, `GOPRIVATE`, `GONOSUMDB`,
`GONOPROXY`, `GONOSUMCHECK`, `GOINSECURE`, `GOVCS`, `GOOS`, `GOARCH`, `CGO_ENABLED`, `CC`, `CXX`, `XDG_CACHE_HOME` and the
Windows `USERPROFILE`, `LOCALAPPDATA` and `APPDATA`; `git` and `gh` never see them. `go`
and `make` also always get `GIT_CONFIG_GLOBAL` and `NETRC` set to the null device, `GIT_CONFIG_NOSYSTEM=1` and
`GIT_TERMINAL_PROMPT=0`, so `go` and the `git` it spawns read no user git config, credential helper, `insteadOf` rule
or `~/.netrc`; fetching a private module then needs your own `GOPRIVATE` and transport. A name unset in the engine is
not set in the child. Any name matching `key`, `token`, `secret`, `password`, `credential` or `auth`
(case-insensitively) is never forwarded, and neither is `FLOWSTATE_*`, `HTTP(S)_PROXY`, `NO_PROXY`, `GOAUTH` or anything
else unlisted. A `GOPROXY` or `GOSUMDB` entry with userinfo (`user:pass@host`, with or without a scheme) or a query
token is dropped: the WHOLE variable is dropped, and a dropped `GOPROXY` becomes `GOPROXY=off` (go fails closed rather
than using the public proxy); a dropped `GOSUMDB` stays unset, so default checksum verification stays on. The
neutraliser claims were verified with git 2.56 and libcurl 8.22 only: `GIT_CONFIG_GLOBAL` needs git 2.32 or later, and
`NETRC` needs a libcurl that honours it. `GOFLAGS`, `XDG_CONFIG_HOME` and `XDG_DATA_HOME` are never forwarded (`GOFLAGS`
can run a program through `-toolexec` and no value filter is reliable; an operator who needs `-mod=vendor` uses the
`go.mod`/vendor layout).

Residual risks: the split only stops `HOME`, XDG, netrc and gitconfig-file credential discovery for `gh` and `git`, and
stops `go` reading your git and netrc credentials. It does not stop OS-keyring tokens (`gh auth token` still returns one
through the session bus), system-level credential helpers or ssh keys read through the passwd home. The exec allowlist
(the subcommand gate: `gh pr`/`gh run`, the listed `git` subcommands) is the real control for `gh` and `git`. It is not
a confidentiality boundary against an allowlisted binary that runs code (`git fetch --upload-pack`, `go test
-exec`/`-toolexec`/`-vettool`, `make`), and same-UID code can read `/proc/<engine>/environ` and the passwd home
directory. That was true before the split. `go env -w` is not allowlisted. A `make` recipe can use `gh` and `git` with
your credentials: it gets `HOME`, so `~/.config/gh` and `~/.gitconfig` stay reachable (this is the accepted `make`
escape; the XDG directories are not forwarded). Because `XDG_CONFIG_HOME` is not forwarded, `go` does not read your go
env file through XDG, but it does read `$HOME/.config/go/env` through the forwarded `HOME`, so a `GOPROXY`, `GOAUTH` or
`GOFLAGS` set there bypasses the value filter (only you can write that file).

**Session defaults** come from an optional `flowstate.json` (the working directory's file, or `--config <path>`):

```json
{
  "default_provider": "z.ai",
  "default_model": "glm-4.5-flash",
  "default_budget": 20000,
  "storage_path": "./flowstate.db",
  "tool_allowlist": ["fs.read"],
  "compaction": { "enabled": true, "reserve": 16384, "keep_recent": 20000, "context_window": 200000 }
}
```

`compaction` is optional, and so is each of its keys (the values above, except `context_window`, are the defaults):
`enabled` is a boolean, `reserve` and `keep_recent` are counts of tokens of at least 1, and `context_window` is
1 to 100000000. Config only: there is no flag or environment override, an unknown key inside it is rejected, keys
match case-insensitively and a repeated key takes its last value.

**Automatic compaction.** In the tool loop (`session run` with a tool surface, and the `delegate` children it starts)
the engine measures the context before each provider request: the last reported turn context plus an estimate (bytes /
4) of the tool results appended since, or, when no turn has reported usage or the session was just resumed, an
estimate of the whole rebuilt history with the system prompt and tool definitions. Above `window - max(reserve,
output)` (`output` is the run's `--max-tokens` when set, else the default described below) it summarises the older
turns with the session's own model, appends one `context.compacted` event (reason `auto`) and continues from the
rebuilt history, keeping about `keep_recent` tokens verbatim. `enabled`, `reserve`, `keep_recent` and `context_window`
take effect there. The summary draws one unit from the budget pool before the turn's own reservation, so it can spend
a pool's last unit; a compaction is not a turn and does not count against `max_turns`. A compaction that fails (no
eligible cut, an empty or truncated summary, a failed summary call, a pool that cannot cover it, or a `tokens_after`
still above the line) writes nothing, is not retried in that run (so a `no_cut` on the first over-the-line turn turns
compaction off for the rest of the run), and the run continues; a `no_cut` gives its pool unit back. A window not
larger than the reserve (or `--max-tokens`) never compacts: raise the window or lower the reserve or `max_tokens`.
Without `--max-tokens` a run sends the model's maximum output from the registry table, capped at 32768 (an unknown
model: 8192), and a known model's line is `window - max(reserve, that value)`, the value limited to a quarter of the
window (an unknown model's line is the reserve alone). An explicit `--max-tokens` is not limited. A stream cut by the
output cap drops the partial tool call instead of failing the run, and the tool loop then retries or fails the turn as
described under Output cap and truncated answers. A delegate or swarm child sends its own model's default, not the
parent's explicit cap (passing it down is a follow-up). With `compaction.enabled` false the larger default cap shrinks
the input headroom, and the reactive overflow retry below is the only net. In `swarm run --execute` the coordinator
and its children, nested ones included, compact by the same `compaction` settings, including `enabled: false`; the
coordinator's window is its profile's `context_window` when set, and each compaction draws from the swarm pool.
`session compact` summarises by hand.

**Overflow retry.** The tool loop also has a reactive net: when the provider says a request does not fit the window
(`provider.ErrContextOverflow`), or answers with a `length` finish and no tool calls on a request whose context was
already over the compaction line, the engine compacts (reason `overflow`) and retries that request once; it needs a
known window and `compaction.enabled`, as the automatic trigger does. The first overflow writes a `provider.error`
event with `terminal` false, which leaves the session `active`: `reason` `context_overflow` for an overflow error,
`output_truncated` for a `length` finish (written before compacting; the rebuild drops the truncated message); the
retry draws a second pool unit and counts as a second turn in the run result, but the log counts only the answered
requests, so a retry costs one `max_turns` turn and an overflow can fail a run that had a turn left. A second overflow
after the retry, an overflow compaction that fails, a pool too short for the compaction, or an unknown window ends the
run as an ordinary provider failure (exit 3, a terminal `provider.error`, `failed`, not `budget_exhausted`). After a
`length` finish whose compaction fails, the run fails with `output_truncated`. A turn has one recovery: after the
compaction retry, a further `length` finish fails the run too; a `length` finish on a request under the line escalates
the cap instead (see Output cap and truncated answers). The adapters classify overflow only on a pinned signature,
never a substring: HTTP 413 on any HTTP route; the Anthropic Messages API (the API-key route and the z.ai
Anthropic-compatible route) answering 400 `invalid_request_error` with a message starting `prompt is too long`; and
z.ai answering 400 with the string code `1261`. The 400 signatures are recalled, unverified against the live APIs: a
miss only means the net does not fire. A numeric code `1261` is not classified (z.ai bodies use a string code), and
neither is a streamed SSE `error` event: a mid-stream overflow error is an ordinary provider failure (Anthropic
reports a too-long prompt as HTTP 400 before streaming starts). The subscription route (the `claude` binary) does not
classify overflow, as it reports only result text. A window comes from the profile's `context_window` for a delegated
child, else from `compaction.context_window`, else from a built-in table of `anthropic` and `z.ai` models matched by
exact, case-sensitive id (it follows Pi's registry); there is no default, and an unknown window never compacts
automatically. `session doctor` resolves the window from `compaction.context_window` and the table only: it does not
read a profile's `context_window`, because a root session has no profile and the doctor does not know the profile of a
child. It warns for a model outside the table, for example the subscription aliases `haiku`, `sonnet` and `opus`, z.ai
`glm-4.5-flash` and every free or local model (a local model's window is whatever you loaded it with); set
`compaction.context_window` to silence it. `GET /sessions/{id}/doctor` reports the same `compactions`,
`context_window` and `warnings` for the same log, resolved from the server's loaded config (like the command, it
ignores a profile's `context_window`).

Explicit flags override the file. `--allow` and `--allow-file` replace `tool_allowlist`; `--workdir` only says where the
tools operate. A file holding anything that looks like a secret is rejected.

### Credentials

Keep keys in a secret manager and inject them for one command, so they never sit in a file or your shell history.
With 1Password, for example, put a reference (not the key) in a file and use `op run`:

```sh
# zai.env
ZAI_API_KEY=op://<vault>/<item>/credential

op run --env-file zai.env -- flowstate session run <id>
```

## HTTP API

`flowstate serve` listens on `127.0.0.1:7600` (`--addr` to change it). It refuses to bind a non-loopback address
unless `FLOWSTATE_SERVE_TOKEN` is set, which then becomes a required bearer token. Request bodies are capped at 1 MiB
and decoded strictly. Errors use the same `{code, message}` body as the command line, with the status for each code.
A 409 has several causes; the body `code` tells them apart (`session_terminal`, `session_cancelled`, `budget_exceeded`,
`max_turns`, `output_truncated`, `hook_stop`, `session_busy`).

| Endpoint | Purpose |
| --- | --- |
| `GET /health` | Liveness |
| `POST /sessions`, `GET /sessions[?status=]` | Create and list |
| `GET /sessions/{id}` | One session with its events |
| `GET /sessions/{id}/events?after=N` | Events past a cursor |
| `GET /sessions/{id}/events/stream?after=N` | Follow the log as server-sent events |
| `POST /sessions/{id}/run` | Run one synchronous turn |
| `POST /sessions/{id}/cancel`, `.../close` | End a session |
| `GET /sessions/{id}/doctor` | Diagnosis (as `session doctor`) |
| `GET`/`POST /sessions/{id}/budget`, `POST .../message` | Shared budget and steering |
| `POST /swarm/validate`, `POST /swarm/run`, `POST /receipt/validate` | Manifests and receipts |

## How it fits together

Dependencies point one way: the command and the HTTP adapter call application use cases, which compose ports for
storage, providers and tools, so the rest of the code does not depend on a particular model or provider SDK.

| Package | Responsibility |
| --- | --- |
| `cmd/flowstate` | Parse input, render output and exit codes, wire the listener |
| `internal/app` | Application use cases: the turn and tool loops, budgets, recovery, swarm runs |
| `internal/session` | Session lifecycle, identity and ordering rules |
| `internal/store` | SQLite persistence through an ORM: sessions, ordered events, budgets, leases, swarm runs |
| `internal/provider` | The provider port and the `z.ai` and `anthropic` adapters |
| `internal/tool` | The tool port, the built-in tools and the permission and loop policy |
| `internal/swarm` | Manifests, packet admission, gates, receipts |
| `internal/metadata` | Writes acceptance receipts into the external metadata root |
| `internal/oauth` | Token handling for provider authentication |
| `internal/server` | The HTTP transport (it may not import the application layer) |

The exact import rules are enforced by a checker: see [the source ownership contract](rules/ARCHITECTURE.md). Design
decisions are recorded as numbered notes in [docs/adr](docs/adr/README.md).

## Developing

Work happens in feature worktrees branched from `next`, one scoped pull request at a time; `main` is release-only.
Read [AGENTS.md](AGENTS.md) first: it is the bootstrap for people and agents alike, and the rules it links are
binding (behaviour-first tests, the architecture contract, commit and branch naming, pull request size budgets).
Project packets, review evidence and receipts live outside the checkout, in the project's external metadata.

You need the Go version in `go.mod` and Node.js 22 or newer for the documentation checks. `make preflight` installs
the pinned golangci-lint and gosec under `bin/tools` the first time it runs.

```sh
npm ci                          # documentation checks
make build
make test                       # unit and Ginkgo suites
make check                      # formatting, lint and tests
make preflight BASE_SHA=<base>  # the blocking CI checks, on the committed HEAD
make help                       # the main targets
```

A pull request is judged by the checkers as they are on its base commit, so it cannot weaken its own review. Tests
follow [the Ginkgo contract](rules/BDD_WORKFLOW.md), documentation follows [the standards](rules/DOCUMENTATION.md),
and what CI enforces is described in [rules/CI_QUALITY.md](rules/CI_QUALITY.md). Merging goes through an independent
review and a merge-admission check (see [rules/MERGE_ADMISSION.md](rules/MERGE_ADMISSION.md)).

### Security regression gate

CI scans immutable snapshots of the candidate and of its base with a pinned gosec. New findings at any severity fail
the build; inherited ones are reported as debt. Source `#nosec` annotations are ignored, so they cannot hide an
alert. The gate is not a security clearance. To run it locally, from the repository root, give it the full 40-character
SHA of an ancestor commit, an output path outside the worktree and the pinned scanner that `make preflight` installs:

```sh
python3 -B scripts/security_gate.py --base "$BASE_SHA" --scanner bin/tools/gosec-<version> \
  --output "$(mktemp -d)/security-results"
```

## More documentation

- [Architecture overview](docs/architecture/OVERVIEW.md): the current behaviour of every surface
- [Architecture decision records](docs/adr/README.md)
- [Pull request budgets](docs/development/PR-BUDGETS.md) and [review handoff](docs/development/REVIEW-HANDOFF.md)
- [Rules](rules/ARCHITECTURE.md): source ownership, tests, documentation, CI and merge admission
