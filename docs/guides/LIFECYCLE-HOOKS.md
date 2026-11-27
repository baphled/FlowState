# Lifecycle hooks (operator guide)

A lifecycle hook is a command the engine runs at a fixed point of a session run, with one JSON document on
stdin and a verdict told by its exit status and stdout. A hook can deny a call, rewrite a call's arguments or
add a stop; it cannot widen what the engine permits: the tool allowlist is checked before any hook, and every
tool applies its own confinement after one. The design of record is
[ADR-012](../adr/ADR-012-lifecycle-hooks.md); this guide describes what the current build does. A complete,
tested set of files are in [the worked example](../examples/hooks/README.md).

A hook is operator-configured code execution. It runs with your permissions, outside every sandbox, and can use
the network. Read [Safety and release notes](#safety-and-release-notes) before enabling anything beyond your own
user file.

## Where hooks are configured

| File | Location | Loads under `--hooks` |
| --- | --- | --- |
| User file | `flowstate/hooks.json` in the OS user config directory: `~/.config/flowstate/hooks.json` on Linux, `~/Library/Application Support/flowstate/hooks.json` on macOS, `%AppData%\flowstate\hooks.json` on Windows | `user`, `both` |
| Project file | `.flowstate/hooks.json` under the project root: the directory the command was started in, never the per-run `--workdir`; for `serve`, the server's root | `project`, `both` |

`--hooks user|project|both|none` is accepted by `session run`, `serve` and `swarm run --execute` and defaults to
`user`: your own file always runs unless you say otherwise, and `none` gives a reproducible run. Hooks are never
configured in `flowstate.json`, which a repository can ship and which loads with no opt-in. Under `both`, an id
declared in both files is a load error naming both, so a repository can neither replace a user hook nor disable
one by reusing its id.

A file that fails any check below refuses the run with a configuration error (`config_invalid`, exit 4) before
any provider request. On `serve` the HTTP run answers 500 `internal` with that message.

## What a hook file looks like

```json
{
  "version": 1,
  "hooks": [
    {"id": "guard-migrations", "point": "tool.before", "match": ["fs.write", "fs.edit"],
     "kind": "command", "argv": ["./scripts/guard-migrations"], "env": ["LANG"], "timeout_ms": 5000,
     "on_failure": "deny"},
    {"id": "pin-reads", "point": "tool.before", "match": ["fs.read"], "kind": "command",
     "argv": ["./scripts/pin-reads"], "transform": true}
  ]
}
```

| Field | Meaning |
| --- | --- |
| `version` | must be `1` |
| `id` | required, unique in the loaded set, `[a-z0-9][a-z0-9._-]{0,63}`; the log records it and the model's denial text names it |
| `point` | one of the [points below](#the-points-this-build-fires); any other value fails the load (`hook_point_unsupported` for a point this build cannot fire) |
| `match` | tool names or `*` globs (`fs.*`), only on `tool.before` and `tool.after`; omitted means every tool |
| `kind` | `command`; `decision` (System One) is recognised and refused until its packet lands |
| `argv` | the executable and its arguments, as a JSON array; never a shell command string |
| `env` | environment **names** whose values are copied from the engine's environment; a value is never written here |
| `timeout_ms` | 1 to 60000; the default is 10000 |
| `on_failure` | `deny` or `allow`; [defaults below](#timeouts-and-on_failure) |
| `transform` | `true` on a `tool.before` hook that may return `args`; refused elsewhere |

The file is strict. An unknown field, at any depth, fails the load. Before the decode every object **key** is
tested against `key`, `token`, `secret` and `password`, case-insensitively and as a substring (`monkey` fails
too); values are never inspected, so keeping a credential out of `argv` is your job. The file may be at most
256 KiB.

`argv[0]` is resolved in one of three ways. A relative path with a separator (`./scripts/x`) is resolved against
the directory of the hook file, so in `.flowstate/hooks.json` it is `.flowstate/scripts/x`, never the
repository root, and it must resolve inside that directory. An absolute path, or a bare name looked up on the
scrubbed `PATH`, is your choice and has no such clause. Want a shell? Write `["sh", "-c", "..."]` yourself.

## The points this build fires

| Point | Fires | A hook may | `on_failure` default |
| --- | --- | --- | --- |
| `user.prompt` | once per root run, after the lease and crash recovery, before the resume writes and before any `user.message` | deny: the run is refused (`hook_refused`, exit 2, HTTP 422; no status change, no provider request) | `deny` |
| `session.start` | once per root run, before the first provider request | allow; a deny is recorded and ignored | `allow` |
| `tool.before` | per tool call, after the allowlist gate, before `tool.call` is written | deny (the call does not run), rewrite `args` as a `transform` hook, allow | `deny` |
| `tool.after` | per executed call, after the tool returned, before the result is sent | observe; it cannot change the result | `allow` |
| `turn.after` | after every provider turn and its tool results, the final no-call turn included, before the turn-limit check | stop the run (`hook_stop`, exit 5, HTTP 409: a root run is paused and resumable, a delegated child fails), allow | `allow` |
| `session.end` | once per run that fired `session.start`, after the run's terminal or paused state is written | observe | `allow` |

`compaction.before` and `compaction.after` are named by the ADR and are refused by this build
(`hook_point_unsupported`). `on_failure: "deny"` is refused at the points with nothing to deny.

What a denial looks like: the model receives one fixed text, `error: denied by hook <id>`, and the call is
recorded as a normal `tool.call` and error `tool.result` (`hook_denied`), so it survives replay. The reason stays
in `hook.result` for you and never reaches a model. A refused `user.prompt` writes `hooks.loaded`,
`hook.invoked` and `hook.result` and nothing else (besides any crash-recovery marker); a session that a hook
refuses every time is left with `flowstate session cancel <id>`.

## Which runs fire which points

A root `session run` or `serve` run fires all six points on the tool-loop path and the three run-level points on the
text-only path. On the text-only turn (a run with no tool surface) there is no tool call, turn loop or compaction,
so `tool.before`, `tool.after` and `turn.after` hooks are listed under `inert` in `hooks.loaded` and never run;
`user.prompt`, `session.start` and `session.end` still do. A resumed run fires `user.prompt` again with the stored
task. A delegated child and a swarm coordinator fire only `tool.before`, `tool.after` and `turn.after`, with the
root's loaded user and project hooks inherited (a child cannot turn them off), and never the run-level points. Their
stdin carries the profile name in `agent` and the nesting level in `depth`; a root run has `""` and `0`.

## Ordering

Hooks of one point run one after another, never in parallel: the user file in file order, then the project
file. `tool.before` has two phases: every `transform` hook first, each seeing the arguments as the previous one
left them, then every other hook, all seeing the **final** arguments, so a guard cannot approve one thing and
have another run. The first deny ends the chain; later hooks do not run. A rewrite cannot change the tool name
and must be a JSON object of at most 64 KiB; the tool decodes it with the same decoder it uses for the model's
own arguments, and `hook.result` records both hashes and the rewritten arguments.

## What a hook reads on stdin

One JSON document, closed, at most 256 KiB (larger is the failure `input_too_large` and the hook is not
started). A hook's stderr is kept up to 8 KiB and `hook.result` records 2 KiB of it. For `tool.before`:

```json
{"agent": "", "cwd": "/work/repo", "depth": 0, "point": "tool.before", "run_id": "7", "session_id": "s_01",
 "tool": {"args": {"content": "package app", "path": "a.go"}, "call_id": "call-1", "name": "fs.write"},
 "version": 1}
```

| Field | Every point | Meaning |
| --- | --- | --- |
| `version` | yes | `1` |
| `point` | yes | the point firing |
| `session_id`, `run_id` | yes | the session and the run lease's fencing token (a decimal string) |
| `agent`, `depth` | yes | `""` and `0` for a root run; the profile name and nesting level for a child |
| `cwd` | yes | the session workdir, which is also the hook process's working directory |
| `tool` | `tool.before`, `tool.after` | `name`, `call_id` and `args` as the model wrote them (at `tool.after`, after any rewrite) |
| `tool.result` | `tool.after` | `{"ok":true,"result":...}` or `{"ok":false,"error":{"code":"...","message":"..."}}` |
| `prompt` | `user.prompt` | the run's task |
| `turn` | `turn.after` | `index` (from 1), `tool_calls`, `final` (true when the turn made no call) |

The write to stdin has the hook's timeout as its deadline, so a hook that never reads it cannot stall the loop.

## What a hook returns

| Exit | Meaning |
| --- | --- |
| 0 | allow. Stdout is empty, or exactly one JSON object (below); anything else is the failure `invalid_output` |
| 2 | deny. Stderr, bounded to 1 KiB and stripped of control characters, is the reason |
| anything else, a signal, a timeout, a start failure, stdout over 64 KiB | hook failure: the `on_failure` policy decides |

The stdout object, all fields optional: `{"decision":"allow"|"deny","reason":"…","args":{…},"stop":true}`.
`reason` is required with a `deny` on stdout; on exit 2 an empty stderr still denies, with an empty reason.
A field the point does not permit is a failure, not ignored (`field_not_permitted`): `args` is read only from a
`transform` hook at `tool.before`, `stop` only at `turn.after`, and `context` at no point in this build
(`context_unsupported`). The failure codes recorded in `hook.result.failure` are `input_too_large`,
`invalid_input`, `start_failed`, `env_name_refused`, `lease_fd_failed`, `timeout`, `canceled`, `exit_status`,
`output_too_large`, `output_incomplete`, `invalid_output`, `field_not_permitted`, `context_unsupported` and
`kind_unsupported`.

## Timeouts and on_failure

`timeout_ms` defaults to 10 s and is capped at 60 s. On expiry, or when the run is cancelled, the hook is
stopped: on Unix it runs in its own process group, which gets SIGTERM and then SIGKILL if it has not emptied
after a 3 s grace; on Windows only the direct process is killed (see [Windows](#windows)). A descendant that
leaves the group (`setsid`) is not reached, so a hook must not start background work. A hook that exits but
leaves a child holding the pipes is the failure `output_incomplete`. A hook stopped by the engine is `timeout`
or `canceled` whatever its exit status: one that traps SIGTERM and exits 0 is never an allow.

`on_failure` is `deny` by default at `tool.before`, `user.prompt` and `compaction.before`, and `allow`
elsewhere. A guard therefore fails closed: a crashed or slow script denies every matching call. An advisory
hook sets `"on_failure": "allow"` to be recorded rather than obeyed.

## The environment a hook sees

A hook process gets `PATH` (the engine's own, or a fixed default when that is empty) and nothing else: on
Windows also `SYSTEMROOT` and `USERPROFILE`. A hook may list names in `env`, whose values are copied from the
engine's environment when they are set. Names matching `key`, `token`, `secret` or `password` are refused at
load; at run the engine refuses a wider set that also covers `credential` and `auth`, and every `FLOWSTATE_*`
name, as `env_name_refused`, so a listed `AUTH_METHOD` loads and then fails: pick another name. The working
directory is the session workdir. A hook gets no engine handle, no `FLOWSTATE_*` variable and no lease
descriptor, and must not call back into the engine. Nothing else is enforced.

## File permissions the loader enforces

Both checks are load-time checks on Unix; Windows has no equivalent and skips them.

- The hook file must be a regular file of at most 256 KiB, opened inside its own directory (a symlink that
  leaves it is refused), and not group- or world-writable. Install it as `0600`.
- The executable `argv[0]` names must, after symlink resolution, be a regular file that carries the execute bit
  and is not group- or world-writable. Install scripts as `0700` (or `0755`). A missing execute bit and a
  writable mode are the two most common load failures.

A file replaced between the check and the run is not caught: your own permissions are the boundary. An absolute
executable inside `--workdir` can be overwritten by `fs.write`; keep such files outside it.

## The protected hook tree

The file tools carry a refusal, `fs_path_protected`, for a write or edit that would land in a hook directory:
any path with a component named `.flowstate` (case-folded), any path whose physical resolution lies under
`<project root>/.flowstate` or the user hook directory (through symlinked directories or hard links, even when
the directory does not exist yet), and any symlinked file whatever it points to. Reads stay allowed.

The refusal is wired into every run that builds the file tools: `session run`, `serve`, `session tool`, a
delegated child, a nested or resumed child, and a swarm coordinator and its packet children, whatever `--hooks`
says. The project hook directory is `<project root>/.flowstate`, the directory the command was started in, never
`--workdir`. Known over-refusals: a symlinked file is never writable through the file tools (`CLAUDE.md ->
AGENTS.md` is the common layout), and this repository's own `docs/examples/swarm/.flowstate/` is refused by the
name rule. The refusal does not cover `exec.run`, `make`, `git` or a person editing the file, so project hooks
remain trust in whatever else can write to the repository.

## Windows

A command hook is argv only; no shell is involved, and a `.sh` file cannot be the executable. Name the
interpreter as `argv[0]` and give the script as an absolute path argument, since an argument is resolved by the
interpreter against the session workdir, not the hook directory:

```json
{"id": "guard", "point": "tool.before", "match": ["fs.write"], "kind": "command",
 "argv": ["sh", "C:/Users/me/AppData/Roaming/flowstate/scripts/guard-protected-paths.sh"]}
```

`sh` (for example the one Git for Windows ships) and the scripts' `jq` must be on the `PATH` the engine sees.
The permission checks above are skipped. A stopped hook loses only its direct process: the job object that
would reach its descendants is not built. In the protected-tree name rule, 8.3 short names (`FLOWST~1`) and
names with trailing dots or spaces are not matched.

## Safety and release notes

- **Do not enable `--hooks project|both` on a repository you do not trust.** A project hook file is code the
  repository ships; the flag is the whole trust decision, and it is never recorded in `flowstate.json`, so a
  clone cannot turn it on. `hooks.loaded` records the `scope` and the set hash, so an audit shows whether
  project code ran.
- **Hooks run with your permissions.** There is no confinement, by design; the scrubbed environment, the
  output caps and the timeout reduce accident and drive-by, not a hostile author who is also you.
- **The model can read hook files.** `fs.read` is allowed there. Never put a credential in one; name an
  environment variable in `env` instead.
- **What the model sees of a hook** is the fixed denial text and nothing else; reasons, stdout and stderr stay
  in the log. Output printed to you is stripped of control characters first.
- **A slow hook delays the loop, not the lease.** The run lease has no TTL; the timeout bounds the delay.
- **Fail closed, on purpose.** A broken guard script denies every matching call and the model is told
  `denied by hook <id>`. Fix the script; do not set `on_failure: "allow"` on a guard to make the symptom go away.

## Troubleshooting

Every invocation is in the session log. Read it with:

```sh
flowstate session events <id> --json | jq 'select(.type | startswith("hook"))'
```

`hooks.loaded` lists `ids`, the per-hook `file` and resolved `path`, and `inert` (hooks whose point the run's
path lacks). `hook.invoked` precedes each run; its `hook.result` carries `decision` (`allow`, `deny`, `failed`),
`exit_code`, bounded `stdout` and `stderr`, `reason`, and `failure` with the `on_failure` that was applied.
`flowstate session doctor <id>` and `GET /sessions/{id}/doctor` also report a `hooks` object (the last
`hooks.loaded`, the `unmatched` invocations, the `denials`, the `failed` count and the run's `outcome`; see the
README). An unmatched `hook.invoked` after a crash is expected: the hook is not re-run on resume.

| Symptom | Cause |
| --- | --- |
| `config_invalid: ... hook_point_unsupported` at load | the point is not fired by this build (`compaction.*`) |
| `config_invalid: ... transform is only allowed on tool.before` at load | `"transform": true` on another point |
| `... lacks the execute bit` / `... is group- or world-writable` | `chmod` the script or the hook file ([permissions](#file-permissions-the-loader-enforces)) |
| `... looks like a secret` at load | an object key holding `key`, `token`, `secret` or `password`, even inside a longer word |
| `... resolves outside the hook file's directory` | a relative `argv[0]` that climbs out; use an absolute path instead |
| `failure: start_failed` | the executable was removed after the load, it has no shebang or names a missing interpreter, or on Windows a `.sh` file was `argv[0]` |
| `failure: exit_status` with `exit_code: 127` | a command the script calls (`jq`, `grep`) is not on the hook's `PATH` |
| `failure: env_name_refused` | a listed `env` name matches the run-time secret pattern or is `FLOWSTATE_*` |
| `failure: field_not_permitted` | `stop` outside `turn.after`, or `args` from a hook that is not a `transform` |
| `failure: context_unsupported` | the script printed `context`; no point accepts it in this build |
| `failure: invalid_output` | stdout on exit 0 was not one JSON object, or a deny had no `reason` |
| `failure: input_too_large` | the call's arguments exceeded 256 KiB; the hook never ran |
| `failure: timeout` | the script exceeded `timeout_ms` |
| the hook never runs | its point is inert on a text-only run, the tool is not allowlisted (the gate runs first), or the scope did not select the file |

An executable that is missing at load fails there (`config_invalid`) and never reaches a run; `start_failed` is
the run-time removal above, a script with no shebang or one naming a missing interpreter, and the Windows case.

To try a script by hand with the environment a hook gets:

```sh
printf '%s' '{"point":"tool.before","tool":{"name":"fs.write","args":{"path":"migrations/x.sql"}}}' \
  | env -i PATH="$PATH" HOME="$HOME" ./scripts/guard-protected-paths.sh; echo "exit $?"
```

## What is not built

- `context` output at any point (`context_unsupported`), and the `hook.context` event.
- The `compaction.before` and `compaction.after` points.
- System One `decision` hooks: `kind: "decision"` loads nothing.
- Profile hooks: a `hooks:` block in agent profile frontmatter.
- The Windows job object.
- Run-level points (`user.prompt`, `session.start`, `session.end`) for a delegated child or a swarm coordinator.

## The worked example

[docs/examples/hooks](../examples/hooks/README.md) holds a user file with six hooks and their POSIX `sh`
scripts, installed with two `cp` lines and two `chmod` lines. A spec loads it under the real loader and runs
every script on the engine's own stdin documents, then again with a `PATH` holding only `cat`: no script may
exit 0 because a command it needed was missing.
