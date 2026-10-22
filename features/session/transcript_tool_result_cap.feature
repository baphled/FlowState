@session
Feature: Session transcript tool result cap

  Tool results appended to the session transcript accumulator are
  capped head-first with a visible truncation marker mirroring the
  engine's in-flight cap, so oversized results cannot bloat session
  sidecars to multi-hundred-KB sizes.

  Scenario: Oversized transcript result is truncated with a visible marker
    Given a tool result of 100000 chars destined for the session transcript
    When the transcript tool result cap is applied
    Then the transcript content is at most 25000 chars
    And the transcript content carries the truncation marker

  Scenario: Under-cap transcript result passes through untouched
    Given a tool result of 1000 chars destined for the session transcript
    When the transcript tool result cap is applied
    Then the transcript content is identical to the original result

  Scenario: Transcript cap is idempotent across repeated passes
    Given a tool result of 100000 chars destined for the session transcript
    When the transcript tool result cap is applied
    Then the transcript content carries the truncation marker
    And the transcript content carries exactly one truncation marker
