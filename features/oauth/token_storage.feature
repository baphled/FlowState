@oauth @security @token-storage
Feature: Encrypted OAuth Token Storage
  As a FlowState user
  I want my OAuth tokens to be securely stored
  So I don't need to re-authenticate every session

  Background:
    Given FlowState uses encrypted token storage

  @storage-encrypt
  Scenario: Token is encrypted before storage
    Given I have a raw OAuth access token
    When I store the token
    Then the stored token should be encrypted
    And the encrypted data should not contain the raw token

  @storage-decrypt
  Scenario: Token can be decrypted for use
    Given I have stored an encrypted token
    When I retrieve the token
    Then the retrieved token should match the original
    And decryption should complete within acceptable time

  @storage-file-permissions
  Scenario: Token file has restricted permissions
    Given I store a token
    Then the token file should have restricted permissions
    And only the owner should have read access

  @storage-missing-key
  Scenario: Handle missing encryption key
    Given no encryption key exists
    When I attempt to retrieve a stored token
    Then I should receive an error indicating key missing
    And I should be prompted to re-authenticate

  @storage-corrupt
  Scenario: Handle corrupted token storage
    Given a token file exists but is corrupted
    When I attempt to decrypt the token
    Then I should receive a decryption error
    And I should be prompted to re-authenticate

  @storage-provider-scoped
  Scenario: Tokens are scoped per provider
    Given I have tokens for multiple providers
    When I retrieve the GitHub token
    Then I should not receive tokens for other providers
    And each provider's token should be isolated

  @storage-rekey
  Scenario: Encryption key can be rotated
    Given I have a stored token with key version 1
    When I rotate to a new encryption key
    Then the token should be re-encrypted with the new key
    And the new key version should be stored

  @storage-cleanup
  Scenario: Token is removed when provider is unconfigured
    Given I have a stored GitHub token
    When I remove the GitHub provider configuration
    Then the stored token should be deleted
    And no residual token data should remain
