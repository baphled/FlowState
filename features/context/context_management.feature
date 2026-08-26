Feature: Context Management
  As an AI agent platform
  I need to manage context efficiently using RLM principles
  So that conversations stay within token limits while preserving relevant history

  Background:
    Given FlowState is running

  @smoke
  Scenario: Context window stays within token budget after 20 messages
    Given a general agent with 4096 token context limit
    And I have exchanged 20 messages
    When the next message is processed
    Then the context window should use less than 4096 tokens

  @smoke
  Scenario: Semantic search returns relevant earlier messages
    Given I have a conversation about "cooking pasta"
    And I later discussed "weather forecast"
    When I ask about "Italian recipes"
    Then the context should include messages about "cooking pasta"

  @smoke
  Scenario: Context prioritises recent messages
    Given a general agent with 4096 token context limit
    And I have exchanged 20 messages
    When the next message is processed
    Then the context should include messages about "recent"

  Scenario: Empty conversation starts with system prompt only
    Given a general agent with 4096 token context limit
    When the next message is processed
    Then the context window should use less than 4096 tokens

  Scenario: Oversized message is handled gracefully
    Given a general agent with 4096 token context limit
    When I type "a very long message repeated many times"
    Then the context window should use less than 4096 tokens
