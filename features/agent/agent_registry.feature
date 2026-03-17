Feature: Agent Registry
  As a FlowState operator
  I want FlowState to discover available agents
  So that the right agents can be offered at runtime

  @smoke
  Scenario: Discover agents from JSON and Markdown manifests
    Given an agent directory contains valid JSON and Markdown agent manifests
    When the agent registry discovers agents from that directory
    Then the registry should include agents from both manifest formats

  Scenario: Empty directory returns an empty registry
    Given an empty agent directory
    When the agent registry discovers agents from that directory
    Then the registry should be empty

  Scenario: Invalid agent manifests are skipped gracefully
    Given an agent directory contains valid and invalid agent manifests
    When the agent registry discovers agents from that directory
    Then the valid agents should be available in the registry
    And the invalid agent manifests should be skipped
