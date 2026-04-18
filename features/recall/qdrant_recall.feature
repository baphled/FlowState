@recall
Feature: Vector Recall via Qdrant
  As an agent
  I want to recall relevant information from my vector store
  So that I can provide semantically accurate responses

  Background:
    Given FlowState is running
    And the recall broker is initialised

  # @wip — the recall broker and Qdrant source (internal/recall/broker.go,
  # internal/recall/qdrant/) are implemented and unit-tested, but the BDD
  # glue that spins up a fake Qdrant, routes a recall query through the
  # broker, and asserts on ranked results has not been written. Tagged
  # @wip so the default `~@wip` filter skips them; `GODOG_TAGS=@wip`
  # re-runs them once the harness lands.
  @wip
  Scenario: Recall relevant results from Qdrant
    Given FlowState is configured with a Qdrant URL
    And the Qdrant store contains several memories
    When I perform a recall query for "project deadline"
    Then the broker should query the Qdrant source
    And the results should be ranked by semantic similarity score
    And the most relevant result should be returned first

  @wip
  Scenario: Fall back gracefully when Qdrant is unavailable
    Given FlowState is running without a Qdrant store
    When I perform a recall query
    Then the recall broker should return an empty result set
    And no error should be reported to the user
