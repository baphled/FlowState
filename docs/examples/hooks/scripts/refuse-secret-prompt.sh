#!/bin/sh
# Refusal hook for user.prompt: denies the run when the task text holds what looks like a credential, so a
# pasted key never reaches a provider. Exit 2 refuses the run before any provider request: the command ends
# with the code hook_refused (exit 2, HTTP 422), the session's status is unchanged, and the log holds
# hooks.loaded, hook.invoked and hook.result and nothing else. The reason on stderr is for the operator; no
# model ever reads it. Exit 0 lets the run start.
#
# Exit contract: 2 denies (stderr is the reason); 0 allows; any other exit is a hook failure, which
# on_failure decides, and user.prompt denies by default. grep's own status decides which, and is never
# assumed: 0 denies, 1 allows, anything else is this script's exit too, so a grep that is missing (127) or
# fails never becomes an allow.
#
# stdin is one JSON document (docs/guides/LIFECYCLE-HOOKS.md, "What a hook reads"). This script reads
# .prompt, the run's task, and matches the patterns on the raw document: a key is never JSON-escaped, and
# matching the raw bytes cannot be fooled by a quoting mistake. Extend the patterns to your credentials.
# A run this hook refuses every time is left with `flowstate session cancel <id>`.
# It needs grep on PATH: a hook inherits PATH and nothing else unless the hook lists names in "env".
input=$(cat) || exit $?
status=0
printf '%s' "$input" \
  | grep -qE 'AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----|gh[pousr]_[A-Za-z0-9]{36,}' || status=$?
case "$status" in
  0)
    echo "refuse-secret-prompt: the task holds a credential pattern (an access key id, a private key or a token)" >&2
    exit 2
    ;;
  1) exit 0 ;;
  *)
    echo "refuse-secret-prompt: grep exited $status, so the task could not be checked" >&2
    exit "$status"
    ;;
esac
