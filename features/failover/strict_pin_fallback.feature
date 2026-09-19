@failover @strict-fallback
Feature: Strict pin fallback to the healthy chain

  A strict manifest pinned to a provider that has tripped the hard-down
  breaker must not doom the agent: the healthy global chain is appended
  as a fallback tail at chain construction, one WARN names the pinned
  pair and the fallback, and the dead pin is never re-inserted into the
  attempt order. A healthy pin keeps the strict chain untouched.

  Scenario: A hard-down strict head appends the healthy global chain
    Given a strict chain pinned to "anthropic" / "claude-sonnet-4"
    And the global chain is "zai" / "glm-4.6" and "ollama" / "llama3.2"
    And the pinned pair is hard-down
    And the pair "zai" / "glm-4.6" is cooldowned
    When the strict chain is resolved against the healthy fallback
    Then the resolved chain should be:
      | provider  | model          |
      | anthropic | claude-sonnet-4 |
      | ollama    | llama3.2       |

  Scenario: A healthy strict head keeps the chain strict
    Given a strict chain pinned to "anthropic" / "claude-sonnet-4"
    And the global chain is "zai" / "glm-4.6" and "ollama" / "llama3.2"
    When the strict chain is resolved against the healthy fallback
    Then the resolved chain should be:
      | provider  | model           |
      | anthropic | claude-sonnet-4 |

  Scenario: A hard-down pinned pair is never attempted and the healthy fallback completes the turn
    Given a failover hook with a single candidate "anthropic" / "claude-sonnet-4"
    And the global chain is "zai" / "glm-4.6" and "ollama" / "llama3.2"
    And the pinned pair is hard-down
    And the fallback provider "ollama" succeeds
    When the failover hook executes a chat request pinned to "anthropic" / "claude-sonnet-4"
    Then the turn should complete on "ollama"
    And the pinned provider should never be attempted
