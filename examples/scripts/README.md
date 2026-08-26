# Example scripts

## `bake-32k-models.sh`

One-time setup that creates 32k-context variants of the Ollama models used by
the example swarms.

### Why

Ollama defaults `num_ctx` to 2048–4096 regardless of the model's native context
window. The swarm agents' `context_management.sliding_window_size: 10–15`
assumes the model can hold a much larger window. Without baking a 32k variant,
the model silently truncates and the swarm loses long-range context.

### Run

```bash
./examples/scripts/bake-32k-models.sh
```

The script is idempotent — already-baked models are skipped. Pass `--dry-run`
to print what would be done without executing:

```bash
./examples/scripts/bake-32k-models.sh --dry-run
```

If the base model is not yet present locally, the script will pull it
automatically via `ollama pull` before baking.

### Models baked

| Variant          | Based on      | `num_ctx` |
|------------------|---------------|-----------|
| `qwen3:8b-32k`   | `qwen3:8b`   | 32768     |
| `qwen3:14b-32k`  | `qwen3:14b`  | 32768     |
| `mistral:7b-32k` | `mistral:7b` | 32768     |

### Reference `config.yaml` block

After running the script, point your FlowState config at the `-32k` variants:

```yaml
providers:
  default: "ollama"
  ollama:
    host: "http://localhost:11434"

agent_models:
  low:      "qwen3:8b-32k"
  standard: "qwen3:14b-32k"
  deep:     "qwen3:14b-32k"

tool_capable_models:
  - "qwen3:8b-32k"
  - "qwen3:14b-32k"
  - "mistral:7b-32k"
```

### Verification

```bash
ollama list | grep -- "-32k"
# Expected: 3 models, all ending in -32k
#   qwen3:8b-32k
#   qwen3:14b-32k
#   mistral:7b-32k

# Smoke-test num_ctx is honoured:
curl -s http://localhost:11434/api/show -d '{"name":"qwen3:8b-32k"}' | grep -i num_ctx
# Expected: "num_ctx": 32768 (or similar; the 32768 must appear)
```

If `num_ctx` is missing from `/api/show`, send a tiny generate request and
inspect the server log — Ollama logs `num_ctx` per request when it loads the
model.
