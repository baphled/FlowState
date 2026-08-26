Feature: Per-vault query collection resolution
  As an agent calling the query_vault tool
  I want the vault argument to scope my search to that vault's Qdrant collection
  So that recall hits come from the right knowledge base without extra configuration

  Background:
    Given a vault query handler with default collection "flowstate-vault"

  @vault-query
  Scenario: A query without a vault argument searches the default collection
    When an agent queries the vault without a vault argument
    Then the search should target collection "flowstate-vault"

  @vault-query
  Scenario: A query naming a known vault searches that vault's collection
    When an agent queries the vault with vault argument "baphled"
    Then the search should target collection "flowstate-vault-baphled"

  @vault-query
  Scenario: A query naming an unknown vault falls back to the default collection
    When an agent queries the vault with vault argument "nonsense"
    Then the search should target collection "flowstate-vault"

  @vault-query
  Scenario: A multi-word vault name slugifies to its per-vault collection
    When an agent queries the vault with vault argument "Book Of YoNix"
    Then the search should target collection "flowstate-vault-book-of-yonix"

  @vault-query
  Scenario: A mixed-case vault name with special characters slugifies to its per-vault collection
    When an agent queries the vault with vault argument "FullSpektrum®"
    Then the search should target collection "flowstate-vault-fullspektrum"

  @vault-query
  Scenario: A configured default override still targets the per-vault collection for a known vault
    Given a vault query handler with default collection "flowstate-recall"
    When an agent queries the vault with vault argument "colledge"
    Then the search should target collection "flowstate-vault-colledge"

  @vault-query
  Scenario: A whitespace-only vault argument falls back to the default collection
    When an agent queries the vault with vault argument "   "
    Then the search should target collection "flowstate-vault"
