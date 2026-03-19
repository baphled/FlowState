Feature: OAuth Token Storage
  As a user
  I want my OAuth tokens stored securely
  So that my authentication persists across sessions without exposing credentials

  Background:
    Given FlowState is running

  @smoke
  Scenario: Store OAuth token with encryption
    Given I have obtained a GitHub OAuth token
    When the token is saved to storage
    Then the token file should exist at "$XDG_DATA_HOME/flowstate/tokens.enc"
    And the token file should be encrypted
    And the token file permissions should be 0600

  Scenario: Retrieve stored OAuth token
    Given I have a stored encrypted OAuth token for "GitHub Copilot"
    When I start FlowState
    And I select "GitHub Copilot" as provider
    Then the stored token should be decrypted
    And the provider should be authenticated automatically
    And I should not be prompted for authentication

  Scenario: Handle missing token file
    Given no OAuth token is stored for "GitHub Copilot"
    When I select "GitHub Copilot" as provider
    Then I should be prompted to authenticate
    And I should see OAuth authentication options

  Scenario: Handle corrupted token file
    Given I have a corrupted token file
    When FlowState attempts to read the token
    Then I should see "Token file corrupted"
    And I should be prompted to re-authenticate
    And the corrupted file should be backed up

  Scenario: Refresh expired OAuth token
    Given I have a stored OAuth token for "GitHub Copilot"
    And the token has expired
    When I attempt to use the provider
    Then the token should be refreshed automatically
    And the new token should be stored
    And the provider request should succeed

  Scenario: Handle refresh token expired
    Given I have a stored OAuth token for "GitHub Copilot"
    And both access and refresh tokens have expired
    When I attempt to use the provider
    Then I should see "Authentication expired"
    And I should be prompted to re-authenticate via device flow
    And the expired token should be removed

  Scenario: Token storage across multiple providers
    Given I have OAuth tokens for multiple providers
      | Provider        | Status  |
      | GitHub Copilot  | Active  |
      | Another Service | Active  |
    When I list configured providers
    Then each provider should show authentication status
    And I should be able to revoke individual tokens
