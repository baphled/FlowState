Feature: GitHub OAuth Device Flow
  As a user
  I want to authenticate with GitHub using OAuth Device Flow
  So that I can securely connect FlowState to GitHub-powered AI providers

  Background:
    Given FlowState is running
    And I am in provider setup

  @smoke
  Scenario: Initiate device flow and display user code
    Given I select "GitHub Copilot" as provider
    And I choose "OAuth" as authentication method
    When the device flow is initiated
    Then I should see a device code displayed
    And I should see the verification URL "https://github.com/login/device"
    And I should see instructions to enter the code

  Scenario: Successfully complete device flow authentication
    Given I have initiated device flow
    And I see the device code "ABCD-1234"
    When I authorize the device on GitHub
    And the token polling completes successfully
    Then I should see "Authentication successful"
    And the GitHub token should be stored securely
    And I should be returned to the provider list

  Scenario: Device flow expires before authorization
    Given I have initiated device flow
    And I see the device code "ABCD-1234"
    When the device code expires
    Then I should see "Device code expired"
    And I should be prompted to retry
    And no token should be stored

  Scenario: User denies authorization on GitHub
    Given I have initiated device flow
    And I see the device code "ABCD-1234"
    When I deny authorization on GitHub
    Then I should see "Authorization denied"
    And I should be returned to the provider setup
    And no token should be stored

  Scenario: Rate limiting during token polling
    Given I have initiated device flow
    And the GitHub API is rate limiting requests
    When token polling occurs
    Then the polling should respect rate limits
    And I should see a waiting indicator
    And polling should continue with backoff

  Scenario: Network error during device flow
    Given I have initiated device flow
    When a network error occurs during polling
    Then I should see "Network error occurred"
    And I should be prompted to retry
    And no token should be stored
