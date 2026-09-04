@learning
Feature: Learning Recall Bridge
  As an agent
  I want prior learnings recalled at session start
  So that my agent context is primed with what earlier sessions discovered

  Scenario: session start recalls prior learnings and injects them into agent context
    Given FlowState is running
    And the agent system is initialised
    And prior learnings have been recorded for agent "scribe"
    And a session-start learning adapter is wired to the recall broker
    When a session starts for agent "scribe"
    Then the adapter should recall the prior learnings from the recall broker
    And the prior learnings should be injected into the agent context
