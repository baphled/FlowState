#!/bin/bash
# Starts the FlowState backend server for development.
# This script is used by npm run dev:full to start both backend and frontend.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$SCRIPT_DIR/.."
FLOWSTATE_BIN="$BACKEND_DIR/flowstate"
PORT="${PORT:-8080}"

echo "Starting FlowState backend on localhost:$PORT..."

cd "$BACKEND_DIR"
exec "$FLOWSTATE_BIN" serve --port "$PORT"
