Feature: Basic Chat
  As a user
  I want to chat with an AI assistant
  So that I can get help with everyday tasks

  Background:
    Given FlowState is running
    And Ollama is available with model "llama3.2"

  @smoke
  Scenario: Send message and receive streaming response
    Given I am in insert mode
    When I type "What is 2 + 2?"
    And I press Enter
    Then I should see tokens appearing
    And I should see a complete response

  @smoke
  Scenario: Multi-turn conversation maintains context
    Given I have sent the message "My name is Alice"
    And I received a response
    When I type "What is my name?"
    And I press Enter
    Then I should see "Alice" in the response

  Scenario: Empty message is not sent
    Given I am in insert mode
    When I press Enter without typing
    Then no message should be sent
    And I should remain in insert mode

  Scenario: Long message wraps correctly
    Given I am in insert mode
    When I type a message longer than the screen width
    Then the message should wrap to multiple lines
    And the cursor should be visible

  Scenario: Cancel message with Escape
    Given I am in insert mode
    And I have typed "Draft message"
    When I press Escape
    Then I should be in normal mode
    And the input should be cleared

  Scenario: Edit message in external editor
    Given I am in insert mode
    When I press Ctrl+x
    Then my $EDITOR should open
    And when I save and exit
    Then the editor content should appear in the input
