# Learnings

## 2026-03-17 Session: ses_302a3f473ffei4QswQo2BTlywI

### Worktree
- Working in: `/home/baphled/Projects/FlowState.git/agent-platform`
- Branch: `feature/agent-platform` (off `main` at 70f65c8)
- Protected branches: `main`, `next` — never modify directly

### Plan Adjustments Applied
1. Commit template: `AI_AGENT="Opencode" AI_MODEL="claude-opus-4.5"`
2. T19: 6 mandatory always-active skills must be in sample manifests: pre-action, memory-keeper, token-cost-estimation, retrospective, note-taking, knowledge-base
3. F4: KB Curator trigger added after scope verification passes

### Key Architecture Decisions
- TRUE RLM: LLM never gets full conversation — queries its own history via tools
- Embeddings use SEPARATE provider from chat (embeddingProvider reference in Engine)
- Anthropic has NO embedding API — falls back to recency-only
- No SQLite, no vector DB — file-backed only
- All channels buffered size 16
- float64 for embeddings (native Ollama return type)
- Tool output messages stored but NOT embedded (too noisy)

### Commit Convention
```bash
AI_AGENT="Opencode" AI_MODEL="claude-opus-4.5" make ai-commit FILE=/tmp/commit.txt
```
