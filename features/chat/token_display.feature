@chat @tui
Feature: Token Display in StatusBar
  Scenario: Token count shown in StatusBar during streaming
    Given FlowState TUI is running
    When the AI is streaming a response
    Then the StatusBar should show the current token count
