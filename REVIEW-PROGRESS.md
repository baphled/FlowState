# Review Progress

## 2026-10-19 — Branch-policy survey & delivery rebase (glm52)

**Survey (read-only) of lineage around `main` (c6a6b22d):**

1. **`backup/next-old` (dfaefb85, 2026-08-27)** — 1,936 commits / 1,653 files / +400k lines over main. Merge-base is `2a732d1a` (KaRiya CI pipeline). It contains the full `feature/agent-platform` integration (merge df6d0e84) plus only 6 files not also present in agent-platform tip (voice capture/cli/talk `.feature` variants + `PermissionModeChip` Vue components). Everything else in next-old is reachable via agent-platform or its side branches. Post-merge additions unique in time: fs-pollution guard gate work, smoke-glue test wiring (tagged wip), commitlint/keyword-ADR CI tightening.
2. **`feature/agent-platform` tip (ded67250, 2026-10-16)** — 2,254 commits / 1,854 files / +447k lines branched directly off main tip. Major areas: engine (195 files), app agents/skills, tool suite, provider matrix (quota, anthropic, copilot, zai, ollama…), plugin failover/events, swarm, recall/qdrant, context/compaction, plan/harness, web chat+voice Vue stores, examples/swarms+gates, CI/CD pipeline. Note: tip also integrates `feature/compaction-summariser-chain` (main worktree's current branch).
3. **`next` (0197b4c5)** — exactly 6 correction commits on main (M01/M07 failover allowlist + lint + manifest audit snapshot); clean to re-point.
4. **Refs:** `backup/pre-cleanup-3e89a376` (session/validation refactors), `backup/agent-platform-*` archive branches, tags `backup/agent-platform-pre-promotion`, `backup/pre-tui-deletion`, `backup/vue-ui-rebase-pre-agentplatform`, `backup/runner-hardening{,-gl}`. No stashes.

**Action:** created `feature/delivery-gl52` from `main` c6a6b22d in worktree `feature-delivery-gl52`, cherry-picked all 6 next commits (no conflicts; `.omo` run-continuation artifact introduced then removed by later commit). `make check` exit 0. `next` NOT reset — pending user confirmation given agent-platform content supersedes much of next-old.
