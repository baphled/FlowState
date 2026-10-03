@engine
Feature: Tool-use-no-calls streak fails over instead of retrying the same model

  Per the 2026-09-30 Tool Budget Exhaustion RCA: when a provider
  announces stop_reason "tool_use" but emits zero tool calls, the
  engine must not keep retrying the same provider/model pair in-session
  until the iteration budget burns out. After a streak of two such
  rounds the pair is marked unhealthy for the turn and the retry
  routes to the next failover candidate.

  Scenario: A streak of two tool_use_no_calls rounds fails over to the next candidate
    Given a primary provider that answers tool_use with zero tool calls
    And a fallback provider that answers a clean end_turn
    When the turn is streamed through the failover chain
    Then the fallback provider completes the turn
    And the primary provider is not retried beyond the streak threshold of two

  Scenario: A single tool_use_no_calls round stays on the same provider
    Given a primary provider whose first round emits tool_use without calls and whose second round succeeds
    And a fallback provider that answers a clean end_turn
    When the turn is streamed through the failover chain
    Then the fallback provider is never called
