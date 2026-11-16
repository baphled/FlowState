# Retired OpenCode compatibility instructions

This legacy path is retained only to prevent accidental use of its former
commit instructions. It is **not an active FlowState agent skill**.

Do not invoke OpenCode, derive reviewer identity from Git configuration, append
AI attribution to commits, or use this file to select a reviewer. The current
instructions are [AGENTS.md](../../../AGENTS.md) and the
[native verifier dispatch guide](../../../docs/development/REVIEW-DISPATCH.md).
Missing native FlowState verification fails closed; it never selects an external
fallback or emits approval automatically.
