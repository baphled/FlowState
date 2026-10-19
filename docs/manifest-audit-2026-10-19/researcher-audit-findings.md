# Researcher Audit Findings — Manifest Registry & Delivery Path

Chain: `glm52` · Date: 2026-10-19 · READ-ONLY audit · Model: zai/glm-5.2
Secrets excluded; API keys never inspected beyond set/unset booleans.

---

## 1. Effective-state summary

**OBSERVATION — effective registry source of truth is `~/.config/flowstate/agents/`.**
`~/.config/flowstate/config.yaml:1` sets `agent_dir: /home/baphled/.config/flowstate/agents`; `agent_dirs: []` and `agent_overrides: {}` (config.yaml:2-3), so no layered dirs or prompt appends are active.

**OBSERVATION — installed vs repo (`internal/app/agents/`) agent diffs that matter for delivery:**

| Agent | Status |
|---|---|
| Team-Lead, Tech-Lead, Senior/Mid/Junior-Engineer, Principal-Engineer, QA-Engineer, Code-Reviewer, Knowledge-Base-Curator, Security-Engineer, DevOps | **byte-identical** to repo |
| `Writer.md` | **installed only** (no repo counterpart); `writer.md` (lowercase) is identical to repo `writer.md`. Two IDs coexist: `Writer` and `writer` |
| `Researcher.md` | **installed only**; `researcher.md` identical to repo. Duplicate-ID situation again (`Researcher` vs `researcher`) |
| Extra installed-only: mental-health swarm (audhd-coach, biochemistry-analyst, etc.), pogo-*, OVERRIDES.md, `executor.md.pre-2026-05-rewrite` (stale backup file in a discovered dir) |

**OBSERVATION — swarms.** Installed `~/.config/flowstate/swarms/`:
- `dev-swarm.yml`: **byte-identical to repo** (diff = 0 lines). The brief's claim that the 3 `builtin:fs-pollution-guard` gates are missing from installed dev-swarm is **not correct as of today** — all three present at installed lines 111/118/125 (`post-member-senior-engineer-fs-pollution`, `post-member-writer-fs-pollution`, `post-member-kb-curator-fs-pollution`).
- `engineer-swarm.yml`: **DIFFERS** — installed file is missing the two fs-pollution gates present in repo `internal/app/swarms/engineer-swarm.yml:88-110` (`post-member-mid-engineer-fs-pollution`, `post-member-junior-engineer-fs-pollution`, both `builtin:fs-pollution-guard`, target Mid/Junior, `output_key: fs-writes`, MEDIUM, halt). Installed engineer-swarm has only 4 gates (2× `ext:tdd-commit-gate`, 1× `ext:standards-review-gate`, 1× `builtin:evidence-grounding`); repo has 6.
- `meta-swarm.yml`, `a-team`, `board-room`, `planning-loop`, `plan-sme-swarm`, `solo`: identical.
- `review-swarm.yml`: installed only (no repo counterpart) — lead Code-Reviewer, `auto_dispatch_on_lead: true`, 4-member roster.
- Extra installed: due-diligence dir, gate-smoke-swarm, mental-health, pogo, test-basic.

**OBSERVATION — `internal/app/swarm_wiring_test.go:64-102` (`TestEmbeddedSwarmsAttachFSPollutionGuard`)** pins the fs-pollution gates against the *embedded* manifests, not the installed ones — so the missing engineer-swarm gates in `~/.config/flowstate/swarms/` are invisible to CI.

---

## 2. Precedence (loader/schema)

**OBSERVATION — registry construction** (`internal/app/app.go:5080-5107` `setupAgentRegistry`):
1. `Discover(cfg.AgentDir)` first.
2. Each `cfg.AgentDirs` entry merged via `DiscoverMerge` in order — **later entries override earlier on ID clash** (app.go:5074-5075). Currently empty, so no effect.
3. Repo/bundled manifests are **not** consulted at runtime at all — the embedded set is only used by seeding (`internal/app/seed.go`) which **skips existing files** (seedSubdir, seed.go:459-461: "Copies each matching file ... only when the destination file does not already exist"). So **installed config wins permanently unless `flowstate agents refresh` is run** (`internal/cli/agents.go:168-193` force-refreshes from embedded set; `--dry-run`/`--agent` supported).
4. `agent_overrides` (config.go:99, `AgentOverrideConfig.PromptAppend` config.go:1100-1102) only appends prompt text via `Engine.SetAgentOverrides` (app.go:1806-1819, engine.go:2313) — it cannot change models, tools, or gates. Currently `{}`.

**OBSERVATION — swarm loading** (`internal/app/app.go:4925-4944` `resolveSwarmDir`): swarms load from `~/.config/flowstate/swarms/` only; same skip-on-existing seeding applies, and there is **no `swarms refresh` equivalent** — stale installed swarm yml files (engineer-swarm) never self-heal.

**OBSERVATION — auto-dispatch.** `internal/swarm/registry.go:116-165` `AutoDispatchSwarmFor`: `@Team-Lead` auto-dispatches dev-swarm because dev-swarm.yml:33 sets `auto_dispatch_on_lead: true` and Team-Lead is the lead. Same mechanism for review-swarm via Code-Reviewer.

---

## 3. Layering overhead for atomic tasks

**OBSERVATION — the mandatory hierarchy is forced by prose in manifests + the composed sub-swarm, not by any schema field:**

- `~/.config/flowstate/swarms/dev-swarm.yml:39-43,46`: roster contains `engineer-swarm` (not flat Senior/Mid/Junior) — "Team-Lead delegates 'implementation' as a single unit; Senior-Engineer orchestrates the TDD hierarchy internally".
- `engineer-swarm.yml` (installed, lines 6-8): "decomposing delivery briefs into atomic TDD cycles delegated **through the Mid → Junior hierarchy**"; lead Senior-Engineer, members Mid/Junior/Principal/QA.
- `Senior-Engineer.md:53-55` `delegation_allowlist: [Mid-Engineer, Junior-Engineer]` — Senior cannot implement directly by convention? No — it *can* implement (role text says it implements), but the swarm description and Tech-Lead flow push delegation down.
- `Mid-Engineer.md:49-53`: allowlist `[Junior-Engineer, ...]`; role (line 39-40): "decompose and delegate atomic tasks to Junior-Engineer" — this is the field that forces Senior→Mid→Junior even for atomic tasks.
- `Team-Lead.md:82-83,105-106`: routes decomposition → Tech-Lead, implementation → Senior-Engineer; Tech-Lead.md:150-155 "Standard implementation flow" mandates Senior → Hygiene → QA → Code-Reviewer for every feature.

**PROPOSAL — smallest edit to break mandatory chains for atomic tasks:**
- `~/.config/flowstate/agents/Mid-Engineer.md:39-40` — reword goal to "implement atomic tasks directly; delegate to Junior-Engineer only for parallelisable grunt work".
- `~/.config/flowstate/agents/Senior-Engineer.md` sub-delegation table — add "atomic task → implement directly, do not re-delegate".
- `~/.config/flowstate/agents/Tech-Lead.md:150-155` — gate the 4-step flow on "feature work"; add "atomic task → delegate directly to Senior-Engineer for direct implementation, skip hygiene/review steps for trivial diffs".
- `~/.config/flowstate/swarms/engineer-swarm.yml` description — replace "through the Mid → Junior hierarchy" with "to Mid or Junior as complexity warrants".

No schema field enforces the chain; all four are prompt-level fixes.

---

## 4. Model-route audit (Z.ai-only claim vs reality)

**OBSERVATION — `BuildConfigPreferences`** (`internal/app/providers/registry.go:155-167` + `configuredProviderSpecs` 173-186 + `providerCredentialChecks` 202-229): a provider enters the failover chain iff `model != ""` **AND** credential configured. With current config.yaml:

| Provider | model | key | in chain? |
|---|---|---|---|
| zai | glm-5.2 | SET | **yes (hoisted first, `providers.default: zai`, registry.go:258-274)** |
| opencode-go | DeepSeek V4 Pro | SET | **yes** |
| ollama | llama3.2 | SET (host ≠ default) | **yes** |
| anthropic | `''` | SET | **no** (empty model filters it out — registry.go:160) |
| openai | `''` | SET (OAuth enabled) | **no** (empty model) |

**OBSERVATION — exact condition for a non-Z.ai delivery call:** `Engine.ReseedFailoverBasePreferences` (engine.go:2407-2560):
- Manifest with `preferred_models` + **permissive** policy → manifest head then **the full config baseline appended as fallback tail** (engine.go:2543-2553). So a rate-limited zai/glm-5.2 falls back to **opencode-go/DeepSeek V4 Pro** and then **ollama/llama3.2**. This is the silent-fallout path.
- **strict** policy → manifest head only, **except** when the head is hard-down, where `StrictChainWithHealthyFallback` (failover/strict_fallback.go:40-47) appends the healthy global chain — again opencode-go/ollama.
- No `preferred_models` at all (Team-Lead, Tech-Lead, Mid, Code-Reviewer, DevOps, Principal — none declare `model_policy`/`preferred_models`; only Senior:79 permissive, Junior:64 strict, QA/Security/KB-Curator permissive, Writer/Researcher strict with zai glm-5.2/4.6 lists) → config baseline verbatim: zai → opencode-go → ollama.

**Conclusion:** a delivery call **can** silently fall to opencode-go (DeepSeek) or ollama (llama3.2) on zai rate-limit/hard-down. Anthropic/OpenAI currently *cannot* serve (empty `providers.*.model`), but would enter the chain the moment a model is set there — the api_keys being configured is not itself sufficient.

**OBSERVATION — other route surfaces:**
- `category_routing` (config.yaml): every tier → zai except `quick → glm-4-flash zai`. All Z.ai ✓.
- `summariser` (config.yaml:166-168): provider zai/glm-5.2 ✓, **but `ollama_model: llama3.2`** — `SummariserChainEntries` (config/summariser_config.go:46-80) always appends anthropic (model falls back to `providers.anthropic.model` = empty) and an **always-present Ollama last-resort hop** ("Never empty — the Ollama hop is always present"). So compaction/summarisation can silently run on local llama3.2.
- `plugins.failover.tiers` (config.yaml:95-99): glm-4-flash tier-0, glm-5.2 tier-1, llama3/llama3.2 tier-3 — llama entries are dead weight but harmless; the tier map itself doesn't add providers.

**PROPOSAL — smallest edits for hard Z.ai-only:**
- `~/.config/flowstate/config.yaml` → `providers.opencode-go.model: ''` and `providers.ollama.model: ''` (or clear ollama host) — removes both from `BuildConfigPreferences` chain. ⚠️ side effect: summariser loses its always-present last-resort hop; verify `SummariserChainEntries` empty-chain behaviour first.
- Or per-agent: add `model_policy: strict` + `preferred_models: [zai/glm-5.2]` to Team-Lead, Tech-Lead, Mid-Engineer, Code-Reviewer, DevOps, Principal-Engineer (currently absent — confirmed grep no-output) so the config tail is never appended (engine.go:2544-2549).
- `summariser.ollama_model` → leave blank is not enough (falls back to providers.ollama.model then llama3.2, summariser_config.go:22-24); needs the code-level guarantee reviewed if Z.ai-only is absolute.

---

## 5. Gate drift

**OBSERVATION:**
1. **engineer-swarm.yml installed ≠ repo** (the real drift): installed is missing `post-member-mid-engineer-fs-pollution` and `post-member-junior-engineer-fs-pollution` (`builtin:fs-pollution-guard`, repo lines 88-110). Because seeding skips existing files and there is no refresh verb for swarms, this drift persists indefinitely. The wiring test (`swarm_wiring_test.go:64-102`) only checks the embedded FS, so it passes while the runtime gate never fires.
2. **dev-swarm.yml**: brief said 3 fs-pollution gates missing from installed — **refuted**: installed is byte-identical (diff 0). All 3 gates present (installed lines 111-131).
3. review-swarm.yml exists only installed (repo has no counterpart) — benign but means the repo is not the full source of truth for swarm surface.
4. `Writer`/`Researcher` (capitalised) installed-only manifests are referenced by dev-swarm roster (`members: - Researcher ... - Writer`) — the swarm resolves against the installed registry so it works, but a fresh seed from repo alone would break the roster.

**PROPOSAL — smallest edits:**
- `flowstate agents refresh` does not cover swarms; copy repo `internal/app/swarms/engineer-swarm.yml` → `~/.config/flowstate/swarms/engineer-swarm.yml` (or add a `swarms refresh` command mirroring `internal/cli/agents.go:168+`).
- Add `Writer.md`/`Researcher.md` to `internal/app/agents/` (or drop capitals from dev-swarm roster) so repo is self-sufficient.

---

## 6. Stale instructions / memory / budget

**OBSERVATION:**
- **No stale App/Intents/UIKit references** found in any delivery-path manifest (grep across Team-Lead, Tech-Lead, Senior/Mid/Junior, QA, Code-Reviewer — zero hits). Minor: `Team-Lead.md:131` and `Accessibility-Engineer.md:75,108` still route to **TUI-Engineer**, an agent that does not exist in either registry — dead routing target.
- **Memory tools**: every delivery-path agent carries `search_nodes`/`open_nodes` + `memory-keeper`/`knowledge-base` in `always_active_skills` (e.g. Junior-Engineer.md:14,24,31-32; Mid-Engineer.md:15,25,33-34). These are load-on-relevance via `SkillAutoLoaderHook`, not unconditional searches; `uses_recall: false` on all delivery agents (opt-out of vector recall, manifest.go:40-46) — so no unconditional vault-rag cost. Cost is the skill header + any skill_load the model chooses to fire.
- **Context/budget/checkpoint**: manifests define `max_recursion_depth: 2`, `summary_tier: quick`, `sliding_window_size: 10`, `compaction_threshold: 0.75` (Team-Lead.md:45-47 and equivalents). No per-agent `context_limit`/checkpoint directives exist in any delivery manifest — nothing found to flag beyond the compaction settings already present. Global `system_prompt_budget: 32768` (config.yaml).

**PROPOSAL:** remove/replace TUI-Engineer references (Team-Lead.md:131; Accessibility-Engineer.md:75,108) — no other stale-instruction edits evidenced.

---

## 7. Coordination-store key conventions

**OBSERVATION:**
- `context.chain_prefix` (engineer-swarm.yml:109 = `engineer-swarm`) is the namespace prefix; `internal/swarm/context.go:44-76,93-102`: blank chain_prefix falls back to swarm id; a per-run ChainID is assigned at swarm start (`AssignRunChainID`) and prefixed onto it. Sub-swarm output flows under `<chain_prefix>/<chainID>/...` (e.g. `engineer-swarm/<runID>/output`).
- dev-swarm gate comments (dev-swarm.yml:71-79) confirm the convention: structured gate outputs publish under `dev-swarm/{chainID}/output`; `output_key: fs-writes` (repo engineer-swarm gates) names the member-published key the fs-pollution runner validates — this is the exact contract the installed engineer-swarm is missing.
- `AutoDispatchSwarmFor` + `chain_prefix` are the only loader-consumed key fields; no other per-swarm key conventions parsed by the loader.

---

## Recommended smallest edits (summary)

| # | File + field | Type | Change |
|---|---|---|---|
| 1 | `~/.config/flowstate/swarms/engineer-swarm.yml` gates | OBSERVATION→FIX | Sync with repo: add Mid/Junior fs-pollution gates (repo lines 88-110) |
| 2 | `~/.config/flowstate/agents/Mid-Engineer.md:39-40` (metadata.role/goal) | PROPOSAL | Allow direct implementation of atomic tasks |
| 3 | `~/.config/flowstate/agents/Tech-Lead.md:150-155` | PROPOSAL | Fast path for atomic tasks (skip 4-step flow) |
| 4 | `~/.config/flowstate/agents/Senior-Engineer.md` sub-delegation table | PROPOSAL | "Atomic → implement directly" |
| 5 | `~/.config/flowstate/swarms/engineer-swarm.yml` description | PROPOSAL | Drop "through the Mid → Junior hierarchy" |
| 6 | `~/.config/flowstate/config.yaml` `providers.opencode-go.model`, `providers.ollama.model` | PROPOSAL | Blank to make failover chain Z.ai-only (verify summariser last-resort hop) |
| 7 | Team-Lead/Tech-Lead/Mid/Code-Reviewer/DevOps/Principal manifests | PROPOSAL | `model_policy: strict` + zai preferred_models |
| 8 | `Team-Lead.md:131`, `Accessibility-Engineer.md:75,108` | PROPOSAL | Remove dead TUI-Engineer routing |
| 9 | `internal/app/agents/` | PROPOSAL | Add Writer.md/Researcher.md so repo roster is self-sufficient |
