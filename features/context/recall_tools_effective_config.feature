@recall-tools
Feature: Recall tools follow the effective Qdrant configuration
  As an operator who enables Qdrant via the QDRANT_URL environment variable
  I want the query_vault and mcp_memory tools registered for agents that declare them
  So that recall-capable agents work whether Qdrant came from the config file or the environment

  Background:
    Given a sandboxed FlowState environment with an agent "recall-probe" that declares the recall tools
    And no Qdrant URL is set in the FlowState configuration file

  Scenario: Recall tools register when Qdrant is configured only through the environment
    When FlowState wires its engine tools with QDRANT_URL set to "http://env-qdrant:6333"
    Then the "mcp_vault-rag_query_vault" tool is registered for the agent
    And the "mcp_memory_search_nodes" tool is registered for the agent
    And the "mcp_memory_open_nodes" tool is registered for the agent

  Scenario: Recall tools stay absent when Qdrant is not configured anywhere
    When FlowState wires its engine tools with QDRANT_URL unset
    Then the "mcp_vault-rag_query_vault" tool is not registered for the agent
    And the "mcp_memory_open_nodes" tool is not registered for the agent
