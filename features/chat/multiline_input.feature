Feature: Multiline Chat Input
  As a user
  I want to compose messages across multiple lines
  So that I can write structured or detailed prompts

  Background:
    Given FlowState is running
    And I am in the chat input

  @smoke
  Scenario: Alt+Enter inserts newline without sending
    When I type "first line"
    And I press Alt+Enter
    Then the input should contain a newline
    And no message should be sent to the AI

  @smoke
  Scenario: Input box grows by one row per newline
    When I type "line one"
    And I press Alt+Enter
    And I type "line two"
    Then the input display should show 2 lines
    And the message viewport should be reduced by 1 row

  Scenario: Enter sends the multiline message
    Given I have typed "first line" then pressed Alt+Enter then typed "second line"
    When I press Enter
    Then the message containing a newline should be sent
    And the input should be cleared

  Scenario: Backspace removes the newline character
    When I type "hello"
    And I press Alt+Enter
    And I press Backspace
    Then the input should equal "hello"
    And the input should contain no newline
