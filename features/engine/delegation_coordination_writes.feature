@engine @delegation
Feature: Delegation coordination-write enforcement

  Scenario: A synchronous delegate completing without coordination writes fails closed
    Given delegation coordination-write enforcement is enabled
    And a delegate child stream that completes substantive output without coordination writes
    When the delegation result is collected
    Then the parent receives a tool result carrying a coordination-write error
    And the log contains "without writing any coordination_store keys"

  Scenario: A delegate that writes coordination keys reports success
    Given delegation coordination-write enforcement is enabled
    And a delegate child stream that completes and writes a coordination key
    When the delegation result is collected
    Then the delegation reports success with the child output

  Scenario: Enforcement is disabled by configuration
    Given delegation coordination-write enforcement is disabled
    And a delegate child stream that completes substantive output without coordination writes
    When the delegation result is collected
    Then the delegation reports success with the child output
