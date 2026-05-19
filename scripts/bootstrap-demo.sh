#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG_BASE="${XDG_CONFIG_HOME:-$HOME/.config}"
DATA_BASE="${XDG_DATA_HOME:-$HOME/.local/share}"
CONFIG_DIR="$CONFIG_BASE/flowstate"
CONFIG_FILE="$CONFIG_DIR/config.yaml"
DATA_DIR="$DATA_BASE/flowstate"
QDRANT_HOME="$HOME/.local/share/qdrant"
QDRANT_CONFIG_FILE="$QDRANT_HOME/config/config.yaml"
QDRANT_STORAGE_DIR="$QDRANT_HOME/storage"
QDRANT_SNAPSHOTS_DIR="$QDRANT_HOME/snapshots"
QDRANT_URL="${QDRANT_URL:-http://localhost:6333}"
QDRANT_COLLECTION="${QDRANT_COLLECTION:-flowstate-recall}"
OLLAMA_HOST="${OLLAMA_HOST:-http://localhost:11434}"
EMBEDDING_MODEL="${EMBEDDING_MODEL:-nomic-embed-text}"
FLOWSTATE_CMD="${FLOWSTATE_BIN:-}"
NEEDS_BUILD=0
CHECK_ONLY=0
SKIP_OLLAMA_PULL=0
WARNINGS=()
MISSING=()

usage() {
	cat <<'USAGE'
Usage: scripts/bootstrap-demo.sh [--check-only] [--skip-ollama-pull]

Bootstraps the local FS NPR FlowState demo path for WSL/Linux:
  - checks required host tools
  - creates ~/.config/flowstate/config.yaml only when missing
  - starts Qdrant with docker compose -f docker-compose.dev.yml up -d qdrant
  - runs ollama pull nomic-embed-text
  - runs flowstate memory-tools install
  - runs flowstate auth status, flowstate swarm list, and agent inventory

ANTHROPIC_API_KEY is required for the main orchestration provider.
USAGE
}

info() {
	printf '\n==> %s\n' "$*"
}

note() {
	printf '  %s\n' "$*"
}

warn() {
	WARNINGS+=("$*")
	printf '  Warning: %s\n' "$*"
}

missing() {
	MISSING+=("$1"$'\n'"$2")
}

has() {
	command -v "$1" >/dev/null 2>&1
}

parse_args() {
	while [[ $# -gt 0 ]]; do
		case "$1" in
			--check-only)
				CHECK_ONLY=1
				;;
			--skip-ollama-pull)
				SKIP_OLLAMA_PULL=1
				;;
			-h|--help)
				usage
				exit 0
				;;
			*)
				printf 'Unknown option: %s\n\n' "$1" >&2
				usage >&2
				exit 2
				;;
		esac
		shift
	done
}

check_docker() {
	if ! has docker; then
		missing "docker" "Install Docker Desktop with WSL integration, or install Docker Engine plus the Compose v2 plugin. Ubuntu/Debian: sudo apt-get install -y docker.io docker-compose-plugin"
		return
	fi
	if ! docker compose version >/dev/null 2>&1; then
		missing "docker compose" "Install the Docker Compose v2 plugin, then verify with: docker compose version"
		return
	fi
	local docker_info
	docker_info="$(docker info 2>&1 >/dev/null || true)"
	if [[ -n "$docker_info" ]]; then
		if [[ "$docker_info" == *"permission denied"* || "$docker_info" == *"Got permission denied"* ]]; then
			missing "docker permissions" "Your user cannot access the Docker daemon. On Linux run: sudo usermod -aG docker \"$USER\", then log out/in or run: newgrp docker"
			return
		fi
		missing "docker daemon" "Start Docker Desktop with WSL integration enabled, or start Docker on Linux with: sudo systemctl start docker"
	fi
}

check_ollama() {
	if ! has ollama; then
		missing "ollama" "Install Ollama from https://ollama.com/download/linux, start it, then rerun this script."
	fi
}

check_curl() {
	if ! has curl; then
		missing "curl" "Install curl. Ubuntu/Debian: sudo apt-get install -y curl"
	fi
}

check_python_tooling() {
	if ! has python3; then
		missing "python3" "Install Python 3. Ubuntu/Debian: sudo apt-get install -y python3 python3-venv pipx"
		return
	fi
	if python3 -m venv --help >/dev/null 2>&1 || has pipx; then
		return
	fi
	missing "python3 venv or pipx" "Install python3-venv or pipx for the LlamaIndex/Qdrant vault tooling."
}

check_node() {
	if ! has npm; then
		warn "npm was not found. Install Node.js and npm before running make web-install or make web-dev."
	fi
}

check_anthropic_key() {
	if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
		warn "ANTHROPIC_API_KEY is not set. Set it before the orchestration demo: export ANTHROPIC_API_KEY='sk-ant-...'"
	fi
}

check_flowstate() {
	if [[ -n "$FLOWSTATE_CMD" && -x "$FLOWSTATE_CMD" ]]; then
		return
	fi
	if has flowstate; then
		FLOWSTATE_CMD="$(command -v flowstate)"
		return
	fi
	if [[ -x "$REPO_ROOT/build/flowstate" ]]; then
		FLOWSTATE_CMD="$REPO_ROOT/build/flowstate"
		return
	fi
	if has make && has go; then
		FLOWSTATE_CMD="$REPO_ROOT/build/flowstate"
		NEEDS_BUILD=1
		return
	fi
	missing "flowstate binary" "Install FlowState on PATH or install Go and Make so this script can run make build."
}

print_missing_and_exit() {
	if [[ ${#MISSING[@]} -eq 0 ]]; then
		return
	fi
	printf '\nBootstrap cannot continue yet. Missing prerequisites:\n'
	for item in "${MISSING[@]}"; do
		printf '\n%s\n' "$item"
	done
	printf '\nAfter installing the missing prerequisites, rerun: make demo-bootstrap\n'
	exit 1
}

build_flowstate_if_needed() {
	if [[ "$NEEDS_BUILD" -eq 0 ]]; then
		return
	fi
	info "Building FlowState"
	(cd "$REPO_ROOT" && make build)
}

write_config_if_missing() {
	if [[ -f "$CONFIG_FILE" ]]; then
		info "FlowState config already exists"
		note "Leaving $CONFIG_FILE untouched."
		return
	fi
	info "Creating demo FlowState config"
	mkdir -p "$CONFIG_DIR" "$DATA_DIR"
	cat > "$CONFIG_FILE" <<CONFIG
providers:
  default: anthropic
  anthropic:
    model: claude-sonnet-4-20250514
  ollama:
    host: "$OLLAMA_HOST"
    model: llama3.2
qdrant:
  url: "$QDRANT_URL"
  collection: "$QDRANT_COLLECTION"
  api_key: ""
embedding_model: "$EMBEDDING_MODEL"
agent_dir: "$CONFIG_DIR/agents"
skill_dir: "$CONFIG_DIR/skills"
data_dir: "$DATA_DIR"
default_agent: default-assistant
log_level: info
CONFIG
	note "Wrote $CONFIG_FILE"
}

qdrant_permission_error() {
	local path="$1"
	printf '\nQdrant host path is not writable by %s:\n  %s\n' "${USER:-the current user}" "$path" >&2
	printf '\nThis often happens after Docker creates the path during a failed first run.\n' >&2
	printf 'Repair ownership, then rerun make demo-bootstrap:\n\n' >&2
	printf '  sudo chown -R "$(id -u):$(id -g)" "%s"\n\n' "$QDRANT_HOME" >&2
	printf 'The bootstrap will then replace an empty config.yaml directory with the config file Qdrant expects.\n' >&2
	exit 1
}

ensure_qdrant_host_paths() {
	info "Preparing Qdrant host paths"
	mkdir -p "$(dirname "$QDRANT_CONFIG_FILE")" "$QDRANT_STORAGE_DIR" "$QDRANT_SNAPSHOTS_DIR"
	if [[ ! -w "$(dirname "$QDRANT_CONFIG_FILE")" ]]; then
		qdrant_permission_error "$(dirname "$QDRANT_CONFIG_FILE")"
	fi
	if [[ -d "$QDRANT_CONFIG_FILE" ]]; then
		if [[ -n "$(find "$QDRANT_CONFIG_FILE" -mindepth 1 -print -quit)" ]]; then
			printf '\nQdrant config path is a directory and is not empty:\n  %s\n' "$QDRANT_CONFIG_FILE" >&2
			printf 'Move it aside or replace it with a file before rerunning make demo-bootstrap.\n' >&2
			exit 1
		fi
		if ! rmdir "$QDRANT_CONFIG_FILE"; then
			qdrant_permission_error "$QDRANT_CONFIG_FILE"
		fi
		note "Replaced empty directory at $QDRANT_CONFIG_FILE with a config file."
	fi
	if [[ ! -e "$QDRANT_CONFIG_FILE" ]]; then
		printf '# FlowState demo Qdrant config.\n' > "$QDRANT_CONFIG_FILE"
		note "Created $QDRANT_CONFIG_FILE"
	fi
	if [[ ! -f "$QDRANT_CONFIG_FILE" ]]; then
		printf '\nQdrant config path must be a file for docker-compose.dev.yml:\n  %s\n' "$QDRANT_CONFIG_FILE" >&2
		exit 1
	fi
}

start_qdrant() {
	info "Starting Qdrant"
	note "Running: docker compose -f docker-compose.dev.yml up -d qdrant"
	(cd "$REPO_ROOT" && docker compose -f docker-compose.dev.yml up -d qdrant)
}

pull_embedding_model() {
	if [[ "$SKIP_OLLAMA_PULL" -eq 1 ]]; then
		info "Skipping Ollama model pull"
		note "Run later: ollama pull nomic-embed-text"
		return
	fi
	info "Pulling Ollama embedding model"
	note "Running: ollama pull nomic-embed-text"
	if ! OLLAMA_HOST="$OLLAMA_HOST" ollama pull "$EMBEDDING_MODEL"; then
		printf '\nOllama could not pull %s. Start Ollama, then rerun:\n  ollama pull nomic-embed-text\n' "$EMBEDDING_MODEL" >&2
		exit 1
	fi
}

run_flowstate_required() {
	info "$1"
	shift
	note "Running: flowstate $*"
	if ! "$FLOWSTATE_CMD" "$@"; then
		printf '\nFlowState command failed: flowstate %s\n' "$*" >&2
		exit 1
	fi
}

run_flowstate_optional() {
	info "$1"
	shift
	note "Running: flowstate $*"
	if ! "$FLOWSTATE_CMD" "$@"; then
		warn "flowstate $* did not complete. Review the output above and rerun it manually."
	fi
}

subcommand_available() {
	local parent="$1"
	local child="$2"
	local help
	help="$("$FLOWSTATE_CMD" "$parent" --help 2>/dev/null || true)"
	printf '%s\n' "$help" | grep -Eq "^[[:space:]]+$child[[:space:]]"
}

run_auth_status() {
	info "Checking provider authentication"
	note "Requested demo check: flowstate auth status"
	if subcommand_available auth status; then
		note "Running: flowstate auth status"
		if ! "$FLOWSTATE_CMD" auth status; then
			warn "flowstate auth status did not complete. Verify ANTHROPIC_API_KEY or provider auth manually."
		fi
		return
	fi
	warn "this build does not expose flowstate auth status. Run flowstate auth --help and verify ANTHROPIC_API_KEY before the demo."
}

run_agent_inventory() {
	info "Checking agent inventory"
	note "Current builds use: flowstate agent list"
	note "Plural command requested for demo checks: flowstate agents list"
	if subcommand_available agents list; then
		note "Running: flowstate agents list"
		if ! "$FLOWSTATE_CMD" agents list; then
			warn "flowstate agents list did not complete."
		fi
		return
	fi
	if ! subcommand_available agent list; then
		warn "neither flowstate agents list nor flowstate agent list is available in this build."
		return
	fi
	note "Running: flowstate agent list"
	if ! "$FLOWSTATE_CMD" agent list; then
		warn "flowstate agent list did not complete."
	fi
}

print_summary() {
	printf '\n'
	if [[ ${#WARNINGS[@]} -gt 0 ]]; then
		printf 'Bootstrap completed with warnings:\n'
		for warning in "${WARNINGS[@]}"; do
			printf '  - %s\n' "$warning"
		done
		printf '\n'
	fi
	printf 'Success: FlowState demo bootstrap completed.\n'
	printf 'Next command: make demo-run\n'
	printf 'Then open another terminal and run: make web-install && make web-dev\n'
}

main() {
	parse_args "$@"
	info "Checking FlowState demo prerequisites"
	check_docker
	check_ollama
	check_curl
	check_python_tooling
	check_node
	check_anthropic_key
	check_flowstate
	print_missing_and_exit
	if [[ "$CHECK_ONLY" -eq 1 ]]; then
		printf '\nDemo prerequisite check completed.\n'
		exit 0
	fi
	build_flowstate_if_needed
	write_config_if_missing
	ensure_qdrant_host_paths
	start_qdrant
	pull_embedding_model
	run_flowstate_required "Installing FlowState memory tools" memory-tools install
	run_auth_status
	run_flowstate_optional "Checking swarms" swarm list
	run_agent_inventory
	print_summary
}

main "$@"
