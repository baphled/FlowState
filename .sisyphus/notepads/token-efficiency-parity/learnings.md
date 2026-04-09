# Learnings — token-efficiency-parity

## [2026-04-09] Session: ses_28e354a27ffeNTGkqgTK6WSST4

### Key Architecture Context
- FlowState sends ~8,600+ tokens per first-turn request with no size guards
- OpenCode enforces a 35KB ceiling, 3-tier skill selection with byte budgets, and session continuation skip
- SkillAutoLoaderHook fires only on first user message; injects lean header (~200 bytes), NOT full skill content
- Full skill content loaded via `skill_load` tool calls → tool results in history → NEVER evicted
- FileSkillResolver has per-skill cache but no pre-scan, no aggregate ceiling
- WindowBuilder tracks budgets across "system"/"summary"/"semantic"/"sliding" but has NO skill-aware logic
- context_injection.go demonstrates caching pattern: 2KB max + 5min TTL

### Critical Guardrails
- G1: Must NOT change skill_load tool interface
- G2: Must NOT touch internal/recall/ or internal/memory/
- G3: Must NOT refactor WindowBuilder's core truncation algorithm — only ADD skill-aware identification
- G4: Must NOT change skill file format
- G5: Must NOT hardcode 35KB ceiling — must be configurable in SkillAutoLoaderConfig
- G6: All Ginkgo test commands use `go test -v ./internal/PACKAGE/... -count=1` (NOT `go test -run`)
- G7: Must NOT change how containsAssistantMessage() detects continuation
- G9: load_skills from orchestrator MUST continue to bypass MaxAutoSkills caps
- G10: No comments inside function bodies (AGENTS.md rule)
- G11: All commits via make ai-commit with AI attribution

## [2026-04-09] Task 3: SkillAutoLoaderConfig byte budgets

### Key Findings
- `SkillAutoLoaderConfig` now carries two configurable byte budget fields: `max_auto_skills_bytes` and `per_skill_max_bytes`
- Default values follow the OpenCode parity targets: 35KB = 35840 bytes, 5KB = 5120 bytes
- YAML round-tripping preserves both defaults and explicit zero values, so `0` remains a valid opt-out signal

### Verification Notes
- `go test -v ./internal/hook/... -count=1` passed after adding the fields
- YAML-focused specs passed for explicit values, round-trip behaviour, and zero-budget configuration


## [2026-04-09] Baseline token measurement
- Measured the system prompt, lean skill header, and estimated first-turn token footprint from the configured skill directory.
- The evidence file now records system prompt tokens, lean header tokens, skill count, total skill bytes, and the configured skill directory path for parity comparisons.
- The default user config points SkillDir at ~/.config/opencode/skills, so measurements should use config.LoadConfig() rather than hardcoded paths.
