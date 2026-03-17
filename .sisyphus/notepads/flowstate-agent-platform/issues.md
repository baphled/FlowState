# Issues

## 2026-03-17 Session: ses_302a3f473ffei4QswQo2BTlywI
No issues yet — session starting.

## Background Agent Delegation Not Working

**Date**: 2026-03-17
**Impact**: HIGH - blocks normal workflow

Background agents (hephaestus) launch but never produce output:
- Tasks show "running" but 0 messages
- Timeouts after 5+ minutes with no progress
- Had to implement T1 and T18 directly

**Workaround**: Team-Lead implementing infrastructure tasks directly.
For complex implementation tasks, may need synchronous oracle agent calls.
