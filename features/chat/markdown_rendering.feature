@chat @tui
Feature: Markdown Rendering in TUI
  Scenario: AI response rendered with markdown in TUI
    Given FlowState TUI is running
    When the AI sends a response with a code block
    Then the response should be rendered with syntax highlighting
