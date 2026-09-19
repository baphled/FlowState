@engine
Feature: A session cannot go on forever

  Invariant: every ratchet the engine uses — the same-tool-pattern cap,
  the todo no-progress guard, the tool-loop duration budget, mid-loop
  compaction, and re-prompt chains — must bound the session from above,
  and the session itself must carry a lifetime bound. A budget that
  resets on every continuation window is not a budget.

  These scenarios are written tests-first against the current engine.
  Scenarios marked as documenting violations are expected to FAIL; the
  failures are the deliverable and must not be "fixed" by weakening the
  scenario. See docs/invariants/TURN_LIFECYCLE_INVARIANTS.md.

  Scenario: Repeated identical tool calls with narration text trip the same-tool-pattern cap
    Given a scripted model that narrates identical tool requests every round
    When the bounded turn is streamed to completion
    Then the turn is stopped by the same-tool-pattern cap

  Scenario: The todo no-progress guard trips despite ongoing tool activity
    Given a scripted model that keeps calling tools while its todo list never changes
    And a pending todo is seeded for the bounded-session scenarios
    When the bounded turn is streamed to completion
    Then the no-progress guard stops the turn within three continuations

  Scenario: Continuation injection does not reset the tool-loop duration budget
    Given a scripted model that keeps calling tools while its todo list never changes
    And a pending todo is seeded for the bounded-session scenarios
    And the engine is capped at a tool-loop duration of 150ms
    And each tool call stalls the engine for 40ms
    When the bounded turn is streamed to completion
    Then the whole turn finishes within twice its original duration cap

  Scenario: Mid-loop compaction does not re-fire when the achievable trim is marginal
    Given a scripted model that narrates identical tool requests every round
    And a token budget that sits at the compaction gate after a handful of tool batches
    And the summariser only ever trims a sliver of the window
    When the bounded turn is streamed to completion
    Then the mid-loop compactor fires at most once

  Scenario: A session has a lifetime bound
    Given a scripted model that ends every turn without finishing its todos
    And a pending todo is seeded for the bounded-session scenarios
    When six auto-continued turns are streamed on the same session
    Then the session is refused or flagged once its lifetime budget is spent

  Scenario: Background-task re-prompt chain is bounded
    Given a completion orchestrator is watching background tasks
    When four background tasks complete one after another
    Then the re-prompt chain stops at three sends
