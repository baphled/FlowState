#!/bin/sh
# Logger hook for session.start and session.end: appends the point, the session and the run to
# $HOME/flowstate-session.log. One script serves both points: hooks.json gives each point its own entry
# and id. session.start fires once per root run before the first provider request; session.end fires once
# the run's terminal or paused state is written. Neither can deny: a failure is recorded and the run goes
# on (their default is "on_failure": "allow").
#
# Exit contract: 0 with nothing on stdout. Anything else is a recorded failure that changes nothing.
#
# stdin is one JSON document (docs/guides/LIFECYCLE-HOOKS.md, "What a hook reads"). This script reads:
#   .point, .session_id, .run_id
# HOME arrives only because the hook lists it in "env".
input=$(cat) || exit $?
line=$(printf '%s' "$input" | jq -r '[.point, "session=" + .session_id, "run=" + .run_id] | join(" ")') || exit $?
if [ -z "${HOME:-}" ]; then
  echo "log-session: HOME is not set; list it in the hook's env" >&2
  exit 1
fi
printf '%s\n' "$line" >> "$HOME/flowstate-session.log" || exit $?
exit 0
