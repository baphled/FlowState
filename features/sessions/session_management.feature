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

  Scenario: Fork a session
    Given I am in a session with history
    When I fork the session at message 3
    Then a new session should be created
    And it should contain messages 1 through 3
    And the original session should be unchanged

  Scenario: Delete a session
    Given I have a session named "Old Session"
    When I delete "Old Session"
    Then it should no longer appear in the session list
    And I should be prompted to confirm deletion

  @enrichment
  Scenario: Session includes system prompt and skills
    Given I have an active session with messages
    And the session has a system prompt and loaded skills
    When I save the session
    And I reload the session
    Then the session should contain a non-empty system prompt
    And the session should contain loaded skills

  @enrichment
  Scenario: Session includes agent ID
    Given I have an active session with messages
    And the session has an agent ID of "planner"
    When I save the session
    And I reload the session
    Then the session should contain agent ID "planner"

  @enrichment
  Scenario: Old session loads without error
    Given an existing session file without enrichment fields
    When I load the legacy session
    Then the session should load successfully
    And the system prompt should be empty
    And the loaded skills should be empty

  @session-isolation
  Scenario: New session starts with empty context
    Given I have a session store with existing messages
    When I create a new empty context store
    Then the new session should have no messages

  @tool-calls
  Scenario: Tool calls are visible when loading a session
    Given a session was saved with tool call messages
    When I load the session
    Then the loaded session should contain the tool call message

  @tool-output
  Scenario: Tool output is visible when loading a session
    Given a session was saved with tool result messages
    When I load the session
    Then I should see the tool result in the chat view

  @skill-visibility
  Scenario: Skill loads are visible when loading a session
    Given a session was saved with skill load messages
    When I load the session
    Then I should see the skill load in the chat view

   @tool-error
   Scenario: Failed tool calls show error status when loading a session
     Given a session was saved with a failed tool result
     When I load the session
     Then I should see the tool error in the chat view

   @streaming-tool-output
   Scenario: Tool output is visible in live chat during streaming
     Given the engine executes a tool that produces output
     When the stream is processed
     Then the tool result should be visible in the chat
