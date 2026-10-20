@failover @strict-fallback @local-exclusion
Feature: Strict failover never lands on local providers

  A strict-policy agent whose declared chain is hard-down may borrow the
  healthy global chain as a fallback tail — but a local provider
  (e.g. ollama) is never an acceptable stand-in for a strict agent:
  local models cannot meet the tool-calling bar the manifest pins. The
  strict tail skips local providers entirely, and when nothing but
  local providers remains the strict chain fails closed instead of
  silently degrading onto a local model.

  Scenario: A hard-down strict head skips local providers in the fallback tail
    Given a strict chain pinned to "anthropic" / "claude-sonnet-4"
    And the global chain is "zai" / "glm-4.6" and "ollama" / "llama3.2"
    And the pinned pair is hard-down
    And the pair "zai" / "glm-4.6" is cooldowned
    When the strict chain is resolved against the healthy fallback
    Then the resolved chain should be:
      | provider  | model           |
      | anthropic | claude-sonnet-4 |
    And no local provider should be in the resolved chain

  Scenario: A hard-down strict head keeps remote fallback entries
    Given a strict chain pinned to "anthropic" / "claude-sonnet-4"
    And the global chain is "zai" / "glm-4.6" and "ollama" / "llama3.2"
    And the pinned pair is hard-down
    When the strict chain is resolved against the healthy fallback
    Then the resolved chain should be:
      | provider  | model           |
      | anthropic | claude-sonnet-4 |
      | zai       | glm-4.6         |

  Scenario: A strict chain fails closed when only local providers remain
    Given a failover manager armed with base preferences "ollama" / "llama3.2"
    And the capability filter rejects every candidate
    And the manager is fail-closed
    When the manager candidates are listed
    Then the candidate list should be empty

  Scenario: A permissive manager still falls back to the unfiltered base
    Given a failover manager armed with base preferences "ollama" / "llama3.2"
    And the capability filter rejects every candidate
    When the manager candidates are listed
    Then the candidate list should contain "ollama" / "llama3.2"
