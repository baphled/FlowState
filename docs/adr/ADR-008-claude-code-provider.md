---
id: adr-claude-code-provider
title: ADR - Claude Code Provider
type: adr
status: accepted
adr_group: Go CLI
created: 2026-11-20
modified: 2026-11-20T00:00
deciders:
  - Engineering Team
consulted:
  - Architecture Review Board
informed:
  - FlowState Contributors
tags:
  - type/adr
  - project/flowstate
  - topic/architecture
  - topic/adr
  - topic/provider
  - flowstate/cli
  - flowstate/adr
  - timeline/november-2026
aliases:
  - Claude Code Provider
  - claude-code provider
---

# ADR - Claude Code Provider

## Status

**Accepted** — 2026-11-20.

## Context

FlowState must be able to use a user's Claude subscription. Anthropic's published policy
  (<https://code.claude.com/docs/en/legal-and-compliance>, "Authentication and credential use") says third-party
  developers may not offer Claude.ai login in their own applications nor "collect, store, or intermediate Claude.ai
  credentials or session tokens — sign-in to a Claude account must complete through Anthropic's own flow". The same
  policy states that this does not prevent "an end user from signing in to the unmodified Claude Code binary with their
  own Claude subscription".

## Decision

**FlowState reaches a Claude subscription only by running the user's own, unmodified `claude` binary in headless
  print mode.** The `claude-code` provider (`internal/provider/claudecode`) implements `provider.Streamer`:

1. One process per request: `claude -p --output-format stream-json --verbose --include-partial-messages
   --no-session-persistence --tools "" --strict-mcp-config --setting-sources "" --model <model>`, plus
   `--system-prompt` when the request has system messages. The prompt goes to stdin, never argv. The working directory
   is `os.TempDir()`; the environment is inherited unchanged. `--bare` is not passed because it skips the keychain
   read that sign-in relies on.
2. The binary is `claude` on PATH, overridable with `FLOWSTATE_CLAUDE_BIN`. Resolving the provider never runs the
   binary or checks sign-in.
3. Hard rules: FlowState never reads, stores, refreshes or forwards any Claude credential or token; never reads
   `~/.claude`; never sends a request to Anthropic itself from this provider; never sets or imitates Claude Code
   identity headers.
4. Models are the Claude Code aliases `sonnet`, `opus`, `haiku`; the request model is passed through unchanged
   (a full model id works) and defaults to `sonnet`.
5. A signed-out Claude Code (`api_error_status` 401 or `authentication_failed`) yields an error telling the user to
   run `claude` and use `/login`.

An API-key Anthropic provider is a separate, later decision and is out of scope here.

## Consequences

### Negative

1. **No tools yet.** Any request with `Tools`, tool calls or tool-role messages fails closed before a process starts;
   Claude Code's own tools are disabled (`--tools ""`).
2. **Lossy multi-turn mapping.** A single user message is the prompt; longer history is rendered as a plain
   `User:` / `Assistant:` transcript.
3. **`MaxTokens` is ignored.** The CLI cannot express it; FlowState's own budget enforcement on the stream applies.
4. **The user must install and sign in to Claude Code.** A missing binary or signed-out state is a provider error with
   the fix in the message.
5. **Each user uses their own sign-in.** The subscription is never shared or pooled through FlowState.

### Neutral

1. Process launching is an injected seam, so parsing, mapping and argv are unit-tested without a real process on every
   OS. A signed-in turn is not exercised by CI.
