# UX Overhaul Plan (v2) — In-Repo Greenfield Web Console

> Consolidated canonical plan. Vault condensed copy: `1. Projects/FlowState/Plans/ux-overhaul-plan.md`.
> Status: Approved scope (2026-10-09). UI/UX is king; backend changes are required enablers.

## Why the consolidation

- **Consolidation:** the UI returns to the FlowState repo as a first-class in-repo console; one frontend dependency root, one CI story.
- **IP/Licensing severance:** FullSpektrum are no longer permitted to use our work — zero code reuse from the old standalone frontend package. Patterns may be re-created (e.g. the QuestionSelector interaction model); code may not.
- **Rewrite mandate:** the existing UI is confusing and ugly; we build fresh to the opencode gold standard rather than renovating.

## Target architecture

| Piece | Location |
|---|---|
| Vite root (Vue 3 + Pinia + Vite) | `webconsole/` |
| Go embed | `internal/webconsole/embed.go` — `//go:embed all:dist` + SPA fallback handler |
| Mount | `cmd/flowstate/main.go` mounts the console at `/` (API stays under `/api`) |
| Build | `make webconsole-build` (npm → dist), `make build` embeds dist; `make webconsole-dev` runs Vite dev server proxying `/api` to the Go binary (SSE-safe: `proxy.buffer: false`) |
| Legacy frontend | archived, not deleted — freeze → tag `archived/pre-webconsole` → remove worktree; history preserved on remote; tombstone README |

## Phases

### P0 — Transport & contracts (backend, unchanged)
- U1 Resumable session SSE with `Last-Event-ID`
- U2 Cancel endpoint
- U3 Unified DTO casing — **pre-scaffold gate**
- U4 Swarm event contract (per-agent: `agent_id`, role, `current_activity`, `started_at`/`ended_at`, status transitions, child-session ids, tool-call counts; idempotent, replayable)
- U5 Pagination + session titles

### P0.5 — Scaffold (new)
- `webconsole/` Vite scaffold, tokens seeded, SSE-native chatStore, embed pipeline proven end-to-end (`make build` serves the shell).

### P1 — Core interaction
- Stop button wired to U2; status strings; toasts + live region.

### P2 — Discovery
- Ctrl+K command palette; @-mention autocomplete (backend `ExtractAtMentions` already exists); slash commands.

### P3 — Sessions sidebar
- Search, rename, resume, child-session nesting (U5).

### P4 — Theming & accessibility (audit/gate, not migration)
- Token system, light/dark, WCAG AA clean from day one.

### P5 — Swarm visibility
- **SwarmCard** (single collapsed card in chat stream): agent chips with status icons + overall progress.
- **SwarmAgentCard** (expanded): agent name/role, live current-activity string, live elapsed timer, status badge (pending/running/complete/failed), link to child session.
- Click/Enter/Space to expand; Esc collapses and restores focus; expansion state persists per message across refresh (event-sourced via U4 + `Last-Event-ID`).
- Acceptance: refresh mid-swarm reconstructs full swarm state from replayed events; keyboard-only navigation passes; per-agent timers tick live.

```
Collapsed:  ┌─ 🐝 dev-swarm · 3 agents ─ [senior ✓] [qa ⟳] [reviewer ⋯] ─ 1/3 done ─┐
Expanded:   ┌──────────────────────────────────────────────────────────────────┐
            │ senior-engineer · implement            ⟳ running · 04:12        │
            │   → editing internal/api/server.go                              │
            │ qa-engineer · test                    ⋯ pending                  │
            │ code-reviewer · review                ✓ complete · 02:57        │
            └──────────────────────────────────────────────────────────────────┘
```

## Risks (new)
- Accidental legacy code reuse → code-review gate on every PR touching `webconsole/`.
- Node-less Go builds → embed only committed `dist/` artifacts or CI build step before `go build`.
- Vite proxy buffering SSE → `proxy.buffer: false` verified in scaffold phase.

## Non-goals
- Mobile-native apps; the old Bubble Tea TUI; feature parity with archived frontend beyond patterns worth keeping.
