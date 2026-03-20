@smoke @tui @provider-setup
Feature: Provider and MCP Setup
  As a FlowState user
  I want to configure providers and MCP servers
  So I can connect to the AI services I use

  @provider-list
  Scenario: View provider configuration status
    Given provider setup screen is shown
    Then I should see "Ollama" with configured status
    And I should see "OpenAI" with unconfigured status
    And I should see "Anthropic" with configured status
    And I should see "GitHub Copilot" with unconfigured status

  @provider-configure
  Scenario: Configure Anthropic provider
    Given provider setup screen is shown
    When I select "Anthropic" provider
    Then I should see API key input field
    When I enter "sk-ant-..." as API key
    And I press Escape
    Then config should be saved
    And provider should be marked as configured

  @provider-toggle
  Scenario: Toggle provider enabled state
    Given provider setup screen is shown
    And "Ollama" is configured
    When I press Enter on "Ollama"
    Then "Ollama" enabled state should toggle

  @mcp-list
  Scenario: View discovered MCP servers
    Given provider setup screen is shown
    And I am on MCP servers step
    Then I should see discovered MCP servers
    And each server should show enable/disable toggle

  @mcp-toggle
  Scenario: Toggle MCP server enabled state
    Given provider setup screen is shown
    And I am on MCP servers step
    When I press Enter on "memory" server
    Then "memory" enabled state should toggle

  @save-config
  Scenario: Save configuration on Escape
    Given provider setup screen is shown
    And I have configured providers
    And I have configured MCP servers
    When I press Escape
    Then config should be saved to config.yaml
    And I should be returned to chat view

  @step-navigation
  Scenario: Navigate between setup steps
    Given provider setup screen is shown
    When I press Tab
    Then I should move to next step
    When I press Shift+Tab
    Then I should move to previous step

  @opencode-credentials
  Scenario: Configure provider using OpenCode credentials
    Given provider setup screen is shown
    When I select "Anthropic" provider
    Then I should see credential input options:
      | Use OpenCode credentials |
      | Enter manually           |
    When I choose "Use OpenCode credentials"
    Then OpenCode auth.json should be checked
    And Anthropic provider should be marked as configured
    And credential source should be marked as "OpenCode"

  @manual-credentials
  Scenario: Configure provider with manual credential entry
    Given provider setup screen is shown
    When I select "Anthropic" provider
    And I choose "Enter manually"
    Then I should see API key input field
    When I enter "sk-ant-api03-test123" as credential
    And I press Escape
    Then credential should be saved to config
    And provider should be marked as configured
    And credential source should be marked as "Manual"

  @credential-validation
  Scenario Outline: Validate credential format by provider
    Given provider setup screen is shown
    When I select "<provider>" provider
    And I choose "Enter manually"
    And I enter "<credential>" as credential
    And I press Escape
    Then credential should be <result>

    Examples:
      | provider      | credential          | result |
      | Anthropic     | sk-ant-api03-test   | saved  |
      | Anthropic     | sk-ant-oat01-oauth  | saved  |
      | Anthropic     | invalid-key         | rejected |
      | GitHub        | gho_test123456789   | saved  |
      | GitHub        | sk-ant-api03-test   | rejected |
