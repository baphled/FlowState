Feature: Read Tool Output Suppression
  As a user
  I want read tool results to be hidden from the chat view
  So that file contents do not clutter the conversation

  Background:
    Given FlowState is running

  @smoke
  Scenario: Read tool result is not displayed in the chat
    Given the AI uses the read tool on a file
    When the read tool returns the file contents
    Then the file contents should not appear in the chat view
    And other tool results should still be visible

  @smoke
  Scenario: Read tool error is still displayed in the chat
    Given the AI uses the read tool on a non-existent file
    When the read tool returns an error
    Then the error should appear in the chat view

  Scenario: Read tool result is suppressed on session reload
    Given a session exists where the AI used the read tool
    When the session is loaded
    Then the read tool result should not appear in the reloaded chat

  Scenario: Other tool results remain visible after a read
    Given the AI uses the read tool then the bash tool
    When both tools complete
    Then only the bash tool result should appear in the chat
    And the read tool result should not appear
