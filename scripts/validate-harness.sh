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
AGENTS_DIR="$REPO_ROOT/internal/app/agents"

FLOWSTATE_BIN="${FLOWSTATE_BIN:-$REPO_ROOT/flowstate}"
if [[ ! -x "$FLOWSTATE_BIN" ]]; then
  if [[ -x "$REPO_ROOT/build/flowstate" ]]; then FLOWSTATE_BIN="$REPO_ROOT/build/flowstate"
  elif command -v flowstate &>/dev/null; then FLOWSTATE_BIN="$(command -v flowstate)"; fi
fi
FLOWSTATE_SESSIONS_DIR="${FLOWSTATE_SESSIONS_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/flowstate/sessions}"

die() { echo "error: $*" >&2; exit 1; }

[[ -x "$FLOWSTATE_BIN" ]] || die "flowstate binary not found: $FLOWSTATE_BIN (run 'make build' or set FLOWSTATE_BIN)"
[[ -n "${ZAI_API_KEY:-}" ]] || die "ZAI_API_KEY is unset — export it before running (this harness pins to z.ai)"
command -v jq &>/dev/null || die "jq is required but not installed"

validate_one() {
  local agent="$1" prompt="$2"
  local manifest="$AGENTS_DIR/$agent.md"
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

main() {
  local agents=()
  if [[ "${1:-}" == "--all" ]]; then
    for a in planner plan-writer plan-reviewer executor tech-lead; do
      if [[ -f "$AGENTS_DIR/$a.md" ]]; then agents+=("$a")
      else echo "skip: $a (manifest not found at $AGENTS_DIR/$a.md)"; fi
    done
  else
    [[ $# -ge 1 ]] || die "usage: $0 <agent> [prompt] | --all"
    agents=("$1")
  fi

  local rc=0
  for agent in "${agents[@]}"; do
    local prompt
    if [[ "${1:-}" != "--all" && -n "${2:-}" ]]; then
      prompt="$2"
    else
      local pf="$PROMPT_DIR/$agent.txt"
      [[ -f "$pf" ]] || { echo "skip: $agent (no default prompt at $pf)"; continue; }
      prompt="$(<"$pf")"
    fi
    validate_one "$agent" "$prompt" || rc=$?
  done
  exit "$rc"
}

main "$@"
