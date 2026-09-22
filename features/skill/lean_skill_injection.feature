@skills @engine
Feature: Lean skill injection

  Invariant: skills are never injected as full bodies into any prompt
  build. Only a lean header naming available skills is injected; the
  model loads the bodies it needs via skill_load in-session.

  Scenario: System prompt carries skill names only, never bodies
    Given an engine configured with always-active skills "pre-action" and "memory-keeper"
    When the system prompt is built for skills
    Then the system prompt should contain the lean name header for skill "pre-action"
    And the system prompt should contain the lean name header for skill "memory-keeper"
    And the system prompt should not contain the full body of any skill

  Scenario: Skills-first gate auto-injection is lean
    Given an engine carries one always-active skill
    When the always-active turn is streamed once
    Then the first provider request names the always-active skill without its body

  Scenario: SetManifest re-resolution stays lean
    Given an engine configured with always-active skills "pre-action" and "memory-keeper" and a skills resolver for a second manifest with skill "memory-keeper"
    When the engine's manifest is swapped and the system prompt is rebuilt
    Then the rebuilt system prompt contains the lean name header for skill "memory-keeper"
    And the rebuilt system prompt does not contain the full body of any skill
