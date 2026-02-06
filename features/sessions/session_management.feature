Feature: Session Management
  As a user
  I want to manage my chat sessions
  So that I can organize and resume conversations

  Background:
    Given FlowState is running

  @smoke
  Scenario: New session is created on startup
    When FlowState starts with no arguments
    Then a new session should be created
    And the session should have no messages

  @smoke
  Scenario: Session persists messages
    Given I have started a new session
    When I send the message "Hello"
    And I receive a response
    And I quit FlowState
    And I restart FlowState
    Then I should see my previous session
    And it should contain "Hello"

  Scenario: List available sessions
    Given I have multiple sessions
    When I open the session browser
    Then I should see a list of sessions
    And each session should show its title
    And sessions should be sorted by last updated

  Scenario: Switch to another session
    Given I have multiple sessions
    And I am in session "Session A"
    When I open the session browser
    And I select "Session B"
    Then I should be in session "Session B"
    And I should see the messages from "Session B"

  Scenario: Create new session
    Given I am in an existing session
    When I create a new session
    Then I should be in a fresh session
    And the session should have no messages

  Scenario: Auto-generate session title
    Given I have started a new untitled session
    When I send the message "Help me plan a trip to Japan"
    And I receive a response
    Then the session title should be auto-generated
    And the title should be relevant to the conversation

  Scenario: Search sessions
    Given I have sessions about various topics
    When I open the session browser
    And I search for "recipe"
    Then I should only see sessions mentioning "recipe"

  @wip
  Scenario: Fork a session
    Given I am in a session with history
    When I fork the session at message 3
    Then a new session should be created
    And it should contain messages 1 through 3
    And the original session should be unchanged

  @wip
  Scenario: Delete a session
    Given I have a session named "Old Session"
    When I delete "Old Session"
    Then it should no longer appear in the session list
    And I should be prompted to confirm deletion
