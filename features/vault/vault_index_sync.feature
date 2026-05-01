Feature: Vault index and sync commands
  As an agent or operator
  I want to trigger vault indexing and incremental sync from the FlowState CLI
  So that the RAG knowledge base stays current without manually running the vault server

  Background:
    Given a temporary vault directory with markdown files

  @vault @smoke
  Scenario: Index command indexes all files on first run
    When I run the vault index command
    Then the exit code should be 0
    And the output should contain "indexed"
    And the sidecar state file should exist

  @vault @smoke
  Scenario: Index command reports a summary
    When I run the vault index command
    Then the exit code should be 0
    And the output should contain "total="
    And the output should contain "indexed="

  @vault
  Scenario: Index command accepts --vault-root flag
    When I run the vault index command with "--vault-root" set to the temp vault
    Then the exit code should be 0
    And the output should contain "indexed"

  @vault
  Scenario: Index command accepts --collection flag
    When I run the vault index command with "--collection" set to "test-collection"
    Then the exit code should be 0

  @vault
  Scenario: Index command exits non-zero when vault root does not exist
    When I run the vault index command with "--vault-root" set to "/nonexistent/vault"
    Then the exit code should not be 0

  @vault @smoke
  Scenario: Sync command indexes only changed files
    Given the vault has been indexed once
    When I run the vault sync command
    Then the exit code should be 0
    And the output should contain "skipped="

  @vault
  Scenario: Sync command reports files that were updated
    Given the vault has been indexed once
    And a vault file has been modified since the last index
    When I run the vault sync command
    Then the exit code should be 0
    And the output should contain "indexed="

  @vault
  Scenario: Sync command accepts --vault-root flag
    When I run the vault sync command with "--vault-root" set to the temp vault
    Then the exit code should be 0

  @vault
  Scenario: Index command with --reindex flag forces all files to be re-embedded
    Given the vault has been indexed once
    When I run the vault index command with "--reindex"
    Then the exit code should be 0
    And the output should contain "indexed="

  @vault
  Scenario: Agent can call the vault_index MCP tool
    Given FlowState is configured with vault RAG
    When an agent calls the "vault_index" tool with the temp vault path
    Then the tool result should indicate success
    And the result should include a summary with "indexed" count

  @vault
  Scenario: Agent can call the vault_sync MCP tool
    Given FlowState is configured with vault RAG
    And the vault has been indexed once
    When an agent calls the "vault_sync" tool with the temp vault path
    Then the tool result should indicate success
    And the result should include a summary with "skipped" count
