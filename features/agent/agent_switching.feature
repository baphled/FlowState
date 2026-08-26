Feature: Agent Switching
  As a user
  I want to switch between different agents
  So that I can get specialized help for different tasks

  Background:
    Given FlowState is running

  @smoke
  Scenario: Switch from general to coder agent
    Given I am chatting with the "general" agent
    When I switch to the "coder" agent
    Then the active agent should be "coder"

  @smoke
  Scenario: Switch preserves session context
    Given I am chatting with the "general" agent
    And I have sent the message "My name is Alice"
    When I switch to the "coder" agent
    And I type "What is my name?"
    Then I should see a complete response

  Scenario: Invalid agent switch handled gracefully
    Given I am chatting with the "general" agent
    When I switch to the "nonexistent" agent
    Then the active agent should be "general"
