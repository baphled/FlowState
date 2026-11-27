# Lifecycle hooks: a worked example

A user hook file and the scripts it runs, for the lifecycle points this build fires
([guide](../../guides/LIFECYCLE-HOOKS.md), [ADR-012](../../adr/ADR-012-lifecycle-hooks.md)). Every file here
loads under the real loader, and a spec in `internal/app/hooks_test.go` runs every script on the engine's own
stdin documents, then again with a `PATH` too poor to run it: change a file here and the spec says what broke.

## Files

| File | Point | What it does |
| --- | --- | --- |
| [hooks.json](hooks.json) | | the hook file: six hooks, written as a user file |
| [guard-protected-paths.sh](scripts/guard-protected-paths.sh) | `tool.before`, `fs.write` and `fs.edit` | denies a write under `migrations/` or to a credential-looking file |
| [audit-tool-calls.sh](scripts/audit-tool-calls.sh) | `tool.after`, every tool | appends one line per executed call to `$HOME/flowstate-tool-audit.log` |
| [stop-after-n-turns.sh](scripts/stop-after-n-turns.sh) | `turn.after` | prints `{"stop":true}` once the turn index reaches its argument (5) |
| [refuse-secret-prompt.sh](scripts/refuse-secret-prompt.sh) | `user.prompt` | refuses a task that holds a credential pattern |
| [log-session.sh](scripts/log-session.sh) | `session.start` and `session.end` | appends the point, session and run to `$HOME/flowstate-session.log` |

Each script's header names the stdin fields it reads and the exit codes it returns. The scripts that read JSON
need `jq`; `refuse-secret-prompt.sh` needs only `grep`. A hook inherits `PATH` and nothing else, so `jq`, or
`grep`, must be on the `PATH` the engine sees. No script assumes a command worked: each branches on the status
it needs and passes any other through, so a missing command is an exit 127, which a deny-default gate or a
prompt refusal turns into a denial and an observer into a recorded failure (so without `jq` the turn cap fails
open: the run goes on and the failure is recorded). Both loggers list `HOME` in the hook's `env` to find the home
directory.

## Install it

From the repository root, into the user hook directory (Linux `~/.config/flowstate`, macOS
`~/Library/Application Support/flowstate`, Windows `%AppData%\flowstate`):

```sh
mkdir -p ~/.config/flowstate/scripts
cp docs/examples/hooks/hooks.json ~/.config/flowstate/
cp docs/examples/hooks/scripts/*.sh ~/.config/flowstate/scripts/
chmod 600 ~/.config/flowstate/hooks.json
chmod 700 ~/.config/flowstate/scripts/*.sh
```

The `chmod` lines are not optional on Unix: the loader refuses a hook file that is group- or world-writable, and
an executable that lacks the execute bit or is group- or world-writable. On Windows the permission checks are
skipped, and a `.sh` file cannot be the executable: name the interpreter as `argv[0]`
([Windows](../../guides/LIFECYCLE-HOOKS.md#windows) in the guide). The user file is the default scope, so the next
`flowstate session run` loads it with no flag. A hook file that fails to load refuses the run with a
configuration error (`config_invalid`, exit 4) before any provider request.

## Try it and read the log

```sh
flowstate session run <id> --allow fs.write --db flowstate.db
flowstate session events <id> --json | jq 'select(.type | startswith("hook"))'
```

`hooks.loaded` names the set that ran. Every `hook.invoked` is followed by its `hook.result`, whose `decision`,
`exit_code`, `stderr` and `failure` say what the script did. A denied write appears in the model's history as
`error: denied by hook guard-protected-paths`; the reason is in `hook.result` and never reaches the model.

To skip the file for one run pass `--hooks none`; to stop loading it, remove `~/.config/flowstate/hooks.json`.
`fs.read` can read these files, so never put a credential in one
([Safety](../../guides/LIFECYCLE-HOOKS.md#safety-and-release-notes)).
