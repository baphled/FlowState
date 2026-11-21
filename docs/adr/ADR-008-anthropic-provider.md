---
id: adr-anthropic-provider
title: ADR - Anthropic Provider
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
  - Anthropic Provider
  - anthropic provider
---

# ADR - Anthropic Provider

## Status

**Accepted** — 2026-11-20.

## Context

FlowState must be able to use a user's Claude subscription through the `anthropic`
  provider. Anthropic's published policy
  (<https://code.claude.com/docs/en/legal-and-compliance>, "Authentication and credential use") says third-party
  developers may not offer Claude.ai login in their own applications nor "collect, store, or intermediate Claude.ai
  credentials or session tokens — sign-in to a Claude account must complete through Anthropic's own flow". The same
  policy states that this does not prevent "an end user from signing in to the unmodified Claude Code binary with their
  own Claude subscription".

## Decision

**FlowState reaches a Claude subscription only by running the user's own, unmodified `claude` binary in headless
  print mode.** The `anthropic` provider's OAuth route (`anthropic.Subscription`,
  `internal/provider/anthropic`) implements
  `provider.Streamer`. The sign-in is the user's own OAuth sign-in inside the unmodified `claude` binary. Selection is
  by `providers.anthropic.auth_type`: `oauth` (the default) or `api_key` (not yet available: the registry returns a
  clear error naming `oauth` as the supported route). There is no `claude-code` provider name.

1. One process per request: `claude -p --output-format stream-json --verbose --include-partial-messages
   --no-session-persistence --tools "" --strict-mcp-config --setting-sources "" --model <model>`, plus
   `--system-prompt=<text>` when the request has system messages. The prompt goes to stdin, never argv. The working
   directory is a fresh private directory (`os.MkdirTemp`, mode 0700) per request, removed when the turn ends, never
   the shared temp directory itself, so a planted `CLAUDE.md` there cannot reach the model. Only a directory the
   default factory created in that same call is removed (and only if it is still a direct child of the system temp
   directory named `flowstate-claude-*`); a directory supplied by an injected factory belongs to its caller and is
   never removed, and one that is empty, relative or not clean is refused before launch. The environment is
   inherited unchanged. `--bare` is not passed because it skips the keychain read that sign-in relies on.
2. The binary is `claude` on PATH, overridable with `FLOWSTATE_CLAUDE_BIN`. Resolving the provider never runs the
   binary or checks sign-in.
3. Hard rules: FlowState never reads, stores, refreshes or forwards any Claude credential or token; never reads
   `~/.claude`; never sends a request to Anthropic itself from this provider; never sets or imitates Claude Code
   identity headers.
4. Models are the Claude Code aliases `sonnet`, `opus`, `haiku`; the request model is passed through unchanged
   (a full model id works) and defaults to `sonnet`.
5. A signed-out Claude Code (`api_error_status` 401 or `authentication_failed`) yields an error telling the user to
   run `claude` and use `/login`.
6. The stdin prompt is always the labelled `User:` / `Assistant:` transcript, even for a single message. Claude Code
   runs a prompt whose first byte is `/` as a local slash command (verified: `/usage` returned local text with no
   model call), while `User: /usage` reaches the model. Empty or whitespace-only prompts are rejected before launch.
7. Stdout lines are capped at 16 MiB (a longer line, newline not counted, fails the turn and kills the child).
   Cancelling the request kills the child and closes its stdout, so a descendant holding the pipe open cannot
   keep a read blocked. After a `result` line the
   turn is over: the child gets 5 s to exit and is then killed, so the wait is bounded. A `result` whose
   `subtype` is present and not `success` is an error even when `is_error` is false; the recorded signed-out result
   (`subtype` `success`, `is_error` true) stays an authentication error. A finish event always names a reason:
   a missing or null `stop_reason` becomes `stop`.
8. Launching follows the `exec.run` tool: the executable is resolved to an absolute path first and the command is
   built directly, with no shell. The model name must start with a letter or digit and use only letters, digits and
   `._:[]-`, and the system prompt is passed as `--system-prompt=<text>`, so neither can be read as another flag.
   The trust basis is those argv guards and an operator-controlled binary, not the path resolution. The launcher
   builds `exec.Cmd` as a literal because the security gate reports any `exec.Command` call with variable
   arguments and accepts no waivers; it must not be simplified back to `exec.Command`.

The API-key route (`APIKey`, selected by `auth_type: api_key`) is a separate, later packet and is out of scope here.

## Consequences

### Negative

1. **No tools yet.** Any request with `Tools`, tool calls or tool-role messages fails closed before a process starts;
   Claude Code's own tools are disabled (`--tools ""`).
2. **Lossy prompt mapping.** Every request, including a single message, is rendered as a plain `User:` /
   `Assistant:` transcript. Message content is not escaped, so a message can forge a label.
3. **`MaxTokens` is ignored.** The CLI cannot express it; FlowState's own budget enforcement on the stream applies.
4. **The user must install and sign in to Claude Code.** A missing binary or signed-out state is a provider error with
   the fix in the message.
5. **Sharing is not enforced.** FlowState does not pool or resell a subscription, but it does not enforce
   single-user use either: `flowstate serve` runs an anthropic session for any caller who can reach it. The
   operator must keep the default loopback bind and set `FLOWSTATE_SERVE_TOKEN`; letting other people reach it
   would route their requests through the owner's plan, which the policy does not permit.
6. **The system prompt is visible in the process list** while the turn runs, and is limited to 12 KiB (the Windows
   command line holds 32,767 UTF-16 units and quoting can double each byte); a larger one is rejected before
   launch. The user's prompt goes to stdin.
   Follow-up: switch to `--system-prompt-file` once it is confirmed to be honoured by a signed-in Claude Code.
7. **Accepted limits.** Only the direct child is killed, not its process group, so its descendants survive.
   A descendant that holds stdout open does not delay the call, because stdout is closed on cancellation; one
   that holds stderr open delays the reap, and with it delivery of the result, by up to the launcher's 5 s
   `WaitDelay`. The
   child inherits the whole environment, secrets included, because sign-in depends on it. A relative
   `FLOWSTATE_CLAUDE_BIN` is resolved against the working directory of FlowState. `tool_use` blocks are ignored.
   A request with no system message runs under Claude Code's own default system prompt.

### Follow-ups

1. Refuse anthropic subscription sessions when `serve` has no token or a non-loopback bind (not implemented).

### Neutral

1. Process launching is an injected seam, so parsing, mapping and argv are unit-tested without a real process on every
   OS. A signed-in turn is not exercised by CI.
