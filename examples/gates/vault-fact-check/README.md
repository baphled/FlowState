# vault-fact-check — reference v0 ext gate

Scores a swarm member's claim against the operator's Obsidian vault via Qdrant + Ollama. Returns `pass:true` when the best similarity meets the threshold.

## Install

Symlink (or copy) into your gates_dir:

```bash
ln -s "$(pwd)/examples/gates/vault-fact-check" ~/.config/flowstate/gates/vault-fact-check
```

Or override `gates_dir` in your `config.yaml` to point at `examples/gates/` directly.

## Run requirements

- Python 3.9+ (uses stdlib only — no `pip install`)
- A reachable `flowstate-vault-server` (or any Qdrant collection populated with `nomic-embed-text` vectors)
- Ollama serving `nomic-embed-text`

## Configuration

Per-environment via env vars:

- `QDRANT_URL` (default `http://localhost:6333`)
- `QDRANT_COLLECTION` (default `flowstate-vault`)
- `OLLAMA_HOST` (default `http://localhost:11434`)
- `EMBEDDING_MODEL` (default `nomic-embed-text`)

Per-dispatch via the swarm manifest's `policy:` block:

- `threshold` (float, default 0.65) — similarity floor for `pass:true`
- `top_k` (int, default 3) — evidence entries returned

## Manifest

See `manifest.yml` in this directory.

## Verifying

After installing, run the in-tree smoke from the repo root:

```bash
go run ./tools/smoke/ext_gate_subprocess
```

The smoke runs a fixture gate end-to-end (NOT this Python gate); for the Python gate specifically, hit it directly:

```bash
echo '{"kind":"vault-fact-check","payload":"FlowState is a Go-based AI assistant TUI."}' | ./gate.py
```
