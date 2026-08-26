---
name: knowledge-base
description: Query memory graph and vault before investigating codebase
category: Core Universal
tier: core
when_to_use: Before investigating, when looking for patterns, when solving similar problems
related_skills:
  - memory-keeper
  - note-taking
---
# Knowledge-Base Skill

**Always query memory and vault before reading files or running shell commands.** Use this fixed lookup order on every task:

1. **Memory** — `mcp_memory_search_nodes` with a topic query. If results cover the question, use them.
2. **Vault** — `mcp_vault-rag_query_vault` for KB docs, notes, and architecture context.
3. **Skill** — `skill_load` for domain-specific procedural guidance.
4. **Codebase** — filesystem reads and grep only if the above didn't answer the question.

Never skip steps 1 and 2. Even a partial memory hit prevents re-discovering what's already known.

After finding an answer, use `mcp_memory_open_nodes` to pull the full entity when you need exact observations.

Store all new discoveries immediately with the `memory-keeper` skill so the next agent (or next session) benefits from your work.
