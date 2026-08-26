@oauth @github @smoke
Feature: GitHub OAuth Device Flow
  As a FlowState user
  I want to authenticate with GitHub using OAuth Device Flow
  So I can securely access GitHub Copilot without manually managing tokens

  Background:
    Given FlowState is configured for OAuth
    And no existing GitHub OAuth token is stored

  @oauth-initiate
  Scenario: Initiate GitHub Device Flow authentication
    Given I request GitHub OAuth authentication
    Then I should receive a device code
    And I should receive a user code
    And I should receive a verification URL
    And I should receive a polling interval

  @oauth-user-code
  Scenario: Display user code for manual verification
    Given I initiate GitHub OAuth
    Then the user code should be displayed
    And the verification URL should be displayed
    And I should be instructed to visit the URL within the expiry time

  @oauth-user-approval
  Scenario: Poll for user approval status
    Given I have initiated GitHub OAuth
    When I approve the authorization in browser
    Then the polling should return a success status
    And I should receive an access token
    And I should receive a token type
    And the token should have an expiry time

  @oauth-pending-await
  Scenario: Handle pending authorization state
    Given I have initiated GitHub OAuth
    And I have not yet approved in browser
    When I poll for authorization status
    Then I should receive a pending status
    And I should be told to continue waiting

  @oauth-expired
  Scenario: Handle expired authorization request
    Given I have initiated GitHub OAuth
    And the authorization has expired
    When I poll for authorization status
    Then I should receive an expired status
    And I should be instructed to restart the flow

  @oauth-rate-limit
  Scenario: Handle rate limiting during polling
    Given I have initiated GitHub OAuth
    When GitHub rate limits the polling
    Then I should receive a rate limited error
    And I should wait for the specified interval before retrying

  @oauth-slow-user
  Scenario: Handle slow user authorization
    Given I have initiated GitHub OAuth
    And the device code expires in 300 seconds
    When I poll periodically for up to 300 seconds
    And I eventually approve in browser
    Then I should still receive a valid access token

  @oauth-scope
  Scenario: Request correct OAuth scopes for Copilot
    Given I request GitHub OAuth authentication
    Then the request should include "copilot" scope
    And the request should include appropriate device flow parameters

  @oauth-token-storage
  Scenario: Token is stored after successful authentication
    Given I complete GitHub OAuth authentication
    Then the access token should be stored securely
    And the token should be encrypted at rest
