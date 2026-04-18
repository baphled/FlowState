Feature: Bash Tool
  As a user
  I want the AI to execute bash commands
  So that it can help me with system tasks

  Background:
    Given FlowState is running
    And the bash tool is enabled

  @smoke
  Scenario: AI requests bash command with Ask permission
    Given bash tool permission is set to "ask"
    When the AI requests to run "ls -la"
    Then I should see a permission prompt
    And the prompt should show the command
    When I approve the command
    Then the command should execute
    And the AI should receive the output

  @smoke
  Scenario: Deny bash command execution
    Given bash tool permission is set to "ask"
    When the AI requests to run "rm -rf /"
    And I deny the command
    Then the command should not execute
    And the AI should be informed of the denial

  Scenario: Auto-allow with Allow permission
    Given bash tool permission is set to "allow"
    When the AI requests to run "pwd"
    Then the command should execute immediately
    And no permission prompt should appear

  Scenario: Block with Deny permission
    Given bash tool permission is set to "deny"
    When the AI requests to run "echo hello"
    Then the command should not execute
    And the AI should be informed that bash is disabled

  Scenario: Command output is displayed
    Given bash tool permission is set to "allow"
    When the AI runs "echo 'Hello World'"
    Then the output "Hello World" should appear in the chat
    And it should be formatted as command output

  Scenario: Command error is displayed
    Given bash tool permission is set to "allow"
    When the AI runs "ls /nonexistent"
    Then the error output should appear in the chat
    And it should be formatted as an error

  Scenario: Long running command shows progress
    Given bash tool permission is set to "allow"
    When the AI runs a command that takes several seconds
    Then I should see an indicator that the command is running
    And when complete, I should see the output

  Scenario: Cancel running command
    Given the AI is running a long command
    When I press Ctrl+k
    Then the stream is cancelled and a notification confirms
    And the AI should be informed of the cancellation

  Scenario: Working directory context
    Given I am in directory "/home/user/projects"
    When the AI runs "pwd"
    Then the output should show "/home/user/projects"

  @wip
  Scenario: Remember permission for session
    Given bash tool permission is set to "ask"
    When the AI requests to run "ls"
    And I approve with "remember for session"
    Then subsequent "ls" commands should auto-approve
