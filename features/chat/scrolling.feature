Feature: Chat viewport scrolling
  As a user
  I want to control scrolling in the chat viewport
  So that I can review previous messages while new content streams in

  Background:
    Given a chat intent is initialised with a window size of 80x24
    And the chat has enough messages to require scrolling

  @smoke
  Scenario: Manual scroll position is preserved during streaming
    Given the viewport is at the bottom
    When I scroll up with PgUp
    Then the viewport should not be at the bottom
    When a new stream chunk arrives
    Then the viewport should remain at my scrolled position

  @smoke
  Scenario: Keyboard scroll up disables auto-scroll
    Given the viewport is at the bottom
    When I press PgUp
    Then the viewport should not be at the bottom

  Scenario: Sending a message resets scroll to bottom
    Given the viewport is at the bottom
    When I scroll up with PgUp
    And I type "Hello" and press Enter
    Then the viewport should be at the bottom
