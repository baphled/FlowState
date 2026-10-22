---
name: skill-discovery
description: Auto-load local skills and suggest external skills
category: Core Universal
---

# Skill: skill-discovery

**classification:** Core Universal  
**tier:** T0 (System Behavior)  
**confidence:** 10/10  
**source:** system-mandatory  
**dependencies:** pre-action, memory-keeper

---

## Purpose

Skill Discovery ensures correct domain expertise for every task.
1. **Internal Auto-loading**: Identifies and loads installed skills based on context.
2. **External Discovery Workflow**: Identifies gaps and imports community skills from [skills.sh](https://skills.sh).

---

## Phase 0: Automatic Classification

**Execute BEFORE any tool call.**

1. **PARSE** request to identify task type and domain.
2. **CLASSIFY** by task type:
   - **Implementation** — Writing code
   - **Testing** — Tests, fixtures, harnesses
   - **Writing/Docs** — Prose, READMEs, ADRs, API docs
   - **Research** — Exploring, understanding systems
   - **Architecture** — Design, patterns, refactoring
   - **Security** — Audits, secure coding
   - **Ops/DevOps** — CI/CD, infra, monitoring
   - **Data Analysis** — Metrics, stats, reporting
   - **Git/Delivery** — Commits, PRs, releases
   - **Orchestration** — Breakdown, delegation
3. **LOAD** skills from Matrix matching task type.
4. **DETECT** language and load language-specific skills.
5. **DELEGATE** if complexity warrants.

---

## Internal Skill Selection Matrix

| Task Type | Category | Skills |
|-----------|----------|--------|
| **Implementation** | unspecified-high | clean-code, error-handling, design-patterns |
| **Testing** | unspecified-high | bdd-workflow, bdd-best-practices, test-fixtures |
| **Writing/Docs** | writing | documentation-writing, british-english, proof-reader |
| **Research** | deep | investigation, research, critical-thinking, epistemic-rigor |
| **Architecture** | ultrabrain | architecture, design-patterns, systems-thinker, domain-modeling |
| **Security** | unspecified-high | security, cyber-security, prove-correctness |
| **Ops/DevOps** | unspecified-high | devops, automation, infrastructure-as-code, monitoring |
| **Data Analysis** | unspecified-high | epistemic-rigor, question-resolver, math-expert |
| **Git/Delivery** | quick | git-master, create-pr, release-management |
| **Orchestration** | ultrabrain | architecture, systems-thinker, scope-management, estimation |
| **Refactoring** | deep | refactor, clean-code, design-patterns |
| **Performance** | unspecified-high | performance, profiling, benchmarking |
| **Debugging** | deep | investigation, critical-thinking, logging-observability |

---

## External Discovery Workflow

Use when local options are exhausted and ANY condition met:
1. **Unfamiliar technology** — library not covered by installed skills
2. **Explicit skill gap** — agent lacks domain expertise
3. **Repeated uncertainty** — 2+ uncertain statements in session

### Step 1: Search skills.sh
```bash
scripts/skills-sh.sh find "query"
```
Returns JSON array: `[{"package": "owner/repo@skill-name", "installs": "2.5K"}]`

### Step 2: Import the skill
```bash
scripts/skills-sh.sh add owner/repo@skill-name
```
Imports to `~/.config/opencode/skills/{skill-name}/SKILL.md`.
Validation gate checks size (≤5KB), frontmatter, safety.

### Step 3: Load the skill
```
mcp_skill("skill-name")
```
**Note**: Imported skills available in NEXT session (cache init at startup).

### Guardrails
- **Max 1 import per session** — do not chain imports
- **Validation gate enforced** — no manual review needed
- **70% confidence threshold** — only import when highly confident

---

## Execution Rules

1. **Classify Context FIRST** - Before tools, classify request context.
2. **Auto-select Internal Skills** - Match keywords to matrix.
3. **Inject load_skills** - Ensure selected skills are injected.
4. **Identify External Gaps** - If local skills insufficient, use scripts/skills-sh.sh (max once).
5. **Phase 0 Gate** - Prevents proceeding without coverage.

---

## Anti-Patterns

❌ Proceeding without domain skills loaded  
❌ Manual skill loading when discovery is possible  
❌ Importing external skills more than once per session  
❌ Loading irrelevant skills that waste context  

---

## Integration Points

- **Phase 0 gate** - Runs before all other processing.
- **Skill-auto-loader-config.jsonc** - Source of truth.
- **Universal Skill** - Loaded by default.

## KB Reference

`~/vaults/baphled/3. Resources/Knowledge Base/AI Development System/Skills/Core-Universal/Skill Discovery.md`

## Related skills

- `agent-discovery` — routes to specialist agents
- `pre-action` — decision framework
