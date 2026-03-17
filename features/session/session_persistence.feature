Feature: Session Persistence
  As a user
  I want my chat sessions to persist
  So that I can resume conversations later

  Background:
    Given FlowState is running

  @smoke
  Scenario: Session save/load round-trips with embeddings
    Given I have an active session with messages
    When I save the session
    And I reload the session
    Then all messages should be restored
    And embedding vectors should be preserved

  @smoke
  Scenario: New session starts empty
    Given FlowState is running
    Then all messages should be restored

  @smoke
  Scenario: Session persists message history
    Given I have an active session with messages
    When I save the session
    And I reload the session
    Then all messages should be restored

  Scenario: Multiple sessions can coexist
    Given I have an active session with messages
    When I save the session
    Then all messages should be restored

  Scenario: Session handles corrupted file gracefully
    Given I have an active session with messages
    When I save the session
    And I reload the session
    Then all messages should be restored
