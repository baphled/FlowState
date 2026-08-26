@session @failover
Feature: Session failure reason capture with provider health CLI (S5)

  When a session transitions to the failed state (via StopReasonStreamTruncated,
  StopReasonToolUseNoCalls, or StopReasonAbandonedTool) the session metadata
  captures the terminal stop reason as `failure_reason`. The `flowstate health`
  CLI exposes provider cooldown state for debugging.

  Background:
    Given FlowState is running with failover enabled

  Scenario: Failed session records failure_reason in meta.json
    Given a session is in "active" status
    When an assistant message arrives with StopReason "StopReasonStreamTruncated"
    Then the session status is "failed"
    And the meta.json contains "failure_reason" set to "StopReasonStreamTruncated"

  Scenario: Recovery demotion clears failure_reason
    Given a session has status "failed" and failure_reason "StopReasonToolUseNoCalls"
    When a healthy assistant message arrives
    Then the session status is demoted to "active"
    And failure_reason is cleared

  Scenario: Health CLI shows no cooldowns when healthy
    Given no providers are rate-limited
    When the user runs "flowstate health status"
    Then it prints "No providers are currently rate-limited."

  Scenario: Health CLI shows active cooldowns
    Given "anthropic" / "claude-sonnet-4" is rate-limited with 30s cooldown
    When the user runs "flowstate health status"
    Then the output includes "anthropic"
    And the output includes "claude-sonnet-4"
    And the output includes "30s"

  Scenario: Health CLI reset clears cooldown
    Given "openai" / "gpt-4o" is rate-limited with 60s cooldown
    When the user runs "flowstate health reset openai gpt-4o"
    Then "openai" / "gpt-4o" is no longer rate-limited
