@engine @engine-parity @child-overflow-recovery
Feature: Parent/child engine parity for context-overflow recovery
  As a swarm operator
  I need a delegated child stream that overflows the context window to
  earn the same compact-and-retry recovery the parent stream already
  performs, instead of failing closed on the first
  context_window_exceeded refusal —
  so parent and child engines degrade identically ("engines are
  engines") and a swollen child transcript recovers through
  compaction rather than terminating the delegation chain.

  Scenario: A delegate child whose stream overflows compacts and recovers like the parent
    Given a parent engine and an identical delegate child engine sharing a small context limit
    And both the parent transcript and the child transcript are swollen past the context gate
    When the parent stream is dispatched
    Then the parent recovers via overflow compaction and completes
    When the delegated child stream is dispatched through the delegate tool
    Then the child also recovers via overflow compaction and the delegation completes

  Scenario: A child that still overflows after the recovery retry fails closed with compaction-insufficient
    Given a parent engine and a delegate child engine whose summariser always fails
    And both the parent transcript and the child transcript are swollen past the context gate
    When the delegated child stream is dispatched through the delegate tool
    Then the child retry still overflows after compaction
    And the delegation fails with a terminal compaction-insufficient error
