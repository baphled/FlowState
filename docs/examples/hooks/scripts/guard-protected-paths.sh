#!/bin/sh
# Gate hook for tool.before, matched to fs.write and fs.edit: denies a write under migrations/ and a write
# to a file that looks like a credential. The call does not run; the model sees only
# "error: denied by hook guard-protected-paths", and the reason stays in hook.result for the operator.
#
# Exit contract: 2 denies (stderr is the reason, at most 1 KiB); 0 allows; any other exit, a timeout or
# output that is not one JSON object is a hook failure, which on_failure decides: deny at tool.before
# unless the hook says "on_failure": "allow". A broken guard therefore fails closed.
#
# stdin is one JSON document (docs/guides/LIFECYCLE-HOOKS.md, "What a hook reads"). This script reads
# .tool.args.path, the target, relative to the session workdir.
# It needs jq on PATH: a hook inherits PATH and nothing else unless the hook lists names in "env".
input=$(cat) || exit $?
# The file tools decode their arguments with a decoder that matches keys case-insensitively, so {"PATH": ...} and
# {"Path": ...} name the target too: every key that is "path" in any case is checked, not only the exact one.
# The check is lexical: it does not follow symlinks.
paths=$(printf '%s' "$input" | jq -r '
  [.tool.args | objects | to_entries[] | select(.key | ascii_downcase == "path") | .value | strings
   | ltrimstr("./")] | .[]') || exit $?
set -f
IFS='
'
for path in $paths; do
  case "$path" in
    migrations/* | */migrations/*)
      echo "guard-protected-paths: $path is under migrations/, which the model may not write" >&2
      exit 2
      ;;
    *.pem | *.key | *.p12 | *.pfx | id_rsa* | */id_rsa* | id_ed25519* | */id_ed25519* \
      | .env | .env.* | */.env | */.env.*)
      echo "guard-protected-paths: $path looks like a credential file" >&2
      exit 2
      ;;
  esac
done
exit 0
