@engine
Feature: In-flight tool result cap

  Tool results appended to the live messages slice mid-loop are capped
  head-first with a visible truncation marker, composing safely with the
  persist cap.

  Scenario: Oversized mid-loop result is truncated with a visible marker
    Given a tool result of 100000 chars destined for the in-flight messages
    When the in-flight tool result cap is applied
    Then the capped content is at most 25000 chars
    And the capped content carries the truncation marker

  Scenario: Under-cap result passes through untouched
    Given a tool result of 1000 chars destined for the in-flight messages
    When the in-flight tool result cap is applied
    Then the capped content is identical to the original result

  Scenario: Already-capped result passes through without a double marker
    Given a tool result of 100000 chars already capped by the persist cap
    When the in-flight tool result cap is applied
    Then the capped content carries exactly one truncation marker
