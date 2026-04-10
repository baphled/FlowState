@learning
Feature: Learning Capture via Mem0
  As an agent
  I want my tool interactions to be recorded
  So that the system can learn from my behaviour and outcomes

  Background:
    Given FlowState is running
    And the agent system is initialised

  @smoke
  Scenario: Record tool usage and outcome
    Given FlowState is configured with a Mem0 memory client
    When the agent uses the "read" tool
    And the tool execution returns a successful result
    Then a learning entry should be written to Mem0
    And the entry should include the tool name "read"
    And the entry should contain the result summary

  Scenario: Skip recording when memory client is unavailable
    Given FlowState is running without a Mem0 memory client
    When the agent uses a tool
    Then no learning record should be attempted
    And the agent should continue its task without error
