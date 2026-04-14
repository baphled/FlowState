Feature: Context Compression — Layers 1 and 2
  As an agent platform operator
  I need older messages to be elided into placeholders when they exceed
  the L1 token threshold so the provider window stays within budget
  while preserving the canonical transcript untouched (view-only) and
  never splitting a parallel tool-call group (tool-call atomicity).
  For Layer 2, I need long transcripts to be replaced with a structured
  summary whose Intent and NextSteps carry enough context to continue,
  and which can be rehydrated from a file allow-list on demand.

  Background:
    Given the L1 micro-compaction layer is configured with a 20-token threshold and a 2-message hot tail

  @micro-compaction
  Scenario: Disabled compaction passes every message through verbatim
    Given I have appended a sequence of 5 small assistant messages
    When the splitter runs
    Then no placeholder messages are emitted
    And the canonical recall view is unchanged

  @micro-compaction
  Scenario: Old large solo messages are elided into placeholders
    Given I have appended a sequence of 5 large assistant messages
    When the splitter runs
    Then at least 1 placeholder message is emitted
    And the canonical recall view is unchanged
    And the spill directory contains at least 1 atomic JSON payload

  @micro-compaction
  Scenario: A parallel tool-call fan-out group is compacted atomically
    Given I have appended a parallel fan-out group followed by 4 large solo messages
    When the splitter runs with hot tail size 1
    Then the resulting window contains no orphan tool-result messages
    And every spilled tool-group payload contains every message of its group

  @auto-compaction
  Scenario: Auto-compaction produces a validated summary from a scripted summariser
    Given the L2 summariser returns a valid compaction summary
    And I have a cold slice of 3 messages to summarise
    When the auto-compactor runs
    Then the resulting summary has a non-empty intent
    And the resulting summary has at least one next step
    And the summariser was called exactly once

  @auto-compaction
  Scenario: Auto-compaction rejects a summary missing next_steps without retrying
    Given the L2 summariser returns a summary with empty next_steps
    And I have a cold slice of 3 messages to summarise
    When the auto-compactor runs
    Then the auto-compactor returns an invalid-summary error
    And the summariser was called exactly once

  @auto-compaction
  Scenario: Rehydration restores the intent as a system message and each file as a tool message
    Given a compaction summary whose intent anchors the next turn
    And two files are queued for rehydration
    When the auto-compactor rehydrates
    Then the rehydrated window leads with a system message carrying the intent
    And the rehydrated window carries one tool message per queued file
