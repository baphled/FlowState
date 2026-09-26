@engine @session
Feature: Truncation-aware stop-reason classification

  A turn that ended on a max_tokens/length stop signal was truncated
  mid-output, most likely before the announced tool call could be
  emitted. Such turns must not be classified as tool_use_no_calls —
  that sentinel blames the provider's wire contract when the real
  cause is our own output cap.

  Scenario: max_tokens stop with no tool calls is classified as truncation
    Given a turn on an untrusted provider that ended with upstream stop_reason "max_tokens"
    And the turn emitted no tool call and no delegation
    When the assistant message is flushed
    Then the message stop reason is "stream_truncated"

  Scenario: length stop with no tool calls is classified as truncation
    Given a turn on an untrusted provider that ended with upstream stop_reason "length"
    And the turn emitted no tool call and no delegation
    When the assistant message is flushed
    Then the message stop reason is "stream_truncated"

  Scenario: genuine tool_use finish without a tool call remains tool_use_no_calls
    Given a turn on an untrusted provider that ended with upstream stop_reason "tool_use"
    And the turn emitted no tool call and no delegation
    When the assistant message is flushed
    Then the message stop reason is "tool_use_no_calls"
