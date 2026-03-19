Feature: Provider Setup with OAuth
  As a user
  I want to choose between API key and OAuth authentication
  So that I can use the most convenient and secure method for each provider

  Background:
    Given FlowState is running
    And I am in provider setup

  @smoke
  Scenario: Choose authentication method for OAuth-capable provider
    Given I select "GitHub Copilot" as provider
    Then I should see authentication options:
      | Option    |
      | API Key   |
      | OAuth     |
    When I select "OAuth"
    Then the OAuth device flow should be initiated

  Scenario: API key authentication remains available
    Given I select "GitHub Copilot" as provider
    When I select "API Key" authentication
    Then I should see an input field for the API key
    And I should not see OAuth-related UI
    When I enter a valid API key
    Then the provider should be configured
    And no OAuth token should be stored

  Scenario: OAuth not available for API-key-only providers
    Given I select "Anthropic Claude" as provider
    Then I should only see "API Key" authentication option
    And I should not see "OAuth" option

  Scenario: Display OAuth progress indicator
    Given I have initiated OAuth device flow
    When token polling is in progress
    Then I should see a progress indicator
    And I should see "Waiting for authorization..."
    And I should see elapsed time
    And I should be able to cancel

  Scenario: Cancel OAuth flow mid-process
    Given I have initiated OAuth device flow
    And I see the device code
    When I press Escape
    Then the OAuth flow should be cancelled
    And I should return to provider setup
    And no token should be stored

  Scenario: Switch between authentication methods
    Given I have configured "GitHub Copilot" with API key
    When I edit the provider configuration
    Then I should see "Switch to OAuth" option
    When I select "Switch to OAuth"
    Then the API key should be cleared
    And the OAuth device flow should be initiated

  Scenario: Previously authenticated OAuth provider
    Given I have a stored OAuth token for "GitHub Copilot"
    When I view provider settings
    Then I should see "Authenticated via OAuth" status
    And I should see token expiry information
    And I should see options to:
      | Option              |
      | Refresh token       |
      | Revoke access       |
      | Switch to API key   |

  Scenario: OAuth device code display formatting
    Given I have initiated OAuth device flow
    When the device code is displayed
    Then the code should be large and readable
    And the code should be visually distinct
    And the verification URL should be a clickable hint
    And copy-to-clipboard hint should be shown
