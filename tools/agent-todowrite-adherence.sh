#!/usr/bin/env bash
# D8: todowrite adherence metric for the May 2026 Agent Runtime Quality plan.
#
# Computes `todowrite_used_on_multistep_turn_ratio` from rotating event logs
# at ~/.cache/flowstate/events.jsonl* (and any rotated siblings such as
# events.jsonl.1, events.jsonl.2, ...).
#
# Definitions (per plan §D6, §D8):
#   - A *session* is multi-step if it emitted >=3 `tool` events (any tool).
#   - A session is *compliant* if it emitted >=1 `todowrite` tool event.
#   - Ratio = compliant multi-step sessions / total multi-step sessions.
#
# Output (one line, deterministic format):
#   total=<N> multistep=<M> compliant=<K> ratio=<R.RR>
#
# Optional flags:
#   --per-agent
#       Adds one extra line per agent observed in the logs:
#         agent=<agent_id> multistep=<M> compliant=<K> ratio=<R.RR>
#       Agent is derived from `tool.reasoning` events (`data.AgentID`)
#       — `tool` events themselves do not carry agent attribution. A
#       session's agent is the first non-empty AgentID observed for that
#       session_id; sessions that never emit `tool.reasoning` are kept in
#       the global total but skipped in the per-agent breakdown.
#
#   --logs <glob>
#       Override the default log glob. Default: ~/.cache/flowstate/events.jsonl*
#       Useful for tests against a synthetic temp dir.
#
#   --help
#       Print usage and exit 0.
#
# Required tools: bash, jq, awk, find, cat.
#
# Resilience:
#   - Missing log dir or zero matching files emits the canonical
#       total=0 multistep=0 compliant=0 ratio=0.00
#     and exits 0.
#   - Malformed JSON lines are skipped silently (jq's `?` operator).
#   - Idempotent: re-running on the same logs produces identical output.
#
# Wire format reference (verified against
# ~/.cache/flowstate/events.jsonl on 2026-05-20):
#   {"type":"tool", "data":{"session_id":"...","tool_name":"...",...}}
#   {"type":"tool.reasoning", "data":{"SessionID":"...","AgentID":"...","ToolName":"...",...}}
#
# Note the casing asymmetry: tool events use snake_case `session_id` /
# `tool_name`; tool.reasoning uses CamelCase `SessionID` / `AgentID` /
# `ToolName`. The script normalises both.

set -euo pipefail

LOGS_GLOB="${HOME}/.cache/flowstate/events.jsonl*"
PER_AGENT=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --per-agent)
            PER_AGENT=1
            shift
            ;;
        --logs)
            LOGS_GLOB="$2"
            shift 2
            ;;
        --help|-h)
            sed -n '2,40p' "$0"
            exit 0
            ;;
        *)
            printf 'unknown argument: %s\n' "$1" >&2
            exit 2
            ;;
    esac
done

if ! command -v jq >/dev/null 2>&1; then
    printf 'agent-todowrite-adherence: jq is required but not installed\n' >&2
    exit 2
fi

# Resolve glob to a real file list. shopt -s nullglob so an unmatched glob
# yields an empty array rather than the literal pattern.
shopt -s nullglob
# shellcheck disable=SC2206  # word-splitting the glob is intentional
LOG_FILES=( ${LOGS_GLOB} )
shopt -u nullglob

if [[ ${#LOG_FILES[@]} -eq 0 ]]; then
    printf 'total=0 multistep=0 compliant=0 ratio=0.00\n'
    exit 0
fi

# Stream every relevant event through jq once, projecting to a compact
# TSV: <session_id>\t<agent_id>\t<tool_name>\t<is_todowrite:0|1>
#
# We accept events of type "tool" (carries session_id+tool_name, no
# agent) and "tool.reasoning" (carries SessionID+AgentID+ToolName). For
# adherence counting we only need `tool`; for agent attribution we use
# `tool.reasoning` to map session -> agent.
project_events() {
    # Concatenate all log files; parse each line as JSON via
    # `fromjson?` so malformed lines are silently dropped (jq aborts
    # the whole stream on a hard parse error). The `-R` flag reads
    # raw lines; `fromjson?` is the per-line guard. This is the
    # canonical jq pattern for tolerating malformed JSONL.
    cat "${LOG_FILES[@]}" 2>/dev/null \
        | jq -r -R '
            fromjson? |
            select(.type == "tool" or .type == "tool.reasoning") |
            if .type == "tool" then
                [.type,
                 (.data.session_id // ""),
                 "",
                 (.data.tool_name // ""),
                 (if (.data.tool_name // "") == "todowrite" then "1" else "0" end)
                ] | @tsv
            else
                [.type,
                 (.data.SessionID // ""),
                 (.data.AgentID // ""),
                 (.data.ToolName // ""),
                 "0"
                ] | @tsv
            end
        ' 2>/dev/null
}

# Aggregate in awk. Per-session counters:
#   tool_count[sid]        -- number of `tool` events
#   has_todowrite[sid]     -- 1 if any `tool` event had tool_name=todowrite
#   agent[sid]             -- first non-empty agent seen via tool.reasoning
project_events | awk -F'\t' -v per_agent="${PER_AGENT}" '
    BEGIN {
        OFS = " "
    }
    {
        etype  = $1
        sid    = $2
        agent  = $3
        tool   = $4
        is_tw  = $5

        if (sid == "") next

        if (etype == "tool") {
            tool_count[sid]++
            if (is_tw == "1") has_todowrite[sid] = 1
        } else if (etype == "tool.reasoning") {
            # First non-empty agent for the session wins.
            if (agent != "" && !(sid in session_agent)) {
                session_agent[sid] = agent
            }
        }
    }
    END {
        total = 0
        multistep = 0
        compliant = 0

        # Per-agent maps (only populated when --per-agent)
        # agent_multistep[a], agent_compliant[a]

        for (sid in tool_count) {
            total++
            if (tool_count[sid] >= 3) {
                multistep++
                comp = (sid in has_todowrite) ? 1 : 0
                if (comp) compliant++

                if (per_agent) {
                    a = (sid in session_agent) ? session_agent[sid] : ""
                    if (a != "") {
                        agent_multistep[a]++
                        if (comp) agent_compliant[a]++
                    }
                }
            }
        }

        ratio = (multistep > 0) ? (compliant / multistep) : 0
        # Format ratio with 2 decimal places, always with a dot,
        # locale-independent (awk uses C locale by default with -v).
        printf "total=%d multistep=%d compliant=%d ratio=%.2f\n", \
            total, multistep, compliant, ratio

        if (per_agent) {
            # Deterministic ordering: sort agent keys ascending.
            n = 0
            for (a in agent_multistep) {
                n++
                agents[n] = a
            }
            # Simple insertion sort — agent counts are small in practice.
            for (i = 2; i <= n; i++) {
                key = agents[i]
                j = i - 1
                while (j >= 1 && agents[j] > key) {
                    agents[j+1] = agents[j]
                    j--
                }
                agents[j+1] = key
            }
            for (i = 1; i <= n; i++) {
                a = agents[i]
                am = agent_multistep[a]
                ac = (a in agent_compliant) ? agent_compliant[a] : 0
                ar = (am > 0) ? (ac / am) : 0
                printf "agent=%s multistep=%d compliant=%d ratio=%.2f\n", \
                    a, am, ac, ar
            }
        }
    }
'
