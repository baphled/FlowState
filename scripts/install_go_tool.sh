#!/bin/sh
# Usage: install_go_tool.sh <package@version> <binary-name> <final-path>
# Installs into a scratch directory beside the final path and renames last, so
# an interrupted install never leaves a file make would treat as up to date.
# Honours $GOCMD (default go) and the caller's GOTOOLCHAIN.
set -eu
package=$1 name=$2 final=$3
dir=$(dirname -- "$final")
mkdir -p -- "$dir"
scratch=$(mktemp -d "$dir/.install.XXXXXX")
trap 'rm -rf -- "$scratch"' EXIT HUP INT TERM
absolute=$(cd -- "$scratch" && pwd)
GOBIN=$absolute ${GOCMD:-go} install "$package"
mv -- "$scratch/$name" "$dir/.new.$$"
mv -- "$dir/.new.$$" "$final"
