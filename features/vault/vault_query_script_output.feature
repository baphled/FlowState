Feature: Vault query script retrieval-only output
  As an agent calling the vault rag query tool
  I want the query-vault script to retrieve sources directly without LLM resolution
  So that LlamaIndex never prints the spurious "LLM is explicitly disabled. Using MockLLM." notice and no "Answer: None" line pollutes tool output

  @vault-query @smoke
  Scenario: The embedded query-vault script retrieves sources without a query engine
    When I inspect the embedded query-vault script
    Then the script should not configure an explicit LLM
    And the script should not build a query engine
    And the script should retrieve nodes directly from the retriever
    And the script should still print a Sources section
