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

## F3 QA: io.Discard Bug in `flowstate chat --output json`

**Date**: 2026-03-26
**Impact**: HIGH - blocks F3 acceptance scenarios S1 and S2
**Discovered by**: QA-Engineer during F3 Plan Output Quality verification

### Root Cause

`internal/cli/chat.go` line 111:
```go
response, err := streamChatResponse(application, agentName, opts.Message, opts.Output, io.Discard)
```

The `--output json` flag correctly creates a `JSONConsumer` inside `streamChatResponse`, 
but the consumer's writer is `io.Discard` — JSON events are silently discarded.
Plain text response is then written via `fmt.Fprintf(cmd.OutOrStdout(), "Response: %s\n", response)`.

### Fix Required

Pass `cmd.OutOrStdout()` as the writer when `--output json`:
```go
writer := io.Discard
if opts.Output == "json" {
    writer = cmd.OutOrStdout()
}
response, err := streamChatResponse(application, agentName, opts.Message, opts.Output, writer)
```
Or pass the writer into `streamChatResponse` from `runSingleMessageChat` based on output format.

## F3 QA: Scenario 2 Grep Pattern Wrong Event Type Names

**Date**: 2026-03-26
**Impact**: MEDIUM - scenario spec defect, F3 S2 permanently fails even if S1 fix applied
**Discovered by**: QA-Engineer

The F3 scenario 2 grep uses Go struct names:
```
"DelegationEvent", "PlanArtifactEvent", "ReviewVerdictEvent", "StatusTransitionEvent"
```

But `internal/streaming/events.go` defines actual JSON type values as:
```
"delegation", "plan_artifact", "review_verdict", "status_transition"
```

Corrected grep pattern:
```bash
grep -c '"type":"delegation"\|"type":"plan_artifact"\|"type":"review_verdict"\|"type":"status_transition"' /tmp/plan-output-json.txt
```

## F3 QA: API Session JSON Fields Use PascalCase

**Date**: 2026-03-26
**Impact**: LOW - documentation inconsistency, causes plan scenario's Python snippet to fail
**Discovered by**: QA-Engineer

The F3 Scenario 3 Python snippet uses `json.load(sys.stdin)['id']` (lowercase),
but the actual API returns `{"ID":"...","AgentID":"..."}` (PascalCase from Go default marshalling).
Plan should be updated to use `['ID']` or the API should add `json:"id"` struct tags.
