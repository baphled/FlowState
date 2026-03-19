@oauth @tui @provider-setup
Feature: TUI OAuth Flow for Provider Configuration
  As a FlowState user
  I want to authenticate via OAuth in the TUI
  So I can configure providers without leaving the application

  Background:
    Given FlowState is running
    And the provider setup screen is shown

  @oauth-tui-select
  Scenario: Select GitHub provider for OAuth
    Given I am on the providers step
    When I select "GitHub Copilot" provider
    Then I should see "OAuth" as an authentication option
    And I should see "API Key" as an alternative option

  @oauth-tui-initiate
  Scenario: Initiate OAuth from TUI
    Given I have selected "GitHub Copilot" provider
    When I choose the OAuth authentication option
    Then I should see the OAuth flow initiated message
    And I should be shown the user code to enter
    And I should be shown the verification URL

  @oauth-tui-url-launch
  Scenario: URL is displayed for easy access
    Given OAuth flow is initiated
    Then the verification URL should be prominently displayed
    And the user should be instructed to visit the URL

  @oauth-tui-code-display
  Scenario: User code is clearly displayed
    Given OAuth flow is initiated
    Then the user code should be in a monospace font
    And the user code should be highlighted for easy copying

  @oauth-tui-polling
  Scenario: TUI polls for authorization status
    Given OAuth flow is initiated
    When I approve in browser
    Then the TUI should detect the approval
    And I should see a success message

  @oauth-tui-success
  Scenario: OAuth success is indicated
    Given OAuth authentication completes
    Then I should see "Authentication successful"
    And GitHub Copilot should be marked as configured
    And I should be able to return to provider list

  @oauth-tui-timeout
  Scenario: OAuth timeout is handled gracefully
    Given OAuth flow is initiated
    And the authorization times out
    When the polling detects timeout
    Then I should see a timeout error message
    And I should be given the option to retry

  @oauth-tui-manual-fallback
  Scenario: Can fall back to API key from OAuth screen
    Given OAuth flow is in progress
    When I choose to cancel OAuth
    Then I should be given the option to enter an API key instead
    And I should see the API key input field

  @oauth-tui-error
  Scenario: OAuth errors are displayed clearly
    Given OAuth flow encounters an error
    Then I should see a clear error message
    And I should be given the option to retry
    And the error should not crash the TUI

  @oauth-tui-copilot-enabled
  Scenario: Copilot is enabled after OAuth success
    Given OAuth authentication completes for GitHub Copilot
    When I exit the provider setup
    Then GitHub Copilot should appear as enabled in the provider list
    And the Copilot provider should be ready to use
