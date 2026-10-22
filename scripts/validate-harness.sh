#!/usr/bin/env bash
# validate-harness.sh — local diagnostic for FlowState harness agents.
# Runs one prompt through one (or --all) agent, reports on the persisted
# session, flags the canonical "tool-JSON leaks into assistant Content
# while ToolCalls is empty" regression (session 1776611908809856897) and
# adjacent wiring smells. This is a REPORT, not a gate. Warnings never
# fail the script. Non-zero exit ONLY when the CLI or session I/O fails.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." &>/dev/null && pwd)"
PROMPT_DIR="$SCRIPT_DIR/harness-prompts"
FIXTURE_DIR="$PROMPT_DIR/fixtures"
AGENTS_DIR="$REPO_ROOT/internal/app/agents"

FLOWSTATE_BIN="${FLOWSTATE_BIN:-$REPO_ROOT/build/flowstate}"
if [[ ! -x "$FLOWSTATE_BIN" ]]; then
  if [[ -x "$REPO_ROOT/flowstate" ]]; then FLOWSTATE_BIN="$REPO_ROOT/flowstate"
  elif command -v flowstate &>/dev/null; then FLOWSTATE_BIN="$(command -v flowstate)"; fi
fi
FLOWSTATE_SESSIONS_DIR="${FLOWSTATE_SESSIONS_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/flowstate/sessions}"
FLOWSTATE_DATA_DIR="${FLOWSTATE_DATA_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/flowstate}"
COORD_STORE_PATH="$FLOWSTATE_DATA_DIR/coordination.json"

die() { echo "error: $*" >&2; exit 1; }

[[ -x "$FLOWSTATE_BIN" ]] || die "flowstate binary not found: $FLOWSTATE_BIN (run 'make build' or set FLOWSTATE_BIN)"
command -v jq &>/dev/null || die "jq is required but not installed"

# seed_coord_store_fixture merges a fixture JSON into the live coord-store
# so agent prompts that reference pre-seeded chains (e.g. plan-writer's
# "validate-harness-001/requirements") have stable inputs across runs. The
# merge is deterministic and idempotent: identical fixture keys overwrite
# the live values on every invocation, so two consecutive validator runs
# starting from the same fixture produce identical warning counts.
#
# The fixture format is the on-disk shape of FileStore (see
# internal/coordination/file_store.go:54): a flat JSON object mapping
# "<chainID>/<key>" to a string value.
seed_coord_store_fixture() {
  local fixture="$1"
  [[ -f "$fixture" ]] || return 0
  mkdir -p "$(dirname -- "$COORD_STORE_PATH")"
  local existing='{}'
  [[ -s "$COORD_STORE_PATH" ]] && existing="$(<"$COORD_STORE_PATH")"
  local merged
  merged="$(jq -s '.[0] * .[1]' <(printf '%s' "$existing") "$fixture")" \
    || die "failed to merge fixture $fixture into $COORD_STORE_PATH"
  printf '%s\n' "$merged" > "$COORD_STORE_PATH"
}

validate_one() {
  local agent="$1" prompt="$2" manifest_override="${3:-}"
  local manifest="${manifest_override:-$AGENTS_DIR/$agent.md}"
  local session_id="validate-$agent-$(date +%s)"

  echo "== harness-validate: $agent =="
  echo "session: $session_id"
  local preview=${prompt:0:80}
  printf 'prompt:  "%s%s"\n' "$preview" "$([[ ${#prompt} -gt 80 ]] && echo '...')"

  if ! "$FLOWSTATE_BIN" --sessions-dir "$FLOWSTATE_SESSIONS_DIR" \
        run --agent "$agent" --session "$session_id" --prompt "$prompt" >/dev/null 2>&1; then
    echo "ERROR: flowstate run exited non-zero for agent=$agent" >&2; return 2
  fi
  local session_file="$FLOWSTATE_SESSIONS_DIR/$session_id.json"
  [[ -f "$session_file" ]] || { echo "ERROR: session file not found at $session_file" >&2; return 3; }

  local asst user tool tool_use_count
  asst=$(jq '[.messages[]|select(.message.Role=="assistant")]|length' "$session_file")
  user=$(jq '[.messages[]|select(.message.Role=="user")]|length' "$session_file")
  tool=$(jq '[.messages[]|select(.message.Role=="tool")]|length' "$session_file")
  tool_use_count=$(jq '[.messages[].message.ToolCalls // []|.[]?]|length' "$session_file")
  echo "turns:   assistant=$asst  user=$user  tool=$tool"

  echo "tool_calls (by name):"
  jq -r '[.messages[].message.ToolCalls // []|.[]?.Name // ""|select(length>0)]
         |group_by(.)|map({n:.[0],c:length})|.[]|"  \(.n)  x\(.c)"' "$session_file"
  local unnamed_tc
  unnamed_tc=$(jq '[.messages[].message.ToolCalls // []|.[]?|select((.Name // "")=="")]|length' "$session_file")
  [[ "$unnamed_tc" -gt 0 ]] && echo "WARNING: $unnamed_tc tool_call(s) with empty Name — provider may have emitted unnamed tool_use blocks"

  jq -r '[.messages[]|select(.message.Role=="assistant")|(.message.Content // ""|length)]
         |if length==0 then "content lengths (assistant): (none)"
          else "content lengths (assistant): avg=\((add/length)|floor)  max=\(max)  min=\(min)" end' "$session_file"

  # H1: tool-JSON leak — assistant turn with ToolCalls empty AND Content matching tool-call JSON fragments.
  while IFS=$'\t' read -r idx sample; do
    [[ -z "$idx" ]] && continue
    echo "WARNING: assistant turn $idx content contains tool-JSON substring (possible tool-JSON leak): ${sample:0:80}"
  done < <(jq -r '
    [.messages[]|select(.message.Role=="assistant")]|to_entries[]
    |select(((.value.message.ToolCalls // [])|length)==0)
    |select((.value.message.Content // "")|test("\"tool_use\"|\"tool_call\"|\"name\"\\s*:\\s*\"[^\"]+\"\\s*,\\s*\"arguments\"";"i"))
    |"\(.key+1)\t\(.value.message.Content // "")"' "$session_file")

  # H2: manifest declares tools but 0 tool_use invocations.
  local manifest_has_tools=0
  if [[ -f "$manifest" ]]; then
    if awk '/^capabilities:/,/^[a-zA-Z]/' "$manifest" | grep -qE '^\s{2,}tools:' \
       || grep -qE '^##+\s+Tools' "$manifest"; then manifest_has_tools=1; fi
  fi
  [[ "$manifest_has_tools" == "1" && "$tool_use_count" == "0" ]] && \
    echo "WARNING: manifest declares tools but 0 tool_use invocations — tools may not be registered"

  # H3: manifest always_active_skills missing from session loaded_skills.
  if [[ -f "$manifest" ]]; then
    local decl loaded missing
    decl=$(awk '
      /^\s*always_active_skills:/ { inblk=1; indent=match($0,/[^ ]/); next }
      inblk {
        if ($0 ~ /^\s*-\s/) { sub(/^\s*-\s*/,""); print; next }
        if (match($0,/[^ ]/) && RSTART <= indent) { inblk=0 }
      }' "$manifest" | sort -u | paste -sd, -)
    loaded=$(jq -r '.loaded_skills // []|join(",")' "$session_file")
    if [[ -n "$decl" ]]; then
      missing=""
      IFS=',' read -ra D <<< "$decl"
      for s in "${D[@]}"; do [[ -n "$s" && ",$loaded," != *",$s,"* ]] && missing+="$s,"; done
      [[ -n "$missing" ]] && echo "WARNING: manifest always_active_skills not loaded in session: ${missing%,}"
    fi
  fi

  # H4: empty assistant turns (no Content, no ToolCalls).
  local empty_turns
  empty_turns=$(jq '[.messages[]|select(.message.Role=="assistant")|select(((.message.Content // "")|length)==0 and ((.message.ToolCalls // [])|length)==0)]|length' "$session_file")
  [[ "$empty_turns" -gt 0 ]] && echo "WARNING: $empty_turns empty assistant turn(s) (no Content, no ToolCalls)"

  # H5: single-turn termination for orchestration agents.
  case "$agent" in
    planner|executor|tech-lead) [[ "$asst" == "1" ]] && \
      echo "WARNING: single assistant turn only — prompt may not have exercised the tool_use path" ;;
  esac

  echo "loaded_skills: [$(jq -r '.loaded_skills // []|join(", ")' "$session_file")]"
  echo "agent_id (meta): $(jq -r '.agent_id // "?"' "$session_file")"
  echo
}

# resolve_agent_name maps a user-supplied agent name to the canonical
# manifest basename on disk. Lookup order:
#   1. literal as supplied (e.g. "planner", "Tech-Lead").
#   2. PascalCase variant of a kebab-cased input (e.g. "code-reviewer"
#      -> "Code-Reviewer") for users who typed the lowercase form.
#   3. fully lowercase variant (e.g. "Planner" -> "planner") for users
#      who capitalised a kebab-cased planner-loop manifest.
#
# Prints the resolved basename (without ".md") on stdout, exits non-zero
# when no variant exists. The default --all loop is unaffected: it
# iterates a literal lowercase list and never calls this function.
resolve_agent_name() {
  local input="$1"
  [[ -f "$AGENTS_DIR/$input.md" ]] && { printf '%s' "$input"; return 0; }

  # PascalCase: capitalise each kebab segment.
  local pascal
  pascal="$(printf '%s' "$input" | awk -F'-' '{ for (i=1; i<=NF; i++) { $i = toupper(substr($i,1,1)) tolower(substr($i,2)) } } 1' OFS='-')"
  [[ -f "$AGENTS_DIR/$pascal.md" ]] && { printf '%s' "$pascal"; return 0; }

  # Lowercase fallback.
  local lower
  lower="$(printf '%s' "$input" | tr '[:upper:]' '[:lower:]')"
  [[ -f "$AGENTS_DIR/$lower.md" ]] && { printf '%s' "$lower"; return 0; }

  return 1
}

# resolve_prompt_file mirrors resolve_agent_name for the prompt fixture
# lookup. Prints the resolved path on stdout, prints nothing and returns
# 0 when no fixture exists (the caller logs the skip line).
resolve_prompt_file() {
  local agent="$1"
  local literal="$PROMPT_DIR/$agent.txt"
  [[ -f "$literal" ]] && { printf '%s' "$literal"; return 0; }

  local lower
  lower="$(printf '%s' "$agent" | tr '[:upper:]' '[:lower:]')"
  local lower_pf="$PROMPT_DIR/$lower.txt"
  [[ -f "$lower_pf" ]] && { printf '%s' "$lower_pf"; return 0; }

  return 0
}

main() {
  # --agent-file <path>: override the manifest read by H1/H2/H3 checks for
  # the planner agent (or whichever agent matches). The live flowstate run
  # in validate_one still uses the real agent name; only the scoring checks
  # read the candidate file. Designed for use by autoresearch evaluators
  # that score candidate manifests without touching the live file.
  local agent_file_override=""
  while [[ $# -gt 0 ]]; do
    case "${1:-}" in
      --agent-file)
        [[ $# -ge 2 ]] || die "--agent-file requires a path argument"
        agent_file_override="$2"
        [[ -f "$agent_file_override" ]] || die "--agent-file path not found: $agent_file_override"
        shift 2
        ;;
      *) break ;;
    esac
  done

  # --score: emit a single integer scalar (sum of WARNING: lines across
  # the resolved agents) on stdout, nothing else. Designed for use as
  # the autoresearch evaluator's metric source — see plan § 5.4.
  local score_mode=0
  if [[ "${1:-}" == "--score" ]]; then
    score_mode=1
    shift
  fi

  # Seed the canonical validate-harness-001 chain before any agent runs so
  # plan-writer/plan-reviewer prompts find their expected coord-store keys.
  # Idempotent: re-running the validator never drifts the seeded values.
  seed_coord_store_fixture "$FIXTURE_DIR/validate-harness-001.json"

  local agents=()
  local all_mode=0
  if [[ "${1:-}" == "--all" ]]; then
    all_mode=1
    for a in planner plan-writer plan-reviewer executor tech-lead; do
      if [[ -f "$AGENTS_DIR/$a.md" ]]; then agents+=("$a")
      else
        # Score mode keeps stdout clean: skip lines route to stderr.
        if [[ "$score_mode" == "1" ]]; then
          echo "skip: $a (manifest not found at $AGENTS_DIR/$a.md)" >&2
        else
          echo "skip: $a (manifest not found at $AGENTS_DIR/$a.md)"
        fi
      fi
    done
  else
    [[ $# -ge 1 ]] || die "usage: $0 [--agent-file <path>] [--score] <agent> [prompt] | [--agent-file <path>] [--score] --all"
    local resolved
    resolved="$(resolve_agent_name "$1")" \
      || die "manifest not found for agent '$1' (looked under $AGENTS_DIR with literal, PascalCase, and lowercase variants)"
    agents=("$resolved")
  fi

  local rc=0
  local total_warnings=0
  for agent in "${agents[@]}"; do
    local prompt
    if [[ "${1:-}" != "--all" && -n "${2:-}" ]]; then
      prompt="$2"
    else
      local pf
      pf="$(resolve_prompt_file "$agent")"
      if [[ -z "$pf" ]]; then
        if [[ "$score_mode" == "1" ]]; then
          echo "skip: $agent (no default prompt at $PROMPT_DIR/$agent.txt)" >&2
        else
          echo "skip: $agent (no default prompt at $PROMPT_DIR/$agent.txt)"
        fi
        continue
      fi
      prompt="$(<"$pf")"
    fi

    # Resolve manifest override: apply only to the planner agent in --all
    # mode (the candidate file represents the planner surface); always apply
    # in single-agent mode so a caller can score any agent's candidate.
    local this_override=""
    if [[ -n "$agent_file_override" ]]; then
      if [[ "${all_mode:-0}" == "1" ]]; then
        [[ "$agent" == "planner" ]] && this_override="$agent_file_override"
      else
        this_override="$agent_file_override"
      fi
    fi

    if [[ "$score_mode" == "1" ]]; then
      # Capture validate_one stdout, route human-readable noise to stderr,
      # count WARNING: lines, accumulate. validate_one's own non-zero
      # exit propagates so CLI/session I/O failures still trip the rc.
      local captured
      captured="$(validate_one "$agent" "$prompt" "$this_override")" || rc=$?
      local n
      n="$(printf '%s\n' "$captured" | grep -c '^WARNING:' || true)"
      total_warnings=$((total_warnings + n))
      printf '%s\n' "$captured" >&2
    else
      validate_one "$agent" "$prompt" "$this_override" || rc=$?
    fi
  done

  if [[ "$score_mode" == "1" ]]; then
    printf '%d\n' "$total_warnings"
  fi
  exit "$rc"
}

main "$@"
