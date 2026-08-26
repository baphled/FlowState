Feature: Skill Auto-Loading
  As an agent orchestrator
  I want skills to be injected automatically into the system prompt
  So that agents always have the right capabilities for each task

  @skill-loading
  Scenario: Baseline skills are always injected
    Given the agent system is initialised
    When a new agent session starts with any prompt
    Then the baseline skills should be present in the system prompt
    And the skills should include "pre-action" and "memory-keeper"

  @skill-loading
  Scenario: Agent-specific skills are injected from manifest
    Given an agent manifest specifies the skill "cucumber"
    When the agent is started
    Then the system prompt should include the skill "cucumber"

  @skill-loading
  Scenario: Keyword-matched skills are injected based on prompt content
    Given the prompt contains the keyword "database"
    When the agent session is created
    Then the system should inject the "golang-database" skill into the system prompt

  @skill-loading
  Scenario: Lean skill name injection format appears in system prompt
    Given the agent system is initialised
    When a skill is injected
    Then the system prompt should list the skill by its lean name only
    And the skill documentation should not be inlined in the prompt
