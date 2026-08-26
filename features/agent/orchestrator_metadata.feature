Feature: Orchestrator Metadata
  As a FlowState orchestrator
  I want agent metadata to drive my delegation sections
  So that adding agents automatically updates my prompt

  @smoke
  Scenario: Agent metadata loaded from markdown frontmatter
    Given a markdown agent file with orchestrator metadata
    When it is loaded from the markdown file for orchestrator testing
    Then the orchestrator metadata should contain the configured cost
    And the orchestrator metadata should contain the configured triggers

  Scenario: Dynamic delegation table built from registry metadata
    Given an agent registry with agents that have orchestrator metadata
    When the dynamic delegation table is built
    Then the table should list agents sorted alphabetically
    And the table should include cost and description columns

  Scenario: Key triggers section built from agent metadata
    Given an agent registry with agents that have key triggers
    When the key triggers section is built
    Then the section should list each agent's key trigger

  Scenario: Tool selection table built from agent metadata
    Given an agent registry with agents of different costs
    When the tool selection table is built
    Then agents should be sorted by cost with FREE first
    And the table should include agent name, cost, and description
