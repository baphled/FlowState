Feature: CLI Commands
  As a user
  I want FlowState CLI commands to expose consistent help and stub behaviour
  So that I can discover available entry points before the full implementations land

  Background:
    Given the FlowState CLI is available

  @smoke
  Scenario: Root command shows help with global flags and subcommands
    When I run "flowstate --help"
    Then I should see usage for "flowstate"
    And I should see the global flag "--config"
    And I should see the global flag "--agents-dir"
    And I should see the global flag "--skills-dir"
    And I should see the global flag "--sessions-dir"
    And I should see the subcommand "chat"
    And I should see the subcommand "serve"
    And I should see the subcommand "agent"
    And I should see the subcommand "skill"
    And I should see the subcommand "discover"
    And I should see the subcommand "session"

  @smoke
  Scenario: Chat command shows agent, message, model, and session flags
    When I run "flowstate chat --help"
    Then I should see usage for "flowstate chat"
    And I should see the local flag "--agent"
    And I should see the local flag "--message"
    And I should see the local flag "--model"
    And I should see the local flag "--session"

  Scenario: Serve command shows port and host flags
    When I run "flowstate serve --help"
    Then I should see usage for "flowstate serve"
    And I should see the local flag "--port"
    And I should see the local flag "--host"

  Scenario: Agent list subcommand prints a placeholder message
    When I run "flowstate agent list"
    Then I should see placeholder output containing "agent list stub"

  Scenario: Agent info subcommand accepts an agent name
    When I run "flowstate agent info coder"
    Then I should see placeholder output containing "agent info stub"
    And I should see placeholder output containing "coder"

  Scenario: Skill list subcommand prints a placeholder message
    When I run "flowstate skill list"
    Then I should see placeholder output containing "skill list stub"

  Scenario: Discover command accepts a positional message argument
    When I run "flowstate discover write tests for the CLI"
    Then I should see placeholder output containing "discover stub"
    And I should see placeholder output containing "write tests for the CLI"

  Scenario: Session list subcommand prints a placeholder message
    When I run "flowstate session list"
    Then I should see placeholder output containing "session list stub"

  Scenario: Session resume subcommand accepts a session identifier
    When I run "flowstate session resume session-123"
    Then I should see placeholder output containing "session resume stub"
    And I should see placeholder output containing "session-123"
