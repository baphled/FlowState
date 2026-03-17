Feature: Vim Navigation
  As a user familiar with vim
  I want to use vim motions to navigate
  So that I can efficiently browse conversations

  Background:
    Given FlowState is running
    And I have a conversation with multiple messages
    And I am in normal mode

  @legacy
  Scenario: Basic vertical navigation
    When I press "j"
    Then the viewport should scroll down one line
    When I press "k"
    Then the viewport should scroll up one line

  @legacy
  Scenario: Jump to top and bottom
    When I press "G"
    Then I should be at the bottom of the conversation
    When I press "gg"
    Then I should be at the top of the conversation

  Scenario: Half page scrolling
    When I press Ctrl+d
    Then the viewport should scroll down half a page
    When I press Ctrl+u
    Then the viewport should scroll up half a page

  Scenario: Full page scrolling
    When I press Ctrl+f
    Then the viewport should scroll down a full page
    When I press Ctrl+b
    Then the viewport should scroll up a full page

  Scenario: Arrow keys work in normal mode
    When I press the down arrow
    Then the viewport should scroll down one line
    When I press the up arrow
    Then the viewport should scroll up one line

  @legacy
  Scenario: Enter insert mode with i
    When I press "i"
    Then I should be in insert mode
    And the cursor should be in the input area

  Scenario: Enter insert mode with a
    When I press "a"
    Then I should be in insert mode

  Scenario: Exit insert mode with Escape
    Given I am in insert mode
    When I press Escape
    Then I should be in normal mode

  Scenario: Search with forward slash
    When I press "/"
    Then I should be in search mode
    And the search input should be visible

  Scenario: Navigate search results
    Given I have searched for "hello"
    And there are multiple matches
    When I press "n"
    Then I should jump to the next match
    When I press "N"
    Then I should jump to the previous match

  Scenario: Exit search with Escape
    Given I am in search mode
    When I press Escape
    Then I should be in normal mode
    And the search should be cleared
