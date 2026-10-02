Feature: Summariser Chain — ordered multi-provider fallback (Phase 1)
  As an agent platform operator
  I need the L2 summariser to walk an ordered provider chain — the
  configured summariser provider first (Z.AI when credentials are
  present), Anthropic second, Ollama last with a configurable model —
  so a single dead provider never silently degrades compaction into a
  no-op or an overflowing raw-history dispatch.

  Scenario: Summarisation succeeds via the configured Z.AI provider
    Given a summariser chain with Z.AI configured and healthy
    And a cold slice of 3 messages to summarise
    When the summariser chain runs
    Then the summary is produced by provider "zai"
    And the chain status is "ok"

  Scenario: Z.AI unavailable falls back to Anthropic
    Given a summariser chain with Z.AI configured but failing
    And Anthropic healthy
    And a cold slice of 3 messages to summarise
    When the summariser chain runs
    Then the summary is produced by provider "anthropic"
    And the chain status is "ok"
    And the chain attempted providers are "zai,anthropic"

  Scenario: All providers fail returns a typed error, never a silent no-op
    Given a summariser chain with Z.AI, Anthropic, and Ollama all failing
    And a cold slice of 3 messages to summarise
    When the summariser chain runs
    Then the chain returns a summariser unavailable error
    And the chain status is "unavailable"
    And the chain attempted providers are "zai,anthropic,ollama"

  Scenario: The Ollama last resort uses the configured model name
    Given a summariser chain with Z.AI and Anthropic failing
    And Ollama healthy with model "qwen3:14b"
    And a cold slice of 3 messages to summarise
    When the summariser chain runs
    Then the summary is produced by provider "ollama"
    And the Ollama request used model "qwen3:14b"
    And the chain status is "ok"

  Scenario: An empty summariser response is treated as a failure and walks the chain
    Given a summariser chain with Z.AI returning an empty summary
    And Anthropic healthy
    And a cold slice of 3 messages to summarise
    When the summariser chain runs
    Then the summary is produced by provider "anthropic"
    And the chain status is "ok"

  Scenario: Marshal failure in the engine degrades to the loud truncation fallback
    Given an engine compaction run whose summary marshal fails
    When the engine collects the compaction result
    Then the result is the truncation fallback summary
    And the result is never the empty string
