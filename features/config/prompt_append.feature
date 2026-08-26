Feature: Prompt Append Configuration
  As a FlowState user
  I want to append custom instructions to agent prompts via config
  So that I can customise agent behaviour without editing agent files

  @smoke
  Scenario: Prompt append from config is added to system prompt
    Given an agent with a base system prompt for config
    And a config with prompt_append for that agent
    When the system prompt is built for config
    Then the system prompt should contain the appended text
    And the appended text should appear at the end

  Scenario: No prompt append leaves system prompt unchanged
    Given an agent with a base system prompt for config
    And no prompt_append configured for that agent
    When the system prompt is built for config
    Then the system prompt should not be modified

  Scenario: Prompt append for different agent is ignored
    Given an agent "explorer" with a base system prompt for config
    And a config with prompt_append for agent "planner"
    When the system prompt is built for "explorer" for config
    Then the explorer system prompt should not contain the planner's append text

  Scenario: Multiple agents have different prompt appends
    Given agents "executor" and "explorer" with base system prompts for config
    And a config with prompt_append for "executor"
    And a config with prompt_append for "explorer"
    When system prompts are built for both agents for config
    Then executor's prompt should contain its append text
    And explorer's prompt should contain its append text
    And they should not contain each other's append text
