# autoresearch

`autoresearch` is a ratcheting optimisation harness. It drives a single file — an agent manifest, a skill body, or a Go source file — toward a scalar metric over multiple trials. It is not a one-shot edit: it iterates, scores, keeps the best candidate, and stops when it converges or exhausts the budget.

**When to use it:**

- You want to reduce validation warnings on an agent manifest without hand-editing each hypothesis in turn.
- You want to run a Go performance ratchet and let the harness keep only commits that strictly improve `ns/op`.
- You have a custom scalar and a file you want to pull in a specific direction over many automated trials.

**When not to use it:**

- You need to change multiple files in one logical edit. The harness is single-surface by design; multi-file edits are out of scope.
- You want a one-shot rewrite. Use a direct agent invocation instead.

---

## Invocation modes

There are two ways to start a run:

1. **`/autoresearch` slash command** in the FlowState TUI — an 8-step wizard that assembles the command for you.
2. **`flowstate autoresearch run ...`** in the terminal — direct CLI invocation.

Both invoke the same harness. The wizard is a convenience wrapper that pre-fills flags from a preset and injects the assembled command into chat.

---

## TUI wizard

Type `/autoresearch` in the FlowState chat. An 8-step wizard opens:

**Step 1 — Choose a preset**

| Preset | Surface type | Evaluator | Metric direction |
|---|---|---|---|
| Planner quality | Agent manifest (`.md`) | `planner-validate.sh` | min |
| Performance | Go source file | `bench.sh` | min |
| Custom | Any | You provide | You choose |

**Step 2 — Surface file path**

Path to the file to optimise, relative to the repo root. Examples:
- `internal/app/agents/planner.md`
- `internal/engine/engine.go`

**Step 3 — Evaluator script path**

Pre-filled from the preset. Accept the default or enter an absolute or repo-relative path to your own evaluator.

**Step 4 — Driver script path**

Pre-filled from the preset. Accept the default or enter a path to your own driver.

**Step 5 — Metric direction**

`min` (lower is better) or `max` (higher is better). Pre-filled from the preset.

**Step 6 — Max trials**

Hard cap on the number of trials. Default: `10`.

**Step 7 — Time budget**

Wall-clock budget as a Go duration string. Default: `5m`. The harness terminates when either the trial cap or the time budget fires, whichever comes first.

**Step 8 — Confirm**

The wizard previews the assembled `flowstate autoresearch run ...` command. Confirm to inject it as a user message in chat, or cancel to go back.

On confirmation the command is injected into the chat. If an agent with `autoresearch_run` in its tool list is active (e.g. Senior-Engineer or a custom delegating agent), that agent picks it up and runs it as a background task. Otherwise, copy the command from the chat and run it in the terminal.

---

## CLI flags

```bash
flowstate autoresearch run \
  --surface <path>              \
  --evaluator-script <path>     \
  --driver-script <path>        \
  --metric-direction min|max    \
  --max-trials <int>            \
  --time-budget <duration>      \
  --program <skill-name|path>
```

| Flag | Required | Default | Description |
|---|---|---|---|
| `--surface` | yes | — | File to optimise, relative to invocation cwd |
| `--evaluator-script` | yes | — | Script that scores candidates; reads stdin, prints one integer to stdout, exits 0 |
| `--driver-script` | yes | — | Script that generates candidates; reads the synthesised prompt from stdin, prints the candidate to stdout, exits 0 |
| `--metric-direction` | no | `min` | `min` — lower scores are kept; `max` — higher scores are kept |
| `--max-trials` | no | `10` | Hard cap on trial count |
| `--time-budget` | no | `5m` | Wall-clock budget as a Go duration string (`5m`, `1h`, `30s`) |
| `--program` | no | `autoresearch` | Skill name or path used as the prose program for the agent driver; narrows what the driver asks the agent to do |

### Commit-trials mode

By default the harness runs entirely in memory — the surface file on disk is never touched during a run. Candidates flow as strings between the driver and evaluator; the best candidate is held in the coordination store and retrieved with `autoresearch apply`.

To use the legacy git-mediated mode, add `--commit-trials`:

```bash
flowstate autoresearch run \
  --surface internal/app/agents/planner.md \
  --evaluator-script scripts/autoresearch-evaluators/planner-validate-commit.sh \
  --driver-script scripts/autoresearch-drivers/default-assistant-driver-commit.sh \
  --commit-trials
```

`--commit-trials` requires a clean working tree unless `--allow-dirty` is also set. The `-commit.sh` script variants read and write the surface file inside the trial worktree rather than exchanging candidates over stdin/stdout.

---

## Reference scripts

The following scripts ship with FlowState and cover the two standard use cases.

### Evaluators

**`scripts/autoresearch-evaluators/planner-validate.sh`**

Counts validation warnings for an agent manifest. Reads the candidate from stdin (or `FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE`), stages it in a tempfile, and runs `validate-harness.sh --score --all`. Emits the integer warning count. Use with `--metric-direction min`.

**`scripts/autoresearch-evaluators/bench.sh`**

Go benchmark wrapper. Reads and discards the candidate from stdin (benchmarks key off compiled binaries), then runs `go test -bench`. Emits an integer score. Default metric is `ops_per_sec` (higher is better, so use with `--metric-direction max`); set `FLOWSTATE_AUTORESEARCH_BENCH_METRIC=ns_per_op` to emit nanoseconds-per-op instead (use with `--metric-direction min`).

Environment knobs for `bench.sh`:

| Variable | Default | Description |
|---|---|---|
| `FLOWSTATE_AUTORESEARCH_BENCH_PKG` | `./...` | Go package passed to `go test -bench` |
| `FLOWSTATE_AUTORESEARCH_BENCH_NAME` | `.` | `-bench` filter regex |
| `FLOWSTATE_AUTORESEARCH_BENCH_METRIC` | `ops_per_sec` | `ops_per_sec` or `ns_per_op` |
| `FLOWSTATE_AUTORESEARCH_BENCH_TIMEOUT` | `60s` | Passed to `go test -timeout` |

### Drivers

**`scripts/autoresearch-drivers/default-assistant-driver.sh`**

Reads the synthesised per-trial prompt from stdin (or `FLOWSTATE_AUTORESEARCH_PROMPT_FILE`), invokes `flowstate run --agent default-assistant`, and parses the agent's fenced ` ```surface ` block from the response. Writes the candidate verbatim to stdout.

The parser follows a three-tier fallback:

1. A fenced block tagged ` ```surface ` — primary.
2. A single bare ` ``` ` block, when the response contains exactly one fenced block — fallback.
3. No block found — exits 3; the harness records `validator-io-error`.

### Preset programs

**`skills/autoresearch-presets/planner-quality.md`**

Program (prose goal) for manifest optimisation. Pass as `--program skills/autoresearch-presets/planner-quality.md` or use the Planner quality preset in the wizard.

**`skills/autoresearch-presets/perf-preserve-behaviour.md`**

Program for Go performance ratcheting that constrains the agent to preserve observable behaviour while improving throughput. Pass as `--program skills/autoresearch-presets/perf-preserve-behaviour.md` or use the Performance preset in the wizard.

---

## Worked examples

### Planner manifest quality ratchet

Reduce validation warnings on the planner agent manifest over up to 10 trials, stopping after 5 minutes:

```bash
flowstate autoresearch run \
  --surface internal/app/agents/planner.md \
  --evaluator-script scripts/autoresearch-evaluators/planner-validate.sh \
  --driver-script scripts/autoresearch-drivers/default-assistant-driver.sh \
  --metric-direction min \
  --max-trials 10 \
  --time-budget 5m
```

The baseline score is taken from the manifest as it stands when you run the command. Any trial that produces a strictly lower warning count becomes the new best candidate. The harness converges automatically when five consecutive trials show no improvement.

### Go performance ratchet

Minimise `ns/op` on the engine package, allowing up to 30 trials over one hour:

```bash
flowstate autoresearch run \
  --surface internal/engine/engine.go \
  --evaluator-script scripts/autoresearch-evaluators/bench.sh \
  --driver-script scripts/autoresearch-drivers/default-assistant-driver.sh \
  --program skills/autoresearch-presets/perf-preserve-behaviour.md \
  --metric-direction min \
  --max-trials 30 \
  --time-budget 1h
```

For `ns_per_op` scoring with `bench.sh`, set the environment variable before running:

```bash
FLOWSTATE_AUTORESEARCH_BENCH_METRIC=ns_per_op \
  flowstate autoresearch run \
  --surface internal/engine/engine.go \
  --evaluator-script scripts/autoresearch-evaluators/bench.sh \
  --driver-script scripts/autoresearch-drivers/default-assistant-driver.sh \
  --metric-direction min \
  --max-trials 30 \
  --time-budget 1h
```

---

## Retrieving results

After a run completes, use the `list` and `apply` subcommands to inspect and materialise the best candidate.

```bash
# List all runs and their best scores
flowstate autoresearch list

# Print the best candidate from a run to stdout
flowstate autoresearch apply <run-id>

# Write the best candidate to a file you choose
flowstate autoresearch apply <run-id> --write /tmp/best.md
```

`apply` never writes inside the source repo by default. The `--write` flag accepts any path; if you want to materialise the candidate directly over the surface file (overwriting it in place), pass `--force-inside-repo`:

```bash
flowstate autoresearch apply <run-id> --force-inside-repo
```

The run ID is shown in `flowstate autoresearch list` output. It is also printed to stdout when `flowstate autoresearch run` starts.

---

## Writing your own evaluator

An evaluator is an executable script that:

1. Reads the candidate string from stdin (the harness also writes the same bytes to `FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE` if you prefer a file path).
2. Prints exactly one non-negative integer to stdout.
3. Exits 0.

Anything else — multiple lines, non-integer output, empty output, non-zero exit — is recorded as an `evaluator-contract-violation`. Three consecutive violations terminate the run with `evaluator-contract-failure-rate`.

Minimal stub:

```bash
#!/usr/bin/env bash
# Drain stdin — evaluators must consume it to avoid blocking the harness.
cat > /dev/null
# Emit a score. Replace 42 with your real metric.
echo 42
```

A more realistic evaluator that counts a pattern in the candidate:

```bash
#!/usr/bin/env bash
set -euo pipefail
# Count occurrences of "TODO" in the candidate; lower is better.
candidate="$(cat)"
count="$(printf '%s\n' "$candidate" | grep -c 'TODO' || true)"
echo "$count"
```

Environment variables available to every evaluator:

| Variable | Description |
|---|---|
| `FLOWSTATE_AUTORESEARCH_RUN_ID` | Identifier of the current run |
| `FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE` | Path to a file containing the same bytes as stdin |

The harness checks that the evaluator exists, is a regular file, and is executable before the run starts. A mis-typed path fails immediately rather than burning trials.

### Timeout behaviour

The harness caps evaluator wall-clock via `--evaluator-timeout` (default 5 minutes). At the deadline it sends SIGTERM; 30 seconds later SIGKILL. A timeout records `evaluator_timeout_ms` on the trial outcome but does not count as a contract violation.

---

## Writing your own driver

A driver is an executable script that:

1. Reads the synthesised per-trial prompt from stdin (also available at `FLOWSTATE_AUTORESEARCH_PROMPT_FILE`).
2. Invokes whatever generates the candidate (an LLM, a script, a heuristic).
3. Prints the full candidate string to stdout — no fenced-block wrapping.
4. Exits 0.

The prompt the harness synthesises contains four sections:

```
# PROGRAM
<contents of --program>

# SURFACE
<current candidate or baseline surface contents>

# HISTORY
<last N trial outcomes: score, kept flag, reason, candidate SHA>

# INSTRUCTION
<harness-generated instruction for this trial>
```

Environment variables available to every driver:

| Variable | Description |
|---|---|
| `FLOWSTATE_AUTORESEARCH_RUN_ID` | Identifier of the current run |
| `FLOWSTATE_AUTORESEARCH_TRIAL` | 1-based trial counter |
| `FLOWSTATE_AUTORESEARCH_SURFACE` | Surface path relative to invocation cwd — read-only by contract |
| `FLOWSTATE_AUTORESEARCH_PROMPT_FILE` | Path to the same bytes as stdin |
| `FLOWSTATE_AUTORESEARCH_DRIVER_MAX_TURNS` | Soft cap on agent turns (default 10 if unset) |

The driver timeout is `--driver-timeout` (default 3 minutes). Timeout collapses to `validator-io-error`.

Operator-authored drivers that wrap a different model, a local inference server, or a scripted heuristic edit can copy `default-assistant-driver.sh` and replace the `flowstate run` invocation. The env-var contract and the stdin/stdout convention are the only load-bearing parts.

---

## Asking an agent to run autoresearch

If you are using a delegating agent — Senior-Engineer, Planner, or a custom agent with `can_delegate: true` — you can describe the goal in plain English and the agent will construct the run command:

> "Optimise `internal/app/agents/planner.md` to reduce validation warnings."

The agent invokes `autoresearch_run` with the appropriate preset. Monitor progress via the background task output in the TUI, or by polling `flowstate autoresearch list`.

---

## Convergence and termination

The harness stops when any of the following conditions fires:

| Condition | Description |
|---|---|
| `max-trials` | The `--max-trials` cap is reached |
| `time-budget` | The `--time-budget` wall-clock cap is reached |
| `converged` | Five consecutive trials with `reason=no-improve` |
| `fixed-point-saturated` | The SHA ring is saturated — the driver keeps proposing identical candidates |
| `manifest-gate-failure-rate` | Three consecutive `manifest-validate-failed` trials |
| `evaluator-contract-failure-rate` | Three consecutive evaluator contract violations |

After three consecutive no-improve trials the harness continues but the agent should vary its approach. If the same edit keeps appearing, the harness records `fixed-point-skipped` and the loop closes itself out after the SHA ring fills.

Equal scores never improve the ratchet. A trial must produce a score strictly better than the current best to be kept.

---

## Off-limits fields (manifest surfaces)

When optimising a manifest, the harness enforces that the driver does not delete entries from structural fields that the evaluator scores against. These fields are derived from the manifest's current frontmatter at each trial, not from a fixed list:

- `id`
- `schema_version`
- `capabilities.tools`
- `capabilities.always_active_skills` (every entry currently listed)
- `delegation.delegation_allowlist`
- `delegation.can_delegate`
- `coordination_store`
- `harness`
- `harness_enabled` (when present)

Deleting an entry from any of these fields to silence a validator warning is a score-gaming violation. The manifest gate catches it and records `manifest-validate-failed`. Three consecutive gate failures terminate the run.
