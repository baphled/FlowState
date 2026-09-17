Feature: Token-budgeted context-window truncation
  As an agent platform operator
  I need the engine's overflow fallback, mid-loop compaction rebuild, and
  token-bounded rebuild to bound the surviving window by the usable token
  budget rather than fixed message counts, and to count the prepended
  system-prompt prefix in that budget, so a recovered request never
  overshoots the model's usable window and the newest message always
  survives.

  Token arithmetic shared by every scenario (wordE2ECounter: one token
  per word, 10_000-token limit, defaultOutputReserve 4_096):
  usable = 10_000 - 4_096 = 5_904 and target = usable * 4 / 5 = 4_723.

  Scenario: Overflow fallback truncation fits the token target and keeps the newest message
    Given a token-budget engine is wired with auto-compaction disabled and a 10000-token limit
    And 80 messages of 100 words each are seeded into the token-budget session store
    When the token-budget session streams a turn that overflows the usable window
    Then the surviving overflow-fallback request fits the 4723-token target
    And the surviving overflow-fallback request keeps the newest message

  Scenario: Mid-loop compaction rebuild respects the same token target
    Given a token-budget engine is wired with the wordy echo tool, a 0.99 threshold and a 10000-token limit
    And 53 messages of 100 words each are seeded into the token-budget session store
    When the token-budget session streams a turn whose provider calls the echo tool
    Then the rebuilt mid-loop continuation request fits the 4723-token target
    And the rebuilt mid-loop continuation request keeps the newest message

  Scenario: Token-bounded rebuild accounts for a large system-prompt prefix
    Given a token-budget engine is wired with an 800-word system prompt, a 0.99 threshold and a 10000-token limit
    And 52 messages of 100 words each are seeded into the token-budget session store
    When the token-budget session streams a turn that overflows the usable window
    Then the compacted retry request fits the 4723-token target
    And the compacted retry request keeps the newest message
