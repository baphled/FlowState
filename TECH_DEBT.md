# Technical Debt Register

This document records the known technical debt items identified during the review cycle for the deterministic planning loop. It is intended to help future engineers understand the current risks, prioritise remediation, and separate low-risk maintenance from pre-deployment work.

**Last Updated:** 2026-03-27

## Debt Register

| ID | Title | Description | Severity | Owner | Resolution path |
| --- | --- | --- | --- | --- | --- |
| TD-01 | Background task eviction not wired | `BackgroundTaskManager.EvictCompleted()` exists and is tested, but it has no production callers. The tasks map can grow with total delegations per session, although the current circuit breaker keeps the impact limited. | LOW | Engine team | Call `EvictCompleted()` at the end of each planning loop iteration in `internal/engine/engine.go`. |
| TD-02 | No HTTP authentication | No authentication is enforced on any HTTP endpoint in `internal/api/server.go`. This is acceptable for local development, but it must be addressed before any network-accessible deployment. | MEDIUM (pre-deployment) | API team | Add authentication middleware to `setupRoutes()` before network deployment. |
| TD-03 | Request bodies are read without limits | `handleChat`, `handleCreateSession`, and `handleSessionMessage` do not use `http.MaxBytesReader`, which leaves them open to large payload abuse. | MEDIUM (pre-deployment) | API team | Wrap `r.Body` with `http.MaxBytesReader` in each handler. |
| TD-04 | WebSocket origin is not validated | `websocket.Accept(w, r, nil)` passes nil options in `internal/api/websocket.go:42`, which creates a Cross-Site WebSocket Hijacking risk. | MEDIUM (pre-deployment) | API team | Pass `&websocket.AcceptOptions{OriginPatterns: []string{"localhost:*"}}` or an equivalent validated origin policy. |
| TD-05 | Security response headers are missing | HTTP responses do not include CSP, `X-Content-Type-Options`, `X-Frame-Options`, or HSTS headers. | MEDIUM (pre-deployment) | API team | Add a security headers middleware to `setupRoutes()`. |
| TD-06 | Background task launch has no goroutine cap | `BackgroundTaskManager.Launch` does not enforce a concurrency limit, so runaway or adversarial delegation can create an unbounded number of goroutines. | MEDIUM (pre-deployment) | Engine team | Add a semaphore, such as `golang.org/x/sync/semaphore`, with a configurable cap and a default of 50. |
| TD-07 | IndexAgents godoc is inaccurate | The `IndexAgents` godoc in `internal/discovery/embedding.go` claims it returns an error if embedding fails for any agent, but the implementation continues on per-agent failures in a best-effort manner. | LOW | Discovery team | Update the godoc so it describes the best-effort behaviour accurately. |

## Notes

- Pre-deployment items must be resolved before any network-accessible release.
- Low-severity items should still be tracked to avoid misleading callers and to keep the codebase honest about its behaviour.
