Feature: Auto-compaction usage feedback
  As an agent platform operator
  I need the auto-compaction gate to weigh the provider-reported
  input-token figure from the most recent model turn alongside the
  engine's own estimate, so the trigger reflects what the provider
  actually billed rather than a heuristic alone — while sessions
  without a reported figure keep today's estimate-only trigger
  points byte-for-byte.

  Scenario: Reported input tokens fire the gate before the estimate would
    Given a usage-feedback engine is wired with a 0.50 auto-compaction threshold
    And 30 messages of 100 words each are seeded into the usage-feedback session store
    When the usage-feedback session streams a turn whose provider reports 6000 input tokens
    And the usage-feedback session streams another user turn
    Then auto-compaction fires on the provider-reported input-token figure

  Scenario: Without a reported figure the gate stays quiet below the estimate threshold
    Given a usage-feedback engine is wired with a 0.50 auto-compaction threshold
    And 30 messages of 100 words each are seeded into the usage-feedback session store
    When the usage-feedback session streams a turn whose provider reports no usage
    And the usage-feedback session streams another user turn
    Then auto-compaction stays quiet because the estimate is below the threshold

  Scenario: Without a reported figure the gate fires at the estimate trigger point
    Given a usage-feedback engine is wired with a 0.50 auto-compaction threshold
    And 51 messages of 100 words each are seeded into the usage-feedback session store
    When the usage-feedback session streams a turn whose provider reports no usage
    And the usage-feedback session streams another user turn
    Then auto-compaction fires at the estimate trigger point
