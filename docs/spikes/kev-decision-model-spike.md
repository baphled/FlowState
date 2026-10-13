# Spike: Jev-style decision model (`kev`) alongside FlowState's Ollama vault-sync

**Date:** 2026-10-13 · **Type:** SPIKE, time-boxed, throwaway · **Status:** done
**Rule honoured:** nothing wired into FlowState provider config or engine; no production code changes.

## What was tested

Whether a local "System One" / Jev-style decision model (`ollama.com/library/kev`) can run
alongside FlowState's existing `nomic-embed-text` vault-sync stack, what its decision output
actually looks like on real FlowState seams, and whether concurrent decision calls stall
vault-sync embeddings.

## Environment realities (important context)

- Host has **no working GPU**: `nvidia-smi` fails ("Unable to determine the device handle"),
  and a second Ollama instance logged `total_vram="0 B"` → inference is **100% CPU**,
  15 GB RAM (~6 GB available at test time). All numbers below are CPU numbers.
- The installed Ollama is **0.30.7**; every `kev:*` tag on ollama.com requires a newer
  Ollama (0.40.x). The spike therefore ran a **side-by-side Ollama 0.40.4-rc1 instance on
  port 11435** (`OLLAMA_MODELS` shared), leaving the production daemon on 11434 untouched.

## Model acquisition (finding #1: tag availability)

- `kev:0.8b-mxfp8` (the smallest tag) **cannot run on Linux/CPU**: after upgrading to
  0.40.4-rc1 the pull fails with *"this model requires MLX support, but the MLX runtime is
  not available"* — MLX tags are effectively Apple-Silicon-only for now.
- Working alternative: **`hf.co/ggml-org/Kev-4B-GGUF:Q4_K_M`** (3.0 GB GGUF, community
  conversion; same capability flag: `Capabilities: decision`, arch `qwen35`, 4.21B params).
  Caveat for requirements: this is a third-party GGUF conversion, not the publisher's build.

## API shape

New endpoint `POST /v1/systemone` with `{model, state, questions}`; questions are
`choice` (1–255 named options), `noul` (probability of true), `score` (1–10 ordered levels,
**criteria must be an array**, not an object — object form returns a validation error).
Responses include per-option probabilities and a confidence value, rounded to 4 dp
(confidence = concentration, not correctness — per the model card).

## Measurements

### Baseline vault sync (production daemon, port 11434)

- Warm `nomic-embed-text` single embed via `/api/embed`: **~31–37 ms** (cold first call ~215 ms).
- `flowstate vault sync` (2,515 files, 1–2 changed): **~16.5–21.8 s wall**, `indexed=N` chunks
  embedded; unchanged files are skipped cheaply (metadata pass dominates).

### kev co-residency

- Separate Ollama instance with `OLLAMA_MAX_LOADED_MODELS=2`; kev loaded showed
  **4.9 GB RAM** (100% CPU) in `ollama ps`. Because the two models live on *different daemons*
  here, "combined VRAM" is N/A (no VRAM); combined RAM footprint ≈ 0.4 GB (embed) + 4.9 GB (kev)
  on a 15 GB box — feasible but tight alongside Qdrant.
- On a GPU host, `OLLAMA_MAX_LOADED_MODELS=2` would be the right knob; on this CPU box the
  two-instance split incidentally proves isolation works and is a legitimate alternative
  (separate processes, separate ports, zero interference by construction).

### Decision-call results on real FlowState seams

All warm calls, `Kev-4B-GGUF:Q4_K_M`:

| Seam | Latency | Result |
|---|---|---|
| (a) Swarm routing — "review vault-sync code for races/SQL injection" | 0.6–2.5 s | `bug-hunt` @ p=0.80, conf 0.75 (`none` got 0.16 — sane) |
| (b) DD completeness gate (report missing invariants + test evidence) | 2.4 s | `noul` = **0.0396** — correctly low pass-probability, real probability not a label |
| (c) Plan quality score | 1.8 s | `score` = **2.53** on a 5-level array (slightly harsh; plausible but uncalibrated for our domain) |
| Ambiguous multi-intent task (README + CI + GPU) | 1.8 s | `devops` @ p=0.50 vs `writer` 0.21 — probabilities spread correctly under ambiguity |
| Long state (~2 KB repeated text) | **24.1 s** | correct answer (`devops` @ 0.69) but prompt-length prefill dominates on CPU |
| Simple 3-option routing (warm) | **0.61–0.68 s** | stable, deterministic-ish |

**Failure modes observed:**
1. `score` with criteria as an object → hard API validation error (must be an array of 2–10 strings).
2. Long states are O(prompt length): 24 s at ~2 KB on CPU — unusable in a hot path without a GPU.
3. Model card warns 0.8B variants get only 39/111 on hard JevBench tasks — small variants are
   weak; 4B is the pragmatic floor for routing/gating.
4. `none`-option mass (0.16–0.21) is non-trivial even on clean inputs — thresholds needed.

### Concurrency: decisions during vault sync

kev on port 11435, sync on port 11434, 3–5 sequential decision calls fired while
`flowstate vault sync` ran:

- Decision latency during sync: **~0.7–1.6 s** (vs 0.6–0.7 s idle warm) — mild inflation only.
- Sync wall time under concurrent decisions: **21.8 s** vs 16.5–16.7 s idle — ~+30%,
  consistent with CPU contention (both daemons share cores), **not queueing/stalling**.
  No embedding call appeared to stall or fail. Note: this is the *isolated-instance* result;
  a single shared daemon would serialize on `OLLAMA_NUM_PARALLEL=1` and behave worse.

## Do probabilities survive the build?

Yes. The GGUF build returns full float probabilities per option plus a separate confidence
value; `noul` returns a genuine probability (0.0396 for a failing DD report), not just argmax.
Calibration on FlowState-domain inputs is unverified (publisher says check on labelled
examples — we have none yet).

## Suggested requirements

1. **Model size floor:** 4B-class (0.8B is too weak per JevBench; and 0.8B Ollama tags are
   MLX-only, unusable on Linux CPU/GPU hosts). Budget ≈ 3 GB (Q4) RAM/VRAM + 1.6 GB scratch.
2. **Instance isolation:** run the decision model on a **separate Ollama instance/port** (or
   `OLLAMA_MAX_LOADED_MODELS=2` + separate keep-alive on GPU hosts) so embedding throughput
   for vault sync is never queued behind decision prefill. Isolation also pins Ollama version
   independently (decision API needs ≥ 0.40).
3. **Provider-interface needs:** a `DecisionProvider` surface distinct from chat/embed:
   `Choose(state, options) -> (choice, probabilities, confidence)`,
   `Judge(state, claim) -> p`, `Score(state, levels) -> score + legend`. Must handle the
   `/v1/systemone` schema (score criteria = array), model-capability detection, and
   `none`/threshold policy at the caller, not in the provider.
4. **Latency budget:** sub-second per decision only for short states (< ~1 KB) on CPU;
   require GPU or state-summarisation before state for longer inputs. Never call inline in
   the embedding path — decisions are strictly advisory/async today.
5. **Calibration gate requirement:** before any gate uses kev probabilities as pass/fail,
   build a small labelled set of FlowState DD reports/tasks and measure accuracy vs threshold;
   the publisher explicitly disclaims universal calibration.

## Throwaway artefacts

Side-by-side Ollama 0.40.4-rc1 in `/home/baphled/ollama-spike-tmp/` (daemon stopped after
the spike); pulled model lives in the shared `~/.ollama/models` and can be removed with
`ollama rm hf.co/ggml-org/Kev-4B-GGUF:Q4_K_M`. No repo files changed besides this report.
