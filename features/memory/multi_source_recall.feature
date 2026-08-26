@memory
Feature: Multi-Source Recall
  As a user
  I want the agent to draw on multiple memory sources
  So that responses are enriched with my personal knowledge and history

  Background:
    Given FlowState is running
    And the agent system is initialised

  # P9b: the multi-source glue in features/support/recall_learning_steps.go
  # wires MCPMemorySource and vault.Source behind the recall broker using
  # in-process stubs so scenarios exercise the real broker fan-out without
  # a live MCP memory / vault-rag server.
  Scenario: Recall from the MCP memory graph
    Given FlowState is configured with an MCP memory server
    And I have previously asked the agent to remember that "the project deadline is Friday"
    When I ask the agent "When is the project deadline?"
    Then the response should mention that the deadline is Friday
    And the agent should recognise the information came from the memory graph

  Scenario: Recall from the vault-rag knowledge base
    Given FlowState is configured with the vault-rag MCP server
    And my knowledge base contains a note about "British English conventions"
    When I ask the agent "What are the British English conventions for FlowState?"
    Then the response should draw on the content from the knowledge base vault
    And the response should follow the recorded conventions
