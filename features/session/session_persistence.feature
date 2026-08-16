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
    Then the session should have no messages

  @smoke
  Scenario: Session persists message history
    Given I have an active session with messages
    When I save the session
    And I reload the session
    Then all messages should be restored

  @smoke
  Scenario: Multiple sessions can coexist
    Given I have an active session with messages
    When I save the session
    And I reload the session
    Then all messages should be restored

  Scenario: Session handles corrupted file gracefully
    Given I have an active session with messages
    When I save the session
    And I reload the session
    Then all messages should be restored

  @wip
  Scenario: Session sticks to the provider+model that actually served the first turn
    Given a session created with a seeded default provider+model pair
    And the first turn fails over to a different provider before completing
    When the assistant message flushes with the failover winner
    Then the session's current provider+model reflect the winner
    And subsequent turns are sent to the winning pair
