@auth @cli @smoke
Feature: Provider Authentication
  As a FlowState user
  I want to authenticate with AI providers
  So that I can securely access provider APIs without exposing credentials

  Background:
    Given the FlowState CLI is available
    And no existing provider tokens are stored

  @auth-github-oauth @oauth
  Scenario: Authenticate with GitHub Copilot via OAuth Device Flow
    When I run "flowstate auth github-copilot"
    Then I should see "Starting GitHub OAuth authentication..."
    And I should see "Device code:"
    And I should see "User code:"
    And I should see "Verification URL: https://github.com/login/device"
    And I should see "Waiting for authorization..."
    When GitHub authorization completes successfully
    Then I should see "✓ Authentication successful"
    And I should see "Token stored securely"
    And the encrypted token should exist in ~/.local/share/flowstate/tokens/github.token
    And config.yaml should contain oauth settings for github-copilot

  @auth-github-oauth-expired @oauth
  Scenario: Handle expired GitHub OAuth authorization
    When I run "flowstate auth github-copilot"
    And the GitHub authorization expires before completion
    Then I should see "✗ Authorization expired"
    And I should see "Please restart the authentication flow"
    And no token should be stored

  @auth-anthropic-api-key
  Scenario: Authenticate with Anthropic via API key
    When I run "flowstate auth anthropic"
    Then I should see "Enter your Anthropic API key:"
    And the input should be hidden
    When I enter "sk-ant-api03-test-key-12345678901234567890123456789012"
    Then I should see "✓ API key saved successfully"
    And config.yaml should contain "api_key: sk-ant-api03-test-key-12345678901234567890123456789012"

  @auth-anthropic-invalid-key
  Scenario: Reject invalid Anthropic API key format
    When I run "flowstate auth anthropic"
    And I enter "invalid-key-format"
    Then I should see "✗ Invalid API key format"
    And I should see "Expected format: sk-ant-api03-..."
    And config.yaml should not contain the invalid key

  @auth-help
  Scenario: Show available authentication providers
    When I run "flowstate auth --help"
    Then I should see usage for "flowstate auth"
    And I should see the subcommand "github-copilot"
    And I should see the subcommand "anthropic"
    And I should see "Authenticate with AI providers"
