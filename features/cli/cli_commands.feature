Feature: CLI Commands
  As a user
  I want FlowState CLI commands to expose consistent help and behaviour
  So that I can discover and use available entry points

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
     And I should see the subcommand "models"

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

  Scenario: Agent list subcommand shows agents or empty message
    When I run "flowstate agent list"
    Then I should see output containing "executor"

  Scenario: Agent info subcommand accepts an agent name
    When I run "flowstate agent info planner"
    Then I should see output containing "planner"

  Scenario: Skill list subcommand shows skills or empty message
    When I run "flowstate skill list"
    Then I should see output containing "skill"

  Scenario: Discover command accepts a positional message argument
    When I run "flowstate discover execute plan"
    Then I should see output containing "executor"

  Scenario: Session list subcommand shows sessions or empty message
    When I run "flowstate session list"
    Then I should see output containing "session"

   Scenario: Session resume subcommand accepts a session identifier
     When I run "flowstate session resume session-123"
     Then I should see output containing "session-123"
