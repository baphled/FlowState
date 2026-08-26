Feature: AGENTS.md Context Loading
  As a FlowState user
  I want AGENTS.md files to be included in the session context
  So that agents follow project-specific and global instructions

  @smoke
  Scenario: Global AGENTS.md is included in system prompt
    Given a global AGENTS.md exists with content "You are a helpful assistant"
    When a new session is started
    Then the system prompt should contain "You are a helpful assistant"

  @smoke
  Scenario: Working directory AGENTS.md is included in system prompt
    Given an AGENTS.md exists in the working directory with content "Follow project rules"
    When a new session is started
    Then the system prompt should contain "Follow project rules"

  Scenario: Both AGENTS.md files are merged into system prompt
    Given a global AGENTS.md exists with content "Global instructions"
    And an AGENTS.md exists in the working directory with content "Local instructions"
    When a new session is started
    Then the system prompt should contain "Global instructions"
    And the system prompt should contain "Local instructions"

  Scenario: No AGENTS.md files does not affect system prompt
    Given no AGENTS.md files exist
    When a new session is started
    Then the system prompt should not contain AGENTS.md content
