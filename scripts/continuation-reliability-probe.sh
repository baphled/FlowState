#!/usr/bin/env bash
# continuation-reliability-probe.sh — deterministic CONTINUATION-RELIABILITY
# probe for FlowState models.
#
# THE QUESTION
# ------------
# Does a given model SYNTHESIS-HANG on tool-loop continuations? On a turn
# AFTER the first tool call, does it narrate ("I'll write the result…") but
# emit NO tool call? glm-class models are suspected of this; Claude/GPT-class
# are expected to be reliable. This probe produces per-model EVIDENCE to
# replace the extrapolated Tier A/C rankings in the Swarm Model Selection
# reference with real data.
#
# HOW IT WORKS
# ------------
# For each candidate model the probe:
#   1. Pins the run to EXACTLY that provider/model. `flowstate run` has no
#      --model flag and an agent manifest's preferred_models is NOT honoured
#      by the ROOT run engine (it drives delegate engines + the model
#      picker only — the root engine takes its active model from the
#      failover manager's BASE preferences, which are built from CONFIG by
#      providers.BuildConfigPreferences). So we pin via a throwaway --config:
#      set providers.default to the target provider and CLEAR every other
#      provider's `model` field, leaving a single-entry preference list = a
#      hard pin with NO failover tail (a rate-limited target ERRORs rather
#      than silently failing over and polluting per-model attribution).
#   2. Runs a FIXED task that REQUIRES multiple tool-loop turns: read file A,
#      read file B, then write a combined summary to a coordination_store
#      key. This forces continuation behaviour to be exercised, not just
#      turn 1.
#   3. Parses the persisted session JSON (same approach as
#      validate-harness.sh: jq over the messages array, reading
#      .message.Role / .Content / .ToolCalls / .ModelID) for the
#      synthesis-hang signature.
#   4. Emits a per-model verdict: RELIABLE / HANGS / ERROR, plus a hang rate
#      over N repeats (synthesis-hang is probabilistic).
#
# THE HANG SIGNATURE
# ------------------
# A continuation assistant turn (one occurring AFTER the first tool call)
# that has narration Content (matching write|writing|let me|now I|I'll|
# I will|next|then|going to) AND empty ToolCalls, BEFORE the task completed
# (before the final coordination_store write landed), is a HANG. The
# LEGITIMATE terminal summary — a content-only turn AFTER the coordination
# write landed — is NOT a hang. A run where the coordination key never
# landed at all is also a HANG.
#
# This is a REPORT, not a gate. It mirrors validate-harness.sh: a dev-loop
# diagnostic, not CI. Non-zero exit ONLY on its own usage / I/O errors,
# never on a model verdict.

set -uo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." &>/dev/null && pwd)"

FLOWSTATE_BIN="${FLOWSTATE_BIN:-$REPO_ROOT/build/flowstate}"
if [[ ! -x "$FLOWSTATE_BIN" ]]; then
  if [[ -x "$REPO_ROOT/flowstate" ]]; then FLOWSTATE_BIN="$REPO_ROOT/flowstate"
  elif command -v flowstate &>/dev/null; then FLOWSTATE_BIN="$(command -v flowstate)"; fi
fi

FLOWSTATE_DATA_DIR="${FLOWSTATE_DATA_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/flowstate}"
FLOWSTATE_SESSIONS_DIR="${FLOWSTATE_SESSIONS_DIR:-$FLOWSTATE_DATA_DIR/sessions}"
COORD_STORE_PATH="$FLOWSTATE_DATA_DIR/coordination.json"
# The live config we copy from to build per-model throwaway pins. We never
# write to this file — every per-model run uses a derived /tmp copy.
LIVE_CONFIG="${FLOWSTATE_CONFIG:-${XDG_CONFIG_HOME:-$HOME/.config}/flowstate/config.yaml}"
# Throwaway agents land in the live agents dir so the registry loads them
# regardless of the --config override (the agents dir is a separate flag).
AGENTS_DIR="${FLOWSTATE_AGENTS_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/flowstate/agents}"

# How many times to run each model. Synthesis-hang is probabilistic, so a
# single pass under-reports it. Override with REPEATS=N.
REPEATS="${REPEATS:-3}"
# Per-run wall-clock cap (seconds). A hung model can stall the whole loop;
# the timeout bounds it so the matrix always makes progress.
RUN_TIMEOUT="${RUN_TIMEOUT:-240}"
# Pause (seconds) before the single retry on a rate-limit / provider error.
RETRY_PAUSE="${RETRY_PAUSE:-20}"
# Pause between runs to be gentle on contended providers.
INTER_RUN_PAUSE="${INTER_RUN_PAUSE:-3}"

# A unique tag for this probe invocation so our throwaway artefacts never
# collide with a concurrent flowstate session or another probe run.
PROBE_TAG="probe-$$-$(date +%s)"
PROBE_AGENT_ID="continuation-probe-$PROBE_TAG"
PROBE_AGENT_FILE="$AGENTS_DIR/$PROBE_AGENT_ID.md"
FIXTURE_DIR="$(mktemp -d "/tmp/${PROBE_TAG}-fixtures.XXXXXX")"
FILE_A="$FIXTURE_DIR/a.txt"
FILE_B="$FIXTURE_DIR/b.txt"

# Default candidate matrix. Decision-critical contrast first: at least one
# glm model vs at least one Claude/GPT model validates the Tier A/C split.
# Override by passing models as positional args:
#   continuation-reliability-probe.sh zai/glm-4.6 openai/gpt-4o
DEFAULT_MODELS=(
  "zai/glm-4.6"
  "zai/glm-5"
  "openai/gpt-4o"
  "openzen/claude-sonnet-4-5"
  "anthropic/claude-sonnet-4-20250514"
  "github/gpt-4o"
)

die() { echo "error: $*" >&2; cleanup; exit 1; }

cleanup() {
  rm -f "$PROBE_AGENT_FILE" 2>/dev/null || true
  rm -rf "$FIXTURE_DIR" 2>/dev/null || true
  rm -f "/tmp/${PROBE_TAG}-cfg-"*.yaml 2>/dev/null || true
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Pre-flight
# ---------------------------------------------------------------------------
[[ -x "$FLOWSTATE_BIN" ]] || die "flowstate binary not found: $FLOWSTATE_BIN (run 'go build -o build/flowstate ./cmd/flowstate' or set FLOWSTATE_BIN)"
command -v jq &>/dev/null || die "jq is required but not installed"
command -v python3 &>/dev/null || die "python3 is required (used to derive per-model pin configs)"
python3 -c 'import yaml' 2>/dev/null || die "python3 'pyyaml' module is required (pip install pyyaml)"
[[ -f "$LIVE_CONFIG" ]] || die "live config not found: $LIVE_CONFIG (set FLOWSTATE_CONFIG)"
[[ -d "$AGENTS_DIR" ]] || die "agents dir not found: $AGENTS_DIR (set FLOWSTATE_AGENTS_DIR)"

# ---------------------------------------------------------------------------
# Fixtures + throwaway probe agent
# ---------------------------------------------------------------------------
printf 'FILE-A: The deployment runs on three nodes in eu-west-1.\n' > "$FILE_A"
printf 'FILE-B: Each node carries a hot standby replica.\n'         > "$FILE_B"

write_probe_agent() {
  cat > "$PROBE_AGENT_FILE" <<EOF
---
schema_version: "1.0.0"
id: $PROBE_AGENT_ID
name: Continuation Reliability Probe
aliases: []
complexity: standard
uses_recall: false
capabilities:
  tools:
    - read
    - write
    - bash
    - coordination_store
  skills: []
  always_active_skills: []
  mcp_servers: []
  capability_description: "Throwaway diagnostic agent for the continuation-reliability probe."
context_management:
  max_recursion_depth: 1
  summary_tier: quick
  sliding_window_size: 10
  compaction_threshold: 0.75
delegation:
  can_delegate: false
  delegation_table: {}
metadata:
  role: "Continuation reliability probe agent"
  goal: "Read two files then write a combined summary to the coordination store, using one tool call per step"
  when_to_use: "Diagnostic probe only — not for production routing"
orchestrator_meta:
  cost: FREE
---

# Continuation Reliability Probe Agent

You are a diagnostic agent exercising the tool-loop continuation path. Follow
the user's multi-step instructions exactly, using ONE tool call per step. Every
turn that mentions reading or writing MUST include the corresponding tool call —
never narrate an action without performing it. Finish only after the
coordination_store write has been made.
EOF
}

# build_pin_config <provider> <model> <out-path>
# Derives a throwaway config from the live one: sets providers.default to the
# target provider, sets ONLY that provider's `model`, and CLEARS every other
# provider's model so BuildConfigPreferences yields a single-entry preference
# list (a hard pin, no failover tail). Also disables compaction / MCP / memory
# extraction so each probe run is fast and free of confounding side-effects.
build_pin_config() {
  local provider="$1" model="$2" out="$3"
  PROBE_PROVIDER="$provider" PROBE_MODEL="$model" \
  PROBE_LIVE_CONFIG="$LIVE_CONFIG" PROBE_OUT="$out" \
  python3 - <<'PY'
import os, yaml
with open(os.environ['PROBE_LIVE_CONFIG']) as f:
    cfg = yaml.safe_load(f) or {}
provider = os.environ['PROBE_PROVIDER']
model    = os.environ['PROBE_MODEL']
prov = cfg.setdefault('providers', {})
prov['default'] = provider
# These are the seven provider keys BuildConfigPreferences inspects.
for pname in ('ollama','ollamacloud','anthropic','openai','github','zai','openzen'):
    node = prov.get(pname)
    if isinstance(node, dict):
        node['model'] = ''
# Set ONLY the target provider's model. If the provider key is missing
# entirely the run will surface that as an ERROR, which is the right outcome.
target = prov.setdefault(provider, {})
if isinstance(target, dict):
    target['model'] = model
# Quieten confounders: no auto/micro compaction, no session-memory
# extraction, no MCP servers spinning up per run.
cfg['compression'] = {
    'auto_compaction': {'enabled': False, 'threshold': 0.75},
    'micro_compaction': {'enabled': False},
    'session_memory': {'enabled': False},
}
cfg['compaction'] = {'micro_enabled': False, 'fact_extraction_enabled': False}
cfg['mcp_servers'] = []
with open(os.environ['PROBE_OUT'], 'w') as f:
    yaml.safe_dump(cfg, f, default_flow_style=False, sort_keys=False)
PY
}

# is_rate_limit_or_provider_error <stderr-file>
# Heuristic match for transient provider failures worth a single retry.
is_rate_limit_or_provider_error() {
  local errfile="$1"
  [[ -s "$errfile" ]] || return 1
  grep -qiE '429|rate.?limit|too many requests|overloaded|timeout|timed out|temporarily unavailable|service unavailable|503|502|500|connection reset|EOF|deadline exceeded' "$errfile"
}

# ---------------------------------------------------------------------------
# Session analysis
# ---------------------------------------------------------------------------
NARRATION_RE='write|writing|let me|now i|i.?ll|i will|next|then|going to|i.?m going|proceed|combine|summary'

# analyse_session <session-file> <coord-key> <expected-model>
# Prints a single TSV line:
#   verdict<TAB>total_tool_calls<TAB>hang_turns<TAB>write_landed<TAB>asst_turns<TAB>model_id
# verdict is RELIABLE, HANGS, or PIN-FAILED. PIN-FAILED means the run
# completed but the active model was NOT the pinned target (the pin failed
# over to a fallback provider, e.g. ollama/llama3.2, because the target was
# unreachable). PIN-FAILED runs carry NO signal about the target model and
# are counted as ERROR by the caller — the hang the fallback model exhibited
# says nothing about Claude/GPT/glm. (Hard ERROR — no run at all — is decided
# by the caller from the run exit, before this is ever called.)
analyse_session() {
  local sf="$1" coord_key="$2" expected_model="${3:-}"
  local model_id total_tc asst hang write_landed

  model_id=$(jq -r '[.messages[]|select(.message.Role=="assistant")|.message.ModelID // ""]|map(select(length>0))|.[0] // "?"' "$sf")
  total_tc=$(jq '[.messages[].message.ToolCalls // []|.[]?]|length' "$sf")
  asst=$(jq '[.messages[]|select(.message.Role=="assistant")]|length' "$sf")

  # Did the final coordination_store write land? Read the live coord store
  # for the requested key and require a non-empty value.
  local coord_val=""
  if [[ -s "$COORD_STORE_PATH" ]]; then
    coord_val=$(jq -r --arg k "$coord_key" '.[$k] // ""' "$COORD_STORE_PATH" 2>/dev/null || echo "")
  fi
  if [[ -n "$coord_val" ]]; then write_landed="yes"; else write_landed="no"; fi

  # Hang turns: continuation assistant turns (index of first tool-bearing
  # assistant turn < this turn's index) that have narration Content AND empty
  # ToolCalls AND are NOT the legitimate terminal summary. The terminal
  # summary is the LAST assistant turn when the write already landed; we
  # exclude exactly that turn from the hang count.
  #
  # jq walks the assistant turns in order, tracks whether a tool call has
  # been seen yet, and flags any post-first-tool content-only narration turn
  # that is not the final turn.
  hang=$(jq -r --arg re "$NARRATION_RE" --arg landed "$write_landed" '
    [.messages[]|select(.message.Role=="assistant")] as $a
    | ($a|length) as $n
    | reduce range(0; $n) as $i (
        {seen_tool:false, hangs:0};
        ($a[$i].message) as $m
        | (($m.ToolCalls // [])|length) as $tc
        | (($m.Content // "")) as $c
        | if $tc > 0 then .seen_tool = true
          else
            # content-only assistant turn
            if .seen_tool
               and ($c|ascii_downcase|test($re))
               and ($c|length) > 0
               # exclude the terminal summary turn ONLY when the write landed
               and (($i < ($n-1)) or ($landed == "no"))
            then .hangs += 1
            else .
            end
          end
      )
    | .hangs' "$sf")

  # PIN-FAILED takes precedence over any hang/reliable verdict: if the
  # active model is not the pinned target, the run says nothing about the
  # target and must not be scored as a hang of it.
  local verdict
  if [[ -n "$expected_model" && "$model_id" != "?" && "$model_id" != "$expected_model" ]]; then
    verdict="PIN-FAILED"
  elif [[ "${hang:-0}" -gt 0 || "$write_landed" == "no" ]]; then
    # HANGS when: any mid-loop narrate-without-act turn, OR the write never
    # landed (the multi-step task did not complete).
    verdict="HANGS"
  else
    verdict="RELIABLE"
  fi

  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$verdict" "$total_tc" "${hang:-0}" "$write_landed" "$asst" "$model_id"
}

# ---------------------------------------------------------------------------
# One run
# ---------------------------------------------------------------------------
# run_once <provider> <model> <slug> <run-index> <cfg-path> <coord-key>
# Echoes one of:
#   OK\t<verdict>\t<total_tc>\t<hang>\t<write_landed>\t<asst>\t<model_id>
#   ERROR\t<reason>
run_once() {
  local provider="$1" model="$2" slug="$3" idx="$4" cfg="$5" coord_key="$6"
  local session_id="$PROBE_TAG-$slug-$idx"
  local sf="$FLOWSTATE_SESSIONS_DIR/$session_id.json"
  local errf; errf="$(mktemp "/tmp/${PROBE_TAG}-run-err.XXXXXX")"

  local prompt
  prompt=$(cat <<EOF
Complete this multi-step task using tool calls only — one tool call per step.
Step 1: use the read tool to read $FILE_A.
Step 2: use the read tool to read $FILE_B.
Step 3: use the coordination_store tool with operation set to "set", key set to "$coord_key", and value set to a single sentence that combines the facts from both files.
Do each step as a separate tool call. Do not finish until the coordination_store write has been made.
EOF
)

  local attempt rc=0
  for attempt in 1 2; do
    rc=0
    timeout "$RUN_TIMEOUT" "$FLOWSTATE_BIN" \
      --config "$cfg" \
      --agents-dir "$AGENTS_DIR" \
      --sessions-dir "$FLOWSTATE_SESSIONS_DIR" \
      run --agent "$PROBE_AGENT_ID" --session "$session_id" --prompt "$prompt" \
      >/dev/null 2>"$errf" || rc=$?

    if [[ "$rc" -eq 0 && -f "$sf" ]]; then
      local line; line="$(analyse_session "$sf" "$coord_key" "$model")"
      rm -f "$errf"
      printf 'OK\t%s\n' "$line"
      return 0
    fi

    # Failed. Retry once on a transient provider error; otherwise give up.
    if [[ "$attempt" -eq 1 ]] && is_rate_limit_or_provider_error "$errf"; then
      echo "    (run $idx attempt 1 hit a transient provider error; retrying in ${RETRY_PAUSE}s)" >&2
      sleep "$RETRY_PAUSE"
      continue
    fi
    break
  done

  local reason
  reason="$(tail -1 "$errf" 2>/dev/null | tr -d '\r' | cut -c1-160)"
  [[ -z "$reason" ]] && reason="run exited rc=$rc with no stderr (timeout or hard kill)"
  rm -f "$errf"
  printf 'ERROR\t%s\n' "$reason"
  return 0
}

# ---------------------------------------------------------------------------
# Main matrix
# ---------------------------------------------------------------------------
main() {
  local models=()
  if [[ $# -gt 0 ]]; then models=("$@"); else models=("${DEFAULT_MODELS[@]}"); fi

  write_probe_agent

  echo "== continuation-reliability-probe =="
  echo "binary:   $FLOWSTATE_BIN"
  echo "config:   derived per-model from $LIVE_CONFIG"
  echo "agent:    $PROBE_AGENT_ID (throwaway, tools: read/write/bash/coordination_store)"
  echo "repeats:  $REPEATS per model   run timeout: ${RUN_TIMEOUT}s"
  echo "models:   ${models[*]}"
  echo

  # Accumulate results for the closing table.
  local -a RESULT_ROWS=()

  local spec
  for spec in "${models[@]}"; do
    local provider="${spec%%/*}"
    local model="${spec#*/}"
    local slug; slug="$(printf '%s' "$spec" | tr '/.' '__')"
    local cfg="/tmp/${PROBE_TAG}-cfg-$slug.yaml"

    echo "-- $spec --"
    build_pin_config "$provider" "$model" "$cfg" \
      || { echo "   ERROR: could not derive pin config"; RESULT_ROWS+=("$spec|ERROR|-|-|config-derivation-failed"); echo; continue; }

    local reliable=0 hangs=0 errors=0 pinfailed=0 any_write=0 last_model_id="?"
    local i
    for ((i=1; i<=REPEATS; i++)); do
      local coord_key="probe/$slug/result-$i"
      # Clear any stale value for this key so write_landed reflects THIS run.
      if [[ -s "$COORD_STORE_PATH" ]]; then
        local tmpc; tmpc="$(mktemp "/tmp/${PROBE_TAG}-coord.XXXXXX")"
        jq --arg k "$coord_key" 'del(.[$k])' "$COORD_STORE_PATH" > "$tmpc" 2>/dev/null && mv "$tmpc" "$COORD_STORE_PATH" || rm -f "$tmpc"
      fi

      local out; out="$(run_once "$provider" "$model" "$slug" "$i" "$cfg" "$coord_key")"
      local status; status="$(printf '%s' "$out" | cut -f1)"
      if [[ "$status" == "ERROR" ]]; then
        local reason; reason="$(printf '%s' "$out" | cut -f2-)"
        echo "   run $i: ERROR — $reason"
        errors=$((errors+1))
      else
        # OK<TAB>verdict<TAB>total_tc<TAB>hang<TAB>write_landed<TAB>asst<TAB>model_id
        local verdict total_tc hang write_landed asst model_id
        verdict="$(printf '%s' "$out" | cut -f2)"
        total_tc="$(printf '%s' "$out" | cut -f3)"
        hang="$(printf '%s' "$out" | cut -f4)"
        write_landed="$(printf '%s' "$out" | cut -f5)"
        asst="$(printf '%s' "$out" | cut -f6)"
        model_id="$(printf '%s' "$out" | cut -f7)"
        last_model_id="$model_id"
        echo "   run $i: $verdict  tool_calls=$total_tc  hang_turns=$hang  write_landed=$write_landed  asst_turns=$asst  model_id=$model_id"
        case "$verdict" in
          RELIABLE)
            reliable=$((reliable+1))
            [[ "$write_landed" == "yes" ]] && any_write=1
            ;;
          HANGS)
            hangs=$((hangs+1))
            [[ "$write_landed" == "yes" ]] && any_write=1
            ;;
          PIN-FAILED)
            # The pinned model was unreachable and the run failed over to a
            # different model. No signal about the target — count as an
            # error, not a hang. The fallback's behaviour is irrelevant.
            pinfailed=$((pinfailed+1))
            echo "   WARNING: model_id=$model_id != pinned model=$model — target unreachable, failed over (NOT scored as a hang of $model)"
            ;;
        esac
      fi
      sleep "$INTER_RUN_PAUSE"
    done

    # PIN-FAILED runs fold into the error count — the pinned model could not
    # be reached, so the row is ERROR (unless some runs DID reach the target).
    errors=$((errors+pinfailed))
    local attempted=$((reliable+hangs))
    local row_verdict row_rate
    if [[ "$attempted" -eq 0 ]]; then
      row_verdict="ERROR"
      row_rate="0/0"
    elif [[ "$hangs" -gt 0 ]]; then
      row_verdict="HANGS"
      row_rate="$hangs/$attempted"
    else
      row_verdict="RELIABLE"
      row_rate="0/$attempted"
    fi
    local row_write="no"; [[ "$any_write" -eq 1 ]] && row_write="yes"
    [[ "$errors" -gt 0 && "$attempted" -gt 0 ]] && row_verdict="$row_verdict (+$errors err)"
    RESULT_ROWS+=("$spec|$row_verdict|$row_rate|$row_write|$last_model_id")
    echo "   => $spec: $row_verdict  hang_rate=$row_rate  any_write=$row_write"
    echo
  done

  echo "==================== RESULTS ===================="
  printf '%-30s %-18s %-10s %-12s %s\n' "MODEL" "VERDICT" "HANG-RATE" "WRITE-LAND" "ACTUAL-MODEL-ID"
  printf '%-30s %-18s %-10s %-12s %s\n' "------------------------------" "------------------" "----------" "------------" "---------------"
  local row
  for row in "${RESULT_ROWS[@]}"; do
    IFS='|' read -r r_model r_verdict r_rate r_write r_id <<< "$row"
    printf '%-30s %-18s %-10s %-12s %s\n' "$r_model" "$r_verdict" "$r_rate" "$r_write" "$r_id"
  done
  echo "================================================="
  echo
  echo "HANG-RATE = (hung runs)/(non-error runs). WRITE-LAND=yes means at"
  echo "least one run completed the final coordination_store write."
  echo "VERDICT HANGS = >=1 mid-loop narrate-without-act OR the final write"
  echo "never landed. ERROR = provider unreachable / no run completed."
}

main "$@"
