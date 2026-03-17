Feature: Agent Discovery
  As a user
  I want the system to suggest appropriate agents for my task
  So that I can get help from the most suitable specialist

  Background:
    Given FlowState is running

  @smoke
  Scenario: Discover agent for coding task
    Given the agent "coder" is available
    When I ask for agent suggestions for "write a function to parse JSON"
    Then I should receive agent suggestions
    And the suggestions should include an agent with confidence above 0.5

  @smoke
  Scenario: Discover agent for research task
    Given the agent "researcher" is available
    When I ask for agent suggestions for "find information about quantum computing"
    Then I should receive agent suggestions
    And the suggestions should include an agent with confidence above 0.5

  Scenario: No suitable agent found
    When I ask for agent suggestions for "something very obscure"
    Then I should receive agent suggestions
