Feature: Always-active skill bodies in system prompt
  As an agent orchestrator
  I want always-active skill bodies injected directly into the system prompt at build time
  So that no runtime skill_load tool call is needed to activate them

  @skills
  Scenario: Always-active skill bodies appear in the system prompt without any skill_load call
    Given an engine configured with always-active skills "pre-action" and "memory-keeper"
    When the system prompt is built for skills
    Then the system prompt should contain the full body of skill "pre-action"
    And the system prompt should contain the full body of skill "memory-keeper"
    And zero skill_load tool calls should be required to activate them
