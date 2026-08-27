@wip @delegation @integrity
Feature: Delegation completion integrity

  Coordinator delegates fan out to specialist agents whose work is
  collected synchronously by the parent stream. Two integrity defects
  undermine the swarm's due-diligence verdicts: (1) when the parent
  stream's context is cancelled after a delegate has already completed
  and persisted its work, the collection loop reports "context
  canceled" — a false negative for work that succeeded; and (2) a
  delegate that finishes with an empty, non-substantive response after
  the bounded retry budget is only warn-logged and then reported as a
  normal success — a false positive.

  Policy: collection is detached from parent cancellation once the
  child stream has produced output (fail-open for completed work), and
  empty completions fail closed with a terminal error once the retry
  budget is exhausted.

  Scenario: A member delegate that completes work must report success, not context canceled
    Given a delegate child stream that produces substantive output and closes
    When the parent stream context is cancelled after the child output arrives
    And the delegation result is collected
    Then the delegation reports success with the child output
    And the delegation error is nil

  Scenario: An empty completed response must not be reported as a normal success
    Given a delegate child stream that produces no substantive output and closes
    When the retry budget is exhausted
    And the delegation result is collected
    Then the delegation fails with an empty-response terminal error
    And the delegation status is not reported as completed
