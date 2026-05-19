# FlowState NPR Demo Quickstart

## Who this is for

This guide is for people setting up FlowState locally to evaluate the FS NPR onboarding proof of concept. It is a fallback path for local demos while hosted deployment decisions are still being made.

NPR here means Neuropsychographic Registry: the structured profile object produced from the onboarding conversation.

## What this installs

The demo bootstrap prepares a local FlowState service runtime for the NPR onboarding swarm. It:

- Builds the FlowState binary from this checkout when needed.
- Creates `~/.config/flowstate/config.yaml` only when it is missing.
- Starts Qdrant with the repo's existing `docker-compose.dev.yml`.
- Pulls the `nomic-embed-text` embedding model with Ollama.
- Materialises FlowState memory tools with `flowstate memory-tools install`.
- Checks swarm discovery and agent inventory with the commands available in the installed FlowState build.

It does not overwrite existing FlowState config, delete sessions, or install host tools such as Docker, Ollama, Go, Node.js, or Python.

## Prerequisites

- WSL2 or Linux.
- Docker with the Compose v2 plugin.
- Ollama running locally.
- Go and Make, unless a `flowstate` binary is already on `PATH`.
- Node.js and npm for the web UI.
- Python 3 with either `venv` support or `pipx` for LlamaIndex and Qdrant vault tooling.
- An Anthropic API key for the main orchestration provider.

On Ubuntu or Debian, the common system packages are:

```bash
sudo apt-get update
sudo apt-get install -y docker.io docker-compose-plugin golang-go make nodejs npm python3 python3-venv pipx curl
```

Install Ollama from <https://ollama.com/download/linux>, then start it before bootstrapping.

## Quickstart

1. Clone the repo if you do not already have it:

   ```bash
   git clone https://github.com/baphled/flowstate.git
   cd flowstate
   ```

2. Create a local demo environment file:

   ```bash
   cp .env.demo.example .env.demo
   nano .env.demo
   ```

   Add your Anthropic API key in `.env.demo`, or export it in your shell:

   ```bash
   export ANTHROPIC_API_KEY="sk-ant-..."
   ```

3. Bootstrap the local demo stack:

   ```bash
   make demo-bootstrap
   ```

   This starts Qdrant, pulls the embedding model, installs memory tools, and checks that the NPR swarm is discoverable.

4. Start the FlowState service:

   ```bash
   make demo-run
   ```

   The service defaults to `http://127.0.0.1:8080`.

5. In another terminal, start the web UI if browser access is needed:

   ```bash
   make web-install
   make web-dev
   ```

   The web UI defaults to the Vite URL shown by `make web-dev`.

6. Start the onboarding flow from the same FlowState chat/session context:

   ```text
   @npr-onboarding Start a new NPR onboarding for userId=example-user
   ```

## Which local profile is used

The normal demo commands use the machine's regular FlowState profile:

```text
~/.config/flowstate/config.yaml
~/.local/share/flowstate/sessions/
~/.local/share/flowstate/memory-tools/
~/.local/share/qdrant/
```

If FlowState has already been used on the machine, existing sessions may appear in the UI. That is expected. The bootstrap script leaves an existing `~/.config/flowstate/config.yaml` untouched and reuses the existing sessions directory.

To run the demo against an isolated temporary profile, export temporary XDG directories before both bootstrap and service startup:

```bash
export FLOWSTATE_DEMO_TEST_HOME="$(mktemp -d)"
export XDG_CONFIG_HOME="$FLOWSTATE_DEMO_TEST_HOME/config"
export XDG_DATA_HOME="$FLOWSTATE_DEMO_TEST_HOME/data"
export ANTHROPIC_API_KEY="sk-ant-..."
make demo-bootstrap
make demo-run
```

The browser UI talks to whichever FlowState service is running on `127.0.0.1:8080`, so it will show the sessions from that service's active profile.

To return to the normal profile, open a new terminal or run:

```bash
unset FLOWSTATE_DEMO_TEST_HOME XDG_CONFIG_HOME XDG_DATA_HOME
```

To remove only the temporary profile:

```bash
rm -rf "$FLOWSTATE_DEMO_TEST_HOME"
```

Do not delete `~/.local/share/qdrant/` unless you explicitly intend to discard local vector data. Do not use `docker compose down -v` for this demo stack.

## What Docker is used for

Docker is only used for Qdrant in this demo path. The bootstrap script starts:

```bash
docker compose -f docker-compose.dev.yml up -d qdrant
```

The compose file binds Qdrant to `127.0.0.1:6333` and persists data under the host Qdrant directory.

## What remains native

FlowState itself runs natively on the host or WSL environment. Ollama runs natively. The `nomic-embed-text` model is pulled into Ollama. Python tooling for LlamaIndex and Qdrant vault workflows also remains native rather than running inside the Qdrant container.

## Environment variables

Use `.env.demo.example` as the starting point:

```bash
cp .env.demo.example .env.demo
```

Required for orchestration:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

Useful demo defaults:

```bash
export QDRANT_URL="http://localhost:6333"
export QDRANT_COLLECTION="flowstate-recall"
export OLLAMA_HOST="http://localhost:11434"
export EMBEDDING_MODEL="nomic-embed-text"
```

Do not commit provider API keys.

## Running Qdrant

Start Qdrant:

```bash
make demo-qdrant-up
```

Check Qdrant:

```bash
make qdrant-status
```

Stop Qdrant when the demo is finished:

```bash
make qdrant-down
```

Stopping Qdrant does not delete the host data directory.

## Running FlowState service

After `make demo-bootstrap`, start the local service:

```bash
make demo-run
```

This starts FlowState as a service endpoint rather than focusing on the TUI as the long-term interface.

## Running the web UI

Install dependencies once:

```bash
make web-install
```

Start the web app:

```bash
make web-dev
```

Keep `make demo-run` running in another terminal so the web UI can reach the FlowState API.

## Troubleshooting

- `docker compose` is missing: install Docker Compose v2, then verify with `docker compose version`.
- `docker daemon` is missing: the Docker CLI is installed, but the Docker engine is not running.
  - On Windows/WSL: open Docker Desktop, then enable the distro under Settings -> Resources -> WSL Integration.
  - On native Linux: run `sudo systemctl start docker`, then check `docker info`.
  - If `docker info` says permission denied: run `sudo usermod -aG docker "$USER"`, then log out/in or run `newgrp docker`.
- Qdrant fails with `not a directory` or `Are you trying to mount a directory onto a file`: the host path Docker is trying to mount as a file is probably an empty directory from a failed first run. The bootstrap repairs an empty `~/.local/share/qdrant/config/config.yaml` directory automatically. If fixing manually, do not touch `storage` or `snapshots`; only replace that `config.yaml` directory with a file:

  ```bash
  rmdir ~/.local/share/qdrant/config/config.yaml
  mkdir -p ~/.local/share/qdrant/config
  printf '# FlowState demo Qdrant config.\n' > ~/.local/share/qdrant/config/config.yaml
  ```

- Qdrant setup fails with `Permission denied` under `~/.local/share/qdrant`: Docker probably created the directory with the wrong owner during a failed first run. Repair ownership, then rerun the bootstrap:

  ```bash
  sudo chown -R "$(id -u):$(id -g)" ~/.local/share/qdrant
  make demo-bootstrap
  ```

- `ollama pull nomic-embed-text` fails: start Ollama with `ollama serve` or the desktop app, then rerun `make demo-bootstrap`.
- Anthropic authentication fails: set `ANTHROPIC_API_KEY` or run the relevant `flowstate auth` command for the installed build.
- Memory commands are missing: rerun `flowstate memory-tools install`.
- Agent inventory differs by build: current builds expose `flowstate agent list`; some scripts or notes may refer to `flowstate agents list`.

## Known limitations

- This path is WSL/Linux first.
- It does not install Docker, Ollama, Go, Node, or Python for you.
- It does not overwrite an existing `~/.config/flowstate/config.yaml`.
- It assumes Anthropic is the primary orchestration provider for the PoC.
- It starts Qdrant locally, but it does not import or reindex a vault by itself.
