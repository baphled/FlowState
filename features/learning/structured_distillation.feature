@learning
Feature: Structured Knowledge Distillation
  As a system
  I want to distil my learning records into structured knowledge
  So that insights are concise and easily retrievable

  Background:
    Given FlowState is running
    And the learning hook is capturing data

  # @wip — the distillation pipeline (internal/learning/distiller.go) and
  # the Qdrant-backed vector store exist, but the BDD glue that runs the
  # pipeline end-to-end and asserts summaries land in the vector store has
  # not been wired. Tagged @wip so the default `~@wip` filter skips them.
  @wip
  Scenario: Distil learning records into summaries
    Given FlowState is configured with a Qdrant store
    And several learning entries have been recorded
    When the distillation pipeline runs
    Then entries should be distilled into structured summaries
    And the summaries should be stored in the vector store

  @wip
  Scenario: Skip distillation when Qdrant is unconfigured
    Given FlowState is running without a Qdrant configuration
    When the distillation process is triggered
    Then the distillation should be skipped gracefully
    And the system should remain stable
