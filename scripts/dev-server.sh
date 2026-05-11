#!/bin/bash
# Starts the FlowState backend server for development.
# This script is used by npm run dev:full to start both backend and frontend.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$SCRIPT_DIR/.."
FLOWSTATE_BIN="$BACKEND_DIR/build/flowstate"
PORT="${PORT:-8080}"
HOST="${FLOWSTATE_HOST:-localhost}"

cd "$BACKEND_DIR"

echo "Building FlowState backend at $FLOWSTATE_BIN..."
mkdir -p "$BACKEND_DIR/build"
go build -o "$FLOWSTATE_BIN" ./cmd/flowstate

echo "Starting FlowState backend on $HOST:$PORT..."
exec "$FLOWSTATE_BIN" serve --host "$HOST" --port "$PORT"
