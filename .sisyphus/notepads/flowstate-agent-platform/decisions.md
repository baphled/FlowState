# Decisions

## 2026-03-17 Session: ses_302a3f473ffei4QswQo2BTlywI

### Worktree Strategy
- Decision: Use `feature/agent-platform` worktree (not `main`)
- Reason: Keeps main clean; all PoC work isolated on feature branch
- Impact: All commits go to feature/agent-platform

### Team-Lead Coordination
- Decision: Use Team-Lead agent to coordinate delivery
- Reason: User explicitly requested @Team-Lead
- Impact: Team-Lead assembles squad, sets merge gates, owns delivery risk
