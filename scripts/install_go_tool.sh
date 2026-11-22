#!/usr/bin/env bash
# Usage: install_go_tool.sh <package@version> <binary-name> <final-path>
# Builds into a private scratch directory beside the final path, moves the result
# to a unique mktemp file in the same directory, then renames that over the final
# path, so neither an interrupt nor a failed build leaves a file make would treat
# as up to date or a temporary file behind. Honours $GOCMD (default go) and the
# caller's GOTOOLCHAIN.
set -euo pipefail
package=$1 name=$2 final=$3
dir=$(dirname -- "$final")
mkdir -p -- "$dir"
scratch='' staged='' child=''

cleanup() {
  if [[ -n $child ]]; then
    kill "$child" 2>/dev/null || true
    wait "$child" 2>/dev/null || true
  fi
  [[ -z $staged ]] || rm -f -- "$staged"
  [[ -z $scratch ]] || rm -rf -- "$scratch"
}
trap cleanup EXIT
for signal in INT TERM HUP; do
  # A trapped signal only runs once the foreground child ends, so go runs in the
  # background and is waited for; exiting here fires the EXIT trap exactly once.
  trap 'exit 1' "$signal"
done

scratch=$(mktemp -d "$dir/.install.XXXXXX")
staged=$(mktemp "$dir/.new.XXXXXX")
absolute=$(cd -- "$scratch" && pwd)
GOBIN=$absolute ${GOCMD:-go} install "$package" &
child=$!
status=0
wait "$child" || status=$?
child=''
[[ $status -eq 0 ]] || exit "$status"
mv -f -- "$scratch/$name" "$staged"
chmod 755 -- "$staged"
mv -f -- "$staged" "$final"
staged=''
