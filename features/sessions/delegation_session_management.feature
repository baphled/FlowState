Feature: Delegation session management
  As a user
  I want to see which agents have been delegated to
  So that I can inspect the work done in each session

  Background:
    Given a coordinator agent is configured
    And delegation is enabled

  @delegation-session @smoke
  Scenario: Delegated sessions appear in the picker
    Given the coordinator has delegated to an agent
    When I open the delegation picker
    Then I should see 1 delegated session

  @delegation-session
  Scenario: Multiple delegations all appear
    Given the coordinator has delegated to 3 different agents
    When I open the delegation picker
    Then I should see 3 delegated sessions

  @delegation-session
  Scenario: Picker is empty when no delegation has occurred
    Given no delegation has occurred
    When I open the delegation picker
    Then the picker should be empty

  @delegation-session
  Scenario: Child session contains delegation messages
    Given the coordinator has delegated to an agent with messages
    When I inspect the delegated session
    Then the session should contain the agent's messages

  @delegation-session @smoke
  Scenario: Child session is linked to parent
    Given the coordinator has delegated to an agent
    Then the delegated session should reference the parent session
