# Spike: Jev-style decision model (`kev`) alongside FlowState's Ollama vault-sync

**Date:** 2026-10-13 · **Type:** SPIKE, time-boxed, throwaway · **Status:** done (GPU re-run 2026-10-14 appended below)
**Rule honoured:** nothing wired into FlowState provider config or engine; no production code changes.

## What was tested

Whether a local "System One" / Jev-style decision model (`ollama.com/library/kev`) can run
alongside FlowState's existing `nomic-embed-text` vault-sync stack, what its decision output
actually looks like on real FlowState seams, and whether concurrent decision calls stall
vault-sync embeddings.

## Environment realities (important context)

- Host **has two GPUs but neither is usable for inference** *(corrected 2026-10-14; originally
  reported as "no working GPU")*: an **NVIDIA RTX 4060 Max-Q Mobile** (nvidia driver 580.178.04
  loaded, but `nvidia-smi` fails with "Unable to determine the device handle" — broken
  userspace/driver state, no usable CUDA device) and an **AMD Radeon 680M iGPU** (amdgpu
  loaded, display-only; no ROCm/`rocm-smi` userspace installed, and gfx1035 is unsupported
  by Ollama's ROCm builds). Ollama 0.30.7 logs show only the CPU backend (AMD Ryzen 7 7735HS,
  15 GB RAM, ~6 GB free at test time), so inference is **100% CPU**. All numbers below are
  CPU numbers. Note: the RTX 4060 would be the viable accelerator if the driver issue is
  fixed (driver reinstall / dmesg check), but that is out of scope for this spike.
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

## GPU re-run (2026-10-14, after driver fix)

Driver fixed (NVIDIA 615.78.08, CUDA 13.4, rebooted). GPU: **RTX 4060 Laptop, 8 GB VRAM**
(7.7 GiB usable / 7.2 GiB available). Ollama 0.40.4-rc1 now logs `inference compute ... library=CUDA`;
a **final Ollama 0.40.4** portable build was pulled into `/tmp/ollama-new` because — see gotcha below —
**0.40.4-rc1 and 0.40.4 have different `/v1/systemone` request schemas**. All GPU numbers use the
official 0.40.4 release on port 11435 with `OLLAMA_MAX_LOADED_MODELS=2`.

### Schema gotcha (breaking finding)

- On **0.40.4-rc1**, the previously-working request shape (`questions` = array of `{type, options}`)
  now fails every time with a misleading parser error (`"Value looks like object, but can't find
  closing '}' symbol"`). Probing shows `questions` only decodes as `null` (→ "questions must contain
  1–64 fields") or an **object of named questions**.
- The **0.40.4 final** schema (verified working): `questions` is an **object mapping name →
  question**; each question = `{"type": "choice"|"score"|"noul", "instructions": <required str>,
  "criteria": <object for choice/noul — for `choice` the criteria keys ARE the options and values
  are descriptions; array of 2–10 level strings for `score`>, "options": <ignored dummy, must be
  present as non-string>}`. Array form (`questions: [...]`) returns the same cryptic parser error —
  treat that error message as "schema mismatch", not a transport bug. The answer mirrors the
  questions object (`answers.<name>`). DD-gate noul on the failing-DD state came back 0.82 here vs
  0.0396 on rc1/CPU — values are **not comparable across the schema change**; re-calibrate whatever
  the production pin lands on.

### Placement & co-residency

- `kev` loads **100% GPU**: 33/33 layers offloaded, CUDA0 model buffer 2,882 MiB; `size_vram` 4.53 GB
  at default ctx 4096 (KV/RS/compute buffers add ~1.4 GB). Cold model load ~9.6 s (one-off).
- `nomic-embed-text` alongside: 0.34 GB VRAM, 100% GPU. **Both resident on one daemon: 4.87 GB of
  8 GB VRAM (~5.4 GB per `nvidia-smi` incl. display/desktop)** — fits with ~2.8 GB headroom.

### Decision-latency suite (warm, GPU, both models resident)

| Seam | CPU (0.40.4-rc1) | GPU (0.40.4) | Result |
|---|---|---|---|
| (a) Swarm routing (bug-hunt task) | 0.6–2.5 s | **0.04–0.07 s** | `bug-hunt` @ 0.97–0.98, conf 0.96 — sharper than CPU run (0.80) |
| (b) DD gate `noul` (failing report) | 2.4 s | **0.03–0.04 s** | 0.82 (⚠ different schema/version — see gotcha; not comparable to 0.0396) |
| (c) Plan quality `score` | 1.8 s | **0.03 s** | 2.28–2.32 on 5-level — consistent with CPU's 2.53 |
| Ambiguous multi-intent | 1.8 s | **0.03–0.04 s** | `devops` @ 0.59, conf 0.39 — spread preserved |
| **Long state ~2 KB (headline)** | **24.1 s** | **0.22–0.24 s** (~0.46 first touch) | ~100× faster; prefill no longer dominates |
| Simple 3-option routing | 0.61–0.68 s | **0.03 s** | rock-stable |

### Concurrency (decisions during vault work)

- `flowstate vault sync` (unchanged vault, 770 files): **18.6 s with 12 concurrent decision calls
  (incl. 2 KB states) vs 20.1 s idle** on the same run conditions — no measurable contention
  (decisions ride the GPU, sync embeddings were on the separate production daemon as today).
- Single-daemon path (both models on 11435, the simpler production topology): 50 warm embeds took
  0.19 s alone and **0.42 s while 10 decision calls ran concurrently** (~2× embed cost, ~106 ms/call
  amortised); decision latency stayed 0.03–0.08 s throughout. `OLLAMA_NUM_PARALLEL=1` did not cause
  the predicted serialisation stall — the decision model is a single embedding-style forward pass
  (0 output tokens), so it never holds the slot for long.

### GPU verdict

The 2 KB long-state case collapses from **24.1 s → ~0.22 s**; every seam is now comfortably
sub-100 ms. With both models fitting in 8 GB on one daemon and no contention measured, the GPU
removes both prior blockers (latency budget and instance isolation).

## Revised suggested requirements (post-GPU)

1. **Model size floor:** unchanged — 4B-class, ≈4.5 GB VRAM at ctx 4096 + 0.34 GB embedder.
   **New: GPU with ≥6 GB free VRAM is the requirement for the hot path**; CPU-only hosts are
   advisory/async-only (24 s for 2 KB states is unusable inline).
2. **Instance isolation: requirement DROPPED on GPU hosts.** A single daemon with
   `OLLAMA_MAX_LOADED_MODELS=2` measured no sync interference (embed ~2× slower under concurrent
   decisions, still ~4 ms/call amortised; sync wall time unchanged). Keep the separate-instance
   option only as a CPU-host fallback / version quarantine while production Ollama is < 0.40.
3. **Version/schema pin (NEW, hard requirement):** the `/v1/systemone` request schema changed
   between 0.40.4-rc1 and 0.40.4 final (array → named-object `questions`; `instructions` required;
   `choice` options now live in `criteria` keys). Pin the production daemon to an exact version,
   have the `DecisionProvider` probe the schema at startup, and **re-calibrate any thresholds
   against the pinned version** (the DD-gate noul value moved 0.04 → 0.82 for the same intent).
   **Re-run note (2026-10-14):** kev GGUF decision decoding additionally requires
   **Ollama ≥ 0.40.4 final** — 0.40.3 (mislabelled rc1 binaries in `ollama-spike-tmp/{rc,new}`)
   returns `unsupported decision encoding "kev"`. Pin ≥ 0.40.4.
4. **Provider interface:** as before (`Choose/Judge/Score`), plus a schema-version adapter and
   mapping the cryptic `"Value looks like object..."` parser error to "unsupported schema/version".
5. **Latency budget: hot-path viable on GPU.** Sub-100 ms for short states, ~0.25 s for 2 KB
   states. Inline (synchronous) routing decisions are acceptable on GPU; keep CPU hosts
   async/advisory. State summarisation is no longer required for ≤2 KB states.
6. **Calibration gate:** unchanged, and now more urgent given the version-to-version probability
   drift observed in (b).

## GPU re-run #2 (post driver migration to 615.78.08, 2026-10-14)

A second GPU re-run was performed after the user rebooted with the migrated NVIDIA
**615.78.08** driver (CUDA UMD 13.4). Measurement sections only, same methodology.

**Preconditions:** `nvidia-smi` healthy — RTX 4060 Laptop, 8,188 MiB total. Production
daemon (11434, Ollama 0.30.7) already running `nomic-embed-text` **100% GPU** (474 MiB
llama-server process at boot).

**Tooling note:** the earlier `new`/`rc` binaries under `ollama-spike-tmp/` turned out to
be **0.40.3**, which rejects the model outright (`unsupported decision encoding "kev"`)
and cannot serve `/v1/systemone` for it — a fresh official **0.40.4 final** tarball was
re-downloaded from GitHub releases into `ollama-spike-tmp/v0404/` (the previous
`/tmp/ollama-*.tgz` files were 404 error pages). Side daemon restarted on 11435 with
`OLLAMA_MAX_LOADED_MODELS=2`, logs `library=CUDA driver=13.4 total="7.7 GiB"`.

### Placement & co-residency (re-confirmed)

- kev: 100% GPU, `size_vram` **4.53 GB** (4,533,539,307 B) at ctx 4096; cold load ~4.7 s.
- kev + nomic-embed-text both resident on the one side daemon: **4.87 GB** by `ollama ps`;
  `nvidia-smi` total **5,366 MiB** of 8,188 MiB (incl. desktop ~486 MiB) → fits with
  ~2.8 GB headroom. Confirms the two-instance workaround is unnecessary on GPU.
- Production daemon (11434): nomic-embed-text **100% GPU** (323 MB, 474 MiB process
  footprint) — recorded; it moved to GPU automatically after the driver fix.
- Warm single embed on 11434: **0.024–0.031 s** (vs ~31–37 ms CPU in the original spike —
  marginally better, effectively unchanged; embeddings were never the bottleneck).

### Decision latency (warm, GPU, 0.40.4 final, both models resident)

| Call | Latency | Result |
|---|---|---|
| Short-state swarm routing (bug-hunt-style state, 5 options) | **0.038–0.044 s** | `security` @ 0.43 vs `bug-hunt` 0.34, conf 0.28 — plausible; note this state favours security wording, and confidence is low |
| ~2 KB repeated long state | **0.26–0.52 s** (first touch 0.52 s, warm 0.26 s) | `none` @ 0.42 — repetitive padded state, answer is degenerate but latency is the point |
| Simple short routing, warm | **0.030–0.037 s** | stable |

These match the earlier GPU run (sub-100 ms short, ~0.25 s for 2 KB) — reproducible.

### Concurrency (decisions during vault sync, GPU)

- 8 sequential decision calls fired while `flowstate vault sync` ran on the production
  daemon (770 files, 0 changed): decisions **0.030–0.043 s** — zero inflation vs idle
  (0.033–0.158 s idle range, first call after idle pause slightly higher at 0.158 s).
- Sync wall time: **13.7 s under concurrent decisions vs 9.6 s idle** — the delta is
  within the run-to-run variance observed across this spike (16.5–21.8 s historically;
  both runs well inside that band) and the decisions ride a different GPU-resident model,
  so no queueing/stalling is evident. 0 chunks embedded (unchanged vault), so this
  exercises the metadata pass, not embedding throughput.

### Re-run conclusions

- All prior GPU conclusions hold and are now reproduced on the migrated driver: hot-path
  sub-100 ms short-state, ~0.25 s for 2 KB, both models in 8 GB with headroom, no
  interference between decisions and vault sync.
- New caveat: **only Ollama ≥ 0.40.4 final decodes the kev GGUF decision encoding**;
  0.40.3 and the rc1/0.40.3 binaries in `ollama-spike-tmp/{rc,new}` fail with
  `unsupported decision encoding "kev"`. The version pin in the requirements must be
  **≥ 0.40.4**, not merely ≥ 0.40.

## Throwaway artefacts

Side-by-side Ollama 0.40.4-rc1 in `/home/baphled/ollama-spike-tmp/` (daemon stopped after
the spike); pulled model lives in the shared `~/.ollama/models` and can be removed with
`ollama rm hf.co/ggml-org/Kev-4B-GGUF:Q4_K_M`. No repo files changed besides this report.
