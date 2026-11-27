#!/bin/sh
# Observer hook for tool.after, every tool: appends one line per executed call to
# $HOME/flowstate-tool-audit.log. It cannot change the result, and a failure here (HOME unset, the file
# not writable) is recorded in hook.result and the call's result is sent as it was: tool.after has nothing
# to deny, so its default is "on_failure": "allow".
#
# Exit contract: 0 with nothing on stdout. Anything else is a recorded failure that changes nothing.
#
# stdin is one JSON document (docs/guides/LIFECYCLE-HOOKS.md, "What a hook reads"). This script reads:
#   .tool.name          the tool name
#   .tool.call_id       the id the model's call and the result share
#   .tool.args.path     the target of an fs.* call, "-" for a tool without one
#   .tool.result.ok     true when the tool returned a result, false when it returned an error
#   .session_id, .run_id
# HOME arrives only because the hook lists it in "env".
input=$(cat) || exit $?
# Every value is model-influenced (the path, the call id): control, format and line-separator characters
# are mapped to "?" so a value cannot start a forged line in the log.
line=$(printf '%s' "$input" | jq -r '
  ["tool=" + .tool.name, "call=" + .tool.call_id, "ok=" + (.tool.result.ok | tostring),
   "session=" + .session_id, "run=" + .run_id, "path=" + (.tool.args.path // "-")]
  | map(tostring | gsub("[\\x00-\\x1f\\x7f\\p{Cf}\\p{Zl}\\p{Zp}]"; "?")) | join(" ")') || exit $?
if [ -z "${HOME:-}" ]; then
  echo "audit-tool-calls: HOME is not set; list it in the hook's env" >&2
  exit 1
fi
printf '%s\n' "$line" >> "$HOME/flowstate-tool-audit.log" || exit $?
exit 0
