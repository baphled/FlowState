@compaction @compaction-phase3
Feature: Summary prompt keep-list sections (Phase 3)
  As an agent platform operator
  I need the summarisation prompt to instruct the model to preserve intent,
  decisions, errors and their fixes, pending work and critical data, so the
  post-compaction summary keeps the load-bearing information the next turn
  needs.

  Scenario: The summary prompt names each keep-list section
    When the compaction summary prompt is rendered
    Then the prompt instructs the model to capture the session intent
    And the prompt instructs the model to capture decisions made
    And the prompt instructs the model to capture errors encountered and their fixes
    And the prompt instructs the model to capture pending work
    And the prompt instructs the model to capture critical data
