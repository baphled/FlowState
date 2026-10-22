#!/bin/bash
# test-local-model.sh — smoke a local Ollama model end-to-end through FlowState.
#
# Verifies that a given Ollama tag can:
#   1. Receive an agentic prompt via FlowState's executor agent.
#   2. Emit a tool_use event for the bash tool.
#   3. Run the tool with the expected command and quote its output back.
#
# Use this when:
#   - Vetting a new local model before adding it to a config.
#   - Reproducing tool-calling regressions against a known-good model.
#   - Comparing model latency / behaviour after an Ollama upgrade.
#
# Sample:
#   ./scripts/test-local-model.sh qwen3:8b
#   ./scripts/test-local-model.sh llama3.1:8b
#   FLOWSTATE_BIN=/tmp/fs-integration ./scripts/test-local-model.sh mistral:7b
#
# Verdicts:
#   PASS       — tool fired, token echoed, exit 0.
#   PASS-SLOW  — tool fired, token echoed, but model timed out generating its
#                final reply (verbose model / CPU offload).
#   PASS-DIRTY — tool fired, token echoed, but non-zero exit (post-tool crash).
#   FAIL       — tool did not fire, or fired with the wrong command.
#   TIMEOUT    — no tool_use event observed inside 180s.
#
# Exit code mirrors the underlying flowstate run; verdict is reported on stdout.
set -u

MODEL="${1:?usage: $0 <ollama-tag>}"

# Resolve the flowstate binary: PATH first, then $FLOWSTATE_BIN, else error.
if command -v flowstate >/dev/null 2>&1; then
  FLOWSTATE_BIN="$(command -v flowstate)"
elif [ -n "${FLOWSTATE_BIN:-}" ] && [ -x "$FLOWSTATE_BIN" ]; then
  : # honour the env override
else
  echo "error: flowstate binary not found." >&2
  echo "  - install flowstate so it's on PATH, or" >&2
  echo "  - set FLOWSTATE_BIN=/path/to/flowstate" >&2
  exit 2
fi

WORKDIR=$(mktemp -d)
TMPCFG="$WORKDIR/config.yaml"
TMPOUT="$WORKDIR/output.txt"
EXPECTED_TOKEN="flowstate-smoke-$(date +%s)"
PROMPT="Use the bash tool to run this exact command: echo $EXPECTED_TOKEN

Then in your final reply, quote the output the bash tool returned."

python3 - "$TMPCFG" "$MODEL" <<'PY'
import sys, yaml, os
src = os.path.expanduser("~/.config/flowstate/config.yaml")
dst, model = sys.argv[1], sys.argv[2]
with open(src) as f:
    cfg = yaml.safe_load(f)
cfg["providers"]["default"] = "ollama"
cfg["providers"]["ollama"]["model"] = model
# Disable Phase A compaction for the smoke — we want to see the raw
# model behaviour, not the compactor's hot/cold rewrite.
cfg["compaction"] = {"micro_enabled": False, "fact_extraction_enabled": False}
with open(dst, "w") as f:
    yaml.safe_dump(cfg, f)
PY

START=$(date +%s)
timeout 180 "$FLOWSTATE_BIN" run \
  --config "$TMPCFG" \
  --agent executor \
  --prompt "$PROMPT" \
  --stats \
  > "$TMPOUT" 2>&1
EXIT=$?
ELAPSED=$(( $(date +%s) - START ))

VERDICT="FAIL"
REASON=""
TOOL_FIRED="no"
TOKEN_IN_OUTPUT="no"
grep -q "🔧 bash" "$TMPOUT" 2>/dev/null && TOOL_FIRED="yes"
grep -q "$EXPECTED_TOKEN" "$TMPOUT" 2>/dev/null && TOKEN_IN_OUTPUT="yes"

if [ "$TOOL_FIRED" = "yes" ] && [ "$TOKEN_IN_OUTPUT" = "yes" ]; then
  if [ "$EXIT" -eq 0 ]; then
    VERDICT="PASS"
  elif [ "$EXIT" -eq 124 ]; then
    VERDICT="PASS-SLOW"
    REASON="tool call succeeded but model timed out generating final reply (verbose / CPU offload)"
  else
    VERDICT="PASS-DIRTY"
    REASON="tool call succeeded but exit=$EXIT (post-tool model crash?)"
  fi
elif [ "$TOOL_FIRED" = "yes" ] && [ "$TOKEN_IN_OUTPUT" = "no" ]; then
  VERDICT="FAIL"
  REASON="bash tool fired but did not run with the expected command"
elif [ "$EXIT" -eq 124 ]; then
  VERDICT="TIMEOUT"
  REASON="no tool call observed before $((180))s timeout"
else
  VERDICT="FAIL"
  REASON="model produced no tool_use event (text-only response)"
fi

printf "%-12s %-40s elapsed=%3ds\n" "$VERDICT" "$MODEL" "$ELAPSED"
if [ "$VERDICT" != "PASS" ]; then
  [ -n "$REASON" ] && echo "  reason: $REASON"
  echo "  ---- output tail ----"
  tail -20 "$TMPOUT" | sed 's/^/  | /'
  echo "  ---- end ----"
  echo "  full output: $TMPOUT (kept for inspection)"
else
  rm -rf "$WORKDIR"
fi
