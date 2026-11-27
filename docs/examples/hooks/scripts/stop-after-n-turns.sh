#!/bin/sh
# Stop hook for turn.after: asks the run to stop once the provider has finished N turns, N being the first
# argument (5 when absent). Exit 0 with {"stop":true} on stdout stops the run: a root run is paused with
# the session.paused reason hook_stop (exit 5, HTTP 409) and can be resumed; a delegated child ends failed
# with reason hook_stop. Exit 0 with nothing on stdout lets the run go on. Only turn.after accepts "stop":
# the same output at another point is a hook failure (field_not_permitted).
#
# Exit contract: 0, with {"stop":true} at or after the limit and nothing before it; 1 when .turn.index or
# the limit is not a whole number; anything else is a hook failure, which turn.after's default records and
# lets the run go on (on_failure: deny is refused at turn.after). No status is assumed: jq's is passed through
# (127 without jq) and the comparison's is branched on. So WITHOUT jq this turn cap does not stop anything: the
# failure is recorded in hook.result and the run goes on.
#
# stdin is one JSON document (docs/guides/LIFECYCLE-HOOKS.md, "What a hook reads"). This script reads
# .turn.index, this turn's number counting from 1; the final no-call turn is included.
# It needs jq on PATH: a hook inherits PATH and nothing else unless the hook lists names in "env".
input=$(cat) || exit $?
index=$(printf '%s' "$input" | jq -r '.turn.index') || exit $?
limit=${1:-5}
case "$index" in
  '' | *[!0-9]*) echo "stop-after-n-turns: .turn.index is not a whole number: $index" >&2; exit 1 ;;
esac
case "$limit" in
  *[!0-9]*) echo "stop-after-n-turns: the turn limit is not a whole number: $limit" >&2; exit 1 ;;
esac
status=0
[ "$index" -ge "$limit" ] || status=$?
case "$status" in
  0) printf '{"stop":true}\n' || exit $? ;;
  1) exit 0 ;;
  *) exit "$status" ;; # unreachable after the numeric guards: [ -ge ] returns 0 or 1
esac
