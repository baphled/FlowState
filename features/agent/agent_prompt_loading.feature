Feature: Agent Prompt Loading
  As a FlowState user
  I want agents to load their embedded prompts
  So that they have the necessary instructions and context to operate

  Scenario: Planner agent loads comprehensive embedded prompt
    Given the planner agent is configured
    When the system prompt is built
    Then the prompt should contain planning instructions
    And the prompt size should be at least 5000 characters

  Scenario: Executor agent loads comprehensive embedded prompt
    Given the executor agent is configured
    When the system prompt is built
    Then the prompt should contain execution instructions
    And the prompt size should be at least 5000 characters

  Scenario: Agent picker shows planner and executor
    Given the FlowState TUI is running
    When I open the agent picker
    Then I should see "planner" in the agent list
    And I should see "executor" in the agent list

  Scenario: Switching agent rebuilds engine with new prompt
    Given the FlowState TUI is running with the planner agent
    When I switch to the executor agent
    Then the active agent should be "executor"
