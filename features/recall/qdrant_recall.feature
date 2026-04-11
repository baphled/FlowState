@recall
Feature: Vector Recall via Qdrant
  As an agent
  I want to recall relevant information from my vector store
  So that I can provide semantically accurate responses

  Background:
    Given FlowState is running
    And the recall broker is initialised

  Scenario: Recall relevant results from Qdrant
    Given FlowState is configured with a Qdrant URL
    And the Qdrant store contains several memories
    When I perform a recall query for "project deadline"
    Then the broker should query the Qdrant source
    And the results should be ranked by semantic similarity score
    And the most relevant result should be returned first

  Scenario: Fall back gracefully when Qdrant is unavailable
    Given FlowState is running without a Qdrant store
    When I perform a recall query
    Then the recall broker should return an empty result set
    And no error should be reported to the user
