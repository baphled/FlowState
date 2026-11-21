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
  by `providers.anthropic.auth_type`: `oauth` (the default) or `api_key` (the API-key route below).
  There is no `claude-code` provider name.

1. One process per request: `claude -p --output-format stream-json --verbose --include-partial-messages
   --no-session-persistence --tools "" --strict-mcp-config --setting-sources "" --model <model>`, plus
   `--system-prompt=<text>`, which is always sent and is empty when the request has no system message, so the
   child never runs under Claude Code's own agent prompt. The prompt goes to stdin, never argv. The working
   directory is a fresh private directory (`os.MkdirTemp`, mode 0700) per request, removed when the turn ends, never
   the shared temp directory itself, so a planted `CLAUDE.md` there cannot reach the model. Only a directory the
   default factory created in that same call is removed (and only if it is still a direct child of the system temp
   directory named `flowstate-claude-*`); a directory supplied by an injected factory belongs to its caller and is
   never removed, and one that is empty, relative or not clean is refused before launch. The environment is
   inherited unchanged. `--bare` is not passed because it skips the keychain read that sign-in relies on.
2. The binary is `claude` on PATH, overridable with `FLOWSTATE_CLAUDE_BIN`. Resolving the provider never runs the
   binary or checks sign-in. A `providers.yaml` that exists but cannot be read or parsed fails resolution with that
   error instead of silently selecting the subscription route; an absent file is the default configuration. The
   file is this CLI's own: `config.yaml` in the same directory belongs to the older FlowState harness, has a
   different schema, and is never read (it made every `anthropic` session fail on a machine with both). A
   missing binary is reported with its path in double quotes (backslashes unchanged), so a path with spaces is
   unambiguous.
3. Hard rules (OAuth route): FlowState never reads, stores, refreshes or forwards any Claude credential or token;
   never reads `~/.claude`; never sends a request to Anthropic itself from this provider; never sets or imitates
   Claude Code identity headers.
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
   a missing, null, `end_turn` or `stop_sequence` `stop_reason` becomes `stop`, `max_tokens` becomes `length`,
   and any other value passes through, as on the API-key route.
   A context that is already cancelled starts no process, and `Chat` then returns `context.Canceled`. Cancelling
   during a turn, including while a held-back result waits for the reap, drops the result: `Chat` returns
   `context.Canceled` and `Stream` delivers nothing more. A `Launcher` that returns neither a process nor an
   error ends the turn with `ErrNoProcess`. `WithMaxLine` and `WithResultGrace` ignore non-positive values and
   keep their defaults. The reader returned by `Process.Stdout` must tolerate `Close` more than once,
   concurrently with `Read` and after `Wait`.
8. Launching follows the `exec.run` tool: the executable is resolved to an absolute path first and the command is
   built directly, with no shell. The model name must start with a letter or digit and use only letters, digits and
   `._:[]-`, and the system prompt is passed as `--system-prompt=<text>`, so neither can be read as another flag.
   The trust basis is those argv guards and an operator-controlled binary, not the path resolution. The launcher
   builds `exec.Cmd` as a literal because the security gate reports any `exec.Command` call with variable
   arguments and accepts no waivers; it must not be simplified back to `exec.Command`.

## API-key route

`auth_type: api_key` selects `anthropic.APIKey`, a second and fully independent route: ordinary Anthropic API
billing with a key the user created, not the Claude subscription. The hard rules of the OAuth route do not apply to
it and it shares nothing with it: it never reads the Claude Code sign-in or `~/.claude`, never runs `claude`, and
sends no OAuth, `Authorization` or `anthropic-beta` header, only `x-api-key`, `anthropic-version: 2023-06-01` and
content headers, to `<base>/v1/messages`.

1. Key: `ANTHROPIC_API_KEY`, else `providers.anthropic.api_key`. A missing key fails at resolve time with an error
   naming both sources. The key is never written to a log line or event. In an error, every exact occurrence of
   the key in the API error type and message, a transport error or a stream error is replaced by `[redacted]`; an
   altered form of the key (partial, encoded) is not detected. Resolving makes no network request.
2. Base URL: `ANTHROPIC_BASE_URL`, else `providers.anthropic.host`, else `https://api.anthropic.com` (the override
   serves tests and proxies).
3. Design: `APIKey` composes the existing Messages wire client in `internal/provider/zai` (anthropic coding route).
   Two opt-in options, off by default so the z.ai route is unchanged apart from item 5: `WithAnthropicSystem` (system
   messages go in the top-level `system` field; a request of only system messages is refused before any request)
   and `WithAnthropicFinish` (a completed stream ends with a finish event carrying the stop reason). `APIKey` adds
   the default model and `anthropic`-named errors (`anthropic: api error <status> <type>: <message>`). With no
   injected client it does not follow redirects, so a 3xx is an API error and `x-api-key` is never re-sent to
   another host. Tool refusal is `ErrUnsupportedTools`, as on the OAuth route.
4. Models: `claude-sonnet-5-5`, `claude-haiku-5-5`, `claude-opus-5-5` are advisory and never validated.
5. Stream failures: the shared client turns an SSE `error` event, a body read error, an EOF before `message_stop`
   and an empty or non-event-stream 200 body into one terminal error event and no finish event. This also applies to
   the z.ai anthropic route, which previously reported those cases as a completed turn. `stop_reason` maps to the
   finish reason: `end_turn`, `stop_sequence` or absent: `stop`; `max_tokens`: `length`; others unchanged.
6. Unsupported: structured tools and tool-role history (refused before any request); non-text blocks are ignored;
   token usage is not reported; `max_tokens` is clamped to 8192..131072; no retries.

## Consequences

### Negative (OAuth route)

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
   command line holds 32,767 UTF-16 units and quoting can double each byte; a spec builds the worst-case argv and
   fails if the limit is raised past that); a larger one is rejected before launch. The user's prompt goes to stdin.
   Follow-up: switch to `--system-prompt-file` once it is confirmed to be honoured by a signed-in Claude Code.
7. **Accepted limits.** Only the direct child is killed, not its process group, so its descendants survive.
   A descendant that holds stdout open does not delay the call, because stdout is closed on cancellation; one
   that holds stderr open delays the reap, and with it delivery of the result, by up to the launcher's 5 s
   `WaitDelay` (`DefaultWaitDelay`; `NewExecLauncher` takes another bound, which the specs use). The
   child inherits the whole environment, secrets included, because sign-in depends on it. A relative
   `FLOWSTATE_CLAUDE_BIN` is resolved against the working directory of FlowState. `tool_use` blocks are ignored.
   A request with no system message runs with an empty system prompt. It first ran under Claude Code's default
   agent prompt; the first signed-in turns showed the model writing tool-call markup as text for tools this route
   switches off, and a prompt about five times larger.
   A real `claude` may use an inherited `ANTHROPIC_API_KEY` and bill the API instead of the subscription.

### Negative (API-key route)

1. Text only: no structured tools, no usage reporting, no retries, redirects not followed.
2. A truncated stream is an error; truncation at `max_tokens` is finish reason `length`.

### Follow-ups

1. Refuse anthropic subscription sessions when `serve` has no token or a non-loopback bind (not implemented).

### Neutral

1. Process launching is an injected seam, so parsing, mapping and argv are unit-tested without a real process on every
   OS. A signed-in turn is not exercised by CI.
