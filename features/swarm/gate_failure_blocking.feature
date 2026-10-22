@swarm @gate-enforcement
Feature: Gate failure blocking enforcement
  As a swarm orchestrator
  I want a failing gate to abort the turn and surface a structured record
  So that gate rejections stop the run instead of being logged as warnings

  Background:
    Given a swarm with a halting post-member gate that always fails

  Scenario: A gate failure aborts the turn and no further work occurs
    When the member completes its stream and the post-member gate is dispatched
    Then the gate dispatch reports a halt
    And the delegation returns the gate error instead of a tool result
    And no further provider or tool calls occur for the turn

  Scenario: Loading a swarm manifest referencing an unregistered gate kind fails fast
    Given a swarm manifest whose gate references kind "ext:mental-health-safety" with no such gate registered
    When the manifest is validated against the gate registry
    Then validation fails with an error naming the unregistered gate kind

  Scenario: The failed-gate record is surfaced through the API in a structured form
    When a gate failure is published on the event bus for the session
    Then the turn record carries a gate failure with gate name, kind, lifecycle, member id and reason
    And the swarm events stream projects the failure as a gate event with status "failed"
