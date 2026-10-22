---
name: memory-keeper
description: Capture discoveries and learnings into searchable knowledge graph
category: Core Universal
tier: core
when_to_use: After solving bugs, discovering patterns, investigating issues, learning new techniques
related_skills:
  - pre-action
  - knowledge-base
---
# Memory-Keeper Skill

**Read before write.** Before any investigation, call `mcp_memory_search_nodes` with a relevant query. If it returns results, use them — don't re-discover what's already captured.

## Lookup sequence (mandatory)
1. `mcp_memory_search_nodes` — search by topic, symptom, or error message.
2. `mcp_memory_open_nodes` — retrieve full entity details when a hit looks relevant.
3. Only proceed to filesystem / bash if memory returned nothing useful.

## Capture sequence (after solving)
Systematically capture problem-solution pairs, patterns, and common mistakes so the next agent benefits:
- What the problem was and what caused it (the WHY, not just WHAT).
- The fix or pattern, with enough context to reproduce it.
- Which files/lines were involved.
- Any gotchas or edge cases discovered along the way.

Make findings searchable: use clear terminology, include error message substrings, and entity names that a future query would naturally use.

Use this skill after solving difficult bugs, discovering new patterns, investigating complex issues, or learning something that took significant time. Store all discoveries immediately to prevent context loss.
