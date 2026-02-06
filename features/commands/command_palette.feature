Feature: Command Palette
  As a user
  I want to access commands quickly
  So that I can efficiently use FlowState features

  Background:
    Given FlowState is running
    And I am in normal mode

  @smoke
  Scenario: Open command palette
    When I press Ctrl+p
    Then the command palette should open
    And I should see a search input
    And I should see a list of available commands

  @smoke
  Scenario: Filter commands by typing
    Given the command palette is open
    When I type "mod"
    Then I should see commands matching "mod"
    And "/models" should be visible
    And non-matching commands should be hidden

  Scenario: Execute command from palette
    Given the command palette is open
    When I type "help"
    And I press Enter
    Then the command palette should close
    And the "/help" command should execute

  Scenario: Navigate commands with arrows
    Given the command palette is open
    When I press the down arrow
    Then the next command should be highlighted
    When I press the up arrow
    Then the previous command should be highlighted

  Scenario: Navigate commands with j/k
    Given the command palette is open
    When I press "j"
    Then the next command should be highlighted
    When I press "k"
    Then the previous command should be highlighted

  Scenario: Close palette with Escape
    Given the command palette is open
    When I press Escape
    Then the command palette should close
    And I should be in normal mode

  Scenario: Show command descriptions
    Given the command palette is open
    Then each command should show its description
    And the description should explain what the command does

  Scenario: Recently used commands appear first
    Given I have previously used "/analyze"
    And the command palette is open
    Then "/analyze" should appear near the top
