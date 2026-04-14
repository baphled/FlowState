Feature: Context Compression — Layer 1 Micro-Compaction
  As an agent platform operator
  I need older messages to be elided into placeholders when they exceed
  the L1 token threshold so the provider window stays within budget
  while preserving the canonical transcript untouched (view-only) and
  never splitting a parallel tool-call group (tool-call atomicity).

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
