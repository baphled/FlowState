#!/usr/bin/env bash
# Bake 32k-context Modelfiles for FlowState local model usage.
# Usage: ./bake-32k-models.sh [--dry-run]
set -euo pipefail

DRY_RUN=false
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=true ;;
    *) echo "Unknown argument: $arg" >&2; exit 1 ;;
  esac
done

# ---------------------------------------------------------------------------
# Prerequisites check
# ---------------------------------------------------------------------------
if ! command -v ollama &>/dev/null; then
  echo "ERROR: 'ollama' is not installed or not in PATH." >&2
  echo "       Install from https://ollama.com and try again." >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# Models to bake: base-model -> 32k variant name
# ---------------------------------------------------------------------------
declare -A MODELS=(
  ["qwen3:8b"]="qwen3:8b-32k"
  ["qwen3:14b"]="qwen3:14b-32k"
  ["mistral:7b"]="mistral:7b-32k"
)

# ---------------------------------------------------------------------------
# Bake loop
# ---------------------------------------------------------------------------
for base in "${!MODELS[@]}"; do
  new_name="${MODELS[$base]}"

  if $DRY_RUN; then
    echo "[dry-run] Would create '${new_name}' from '${base}' with num_ctx 32768"
    continue
  fi

  # Pull base model if not already present
  if ! ollama list | grep -q "^${base}[[:space:]]"; then
    echo "Pulling base model '${base}' ..."
    ollama pull "${base}"
  fi

  # Skip if already baked
  if ollama list | grep -q "^${new_name}[[:space:]]"; then
    echo "skip '${new_name}' (already exists)"
    continue
  fi

  # Write a temporary Modelfile and bake
  modelfile=$(mktemp)
  printf 'FROM %s\nPARAMETER num_ctx 32768\n' "${base}" > "${modelfile}"

  echo "Baking '${new_name}' from '${base}' with num_ctx 32768 ..."
  ollama create "${new_name}" -f "${modelfile}"
  rm -f "${modelfile}"
  echo "Created '${new_name}'"
done

# ---------------------------------------------------------------------------
# Post-bake config snippet
# ---------------------------------------------------------------------------
cat <<'EOF'

# -------------------------------------------------------------------------
# Add to ~/.config/flowstate/config.yaml
# This maps agent complexity levels to local Ollama models with 32k context
# -------------------------------------------------------------------------
# providers:
#   default: "ollama"
#   ollama:
#     host: "http://localhost:11434"
#
# agent_models:
#   low: qwen3:8b-32k
#   standard: qwen3:14b-32k
#   deep: qwen3:14b-32k
#
# tool_capable_models:
#   - "qwen3:8b-32k"
#   - "qwen3:14b-32k"
#   - "mistral:7b-32k"
# -------------------------------------------------------------------------
EOF

if ! $DRY_RUN; then
  echo "Done. Run 'ollama list | grep -- \"-32k\"' to verify."
fi
