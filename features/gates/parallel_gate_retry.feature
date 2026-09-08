@gate-registration
Feature: Parallel Dispatch Gate Retry
  The post-member gate retry contract (PostMemberGateMaxAttempts /
  appendGateDirective forced tool-choice) must also hold on the
  PARALLEL dispatch path: a single gate miss on a parallel swarm
  re-dispatches the failing member with the gate directive before the
  swarm is allowed to fail. A dispatch-time "gate not registered"
  error is a hard configuration error — never retried, never absorbed.

  Scenario: Parallel swarm re-dispatches a member after a gate miss
    Given a parallel swarm whose reviewer fails the post-member gate once
    When the swarm is dispatched
    Then the reviewer is re-dispatched with the gate directive
    And the swarm completes successfully

  Scenario: Parallel swarm fails terminally after exhausting the retry budget
    Given a parallel swarm whose reviewer always fails the post-member gate
    When the swarm is dispatched
    Then the dispatch fails with the gate error
    And the reviewer was dispatched exactly PostMemberGateMaxAttempts times

  Scenario: A gate kind that is not registered is a hard config error
    Given a parallel swarm referencing ext:never-registered
    When the swarm is dispatched
    Then the dispatch fails with a configuration error naming the gate kind
    And the member is not re-dispatched
